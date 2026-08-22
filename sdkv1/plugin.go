package sdkv1

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"time"

	natsHandler "github.com/Inflowenger/go-plugin-sdk/nats"
	"github.com/nats-io/nats.go"
)

type IPlugin interface {
	Send(subject string, data []byte) (*nats.Msg, error)
	GetPluginId()string
}
// DefaultSendTimeout is the NATS request/reply deadline for Send when the plugin
// author doesn't set one. A conservative 5s: fine for the fast RPCs (account
// list, settings test, a single email send). A plugin whose actions proxy slower
// upstream calls — a multi-message search, a large fetch — should raise it in
// code with WithTimeout(), since the deadline must sit above whatever the backend
// needs to answer or the reply is abandoned mid-flight.
const DefaultSendTimeout = 5 * time.Second

// ReqTimeoutEnv is an env var, in SECONDS, that overrides the send timeout at
// deploy time — so an operator can widen it for a slow network (REQ_TIMEOUT=50)
// or tighten it, without touching code. Read in NewPlugin AFTER the options run,
// so it wins over the developer's WithTimeout. WithDotEnv (if used) has already
// loaded the .env file into the environment by then.
const ReqTimeoutEnv = "REQ_TIMEOUT"

type Plugin struct {
	PluginId    string
	infraConn   *natsHandler.Nats
	intro       PluginIntro
	settings    *Settings
	actions     []Action
	metaFn      []Meta
	sendTimeout time.Duration
}

func NewPlugin(opts ...func(*Plugin) error) (*Plugin, error) {
	p := &Plugin{sendTimeout: DefaultSendTimeout}
	for _, o := range opts {
		err := o(p)
		if err != nil {
			return nil, err
		}
	}
	// Operator override, applied last so REQ_TIMEOUT beats the developer's
	// WithTimeout. WithDotEnv (if used) has already loaded the .env file.
	if d, ok := reqTimeoutEnv(); ok {
		p.sendTimeout = d
	}
	return p, nil
}

// reqTimeoutEnv reads REQ_TIMEOUT (seconds) into a duration, reporting ok=false
// when unset, blank, non-numeric, or non-positive (leaving the code/default).
func reqTimeoutEnv() (time.Duration, bool) {
	raw, ok := os.LookupEnv(ReqTimeoutEnv)
	if !ok || raw == "" {
		return 0, false
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		log.Printf("Invalid %s=%s, ignoring", ReqTimeoutEnv, raw)
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
func (p *Plugin) Start() error {
	err := p.introHandler()
	if err != nil {
		return err
	}
	err = p.settingsHandler()
	if err != nil {
		return err
	}
	p.actionsHandler()
	p.metaFunchandler()

	return nil
}
func (p *Plugin) GetPluginId()string{
	return p.PluginId
}
func (p *Plugin) Send(subject string, data []byte) (*nats.Msg, error) {
	conn := p.infraConn.GetConnection()
	if conn == nil {
		fmt.Printf("connection error occurred")
		return nil, fmt.Errorf("connection error")
	}
	timeout := p.sendTimeout
	if timeout <= 0 {
		timeout = DefaultSendTimeout
	}
	for retry := range 5 {
		msg, err := conn.Request(subject, data, timeout)
		if err != nil {
			if err == nats.ErrNoResponders {
				if retry > 2 {
					log.Default().Printf("No responders - retry :%d", retry)
					log.Default().Printf("No responders - body : %s", string(data))

				}
				time.Sleep(time.Duration(retry+1) * time.Second)
				continue

			}
			log.Println("subs : ", subject)
			log.Println("body : ", string(data))

			return msg, err
		}
		if err := conn.Flush(); err != nil {
			log.Println("progress command flush error:", err)
			return msg, err
		}

		fmt.Printf("result of %s  :  %s \n", subject, string(msg.Data))
		return msg, err

	}
	return nil, fmt.Errorf("exception occurred")

}
func WithDotEnv(envFile string) func(*Plugin) error {
	return func(p *Plugin) error {
		env := NewEnv(envFile)
		p.PluginId = env.getEnvVar("PLUGIN_ID")
		credential := env.getEnvVar("INFRA_CRED")
		infraUrl := env.getEnvVar("INFRA_URL")
		_, err := url.Parse(infraUrl)
		if err != nil {
			return err
		}
		ic, err := natsHandler.New(credential, infraUrl)
		if err != nil {
			return err
		}
		p.infraConn = ic
		return nil
	}
}

// WithTimeout sets the NATS request/reply deadline for Send, in SECONDS. Declare
// it where the plugin is constructed, e.g.
// NewPlugin(WithDotEnv(f), WithTimeout(65)). Omit it to keep DefaultSendTimeout
// (5s). A non-positive value is ignored. The REQ_TIMEOUT env var, when set,
// overrides this at deploy time.
func WithTimeout(seconds int) func(*Plugin) error {
	return func(p *Plugin) error {
		if seconds > 0 {
			p.sendTimeout = time.Duration(seconds) * time.Second
		}
		return nil
	}
}

func WithPluginId(pluginId string) func(*Plugin) error {
	return func(p *Plugin) error {
		p.PluginId = pluginId
		return nil
	}
}

func WithInfraConnection(infraUrl, credential string) func(*Plugin) error {
	return func(p *Plugin) error {
		u, err := url.Parse(infraUrl)
		if err != nil {
			return err
		}
		ic, err := natsHandler.New(credential, u.Host)
		if err != nil {
			return err
		}
		p.infraConn = ic
		return nil
	}
}
