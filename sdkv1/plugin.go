package sdkv1

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	signalFn    SignalHandler
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
	if err := p.signalsHandler(); err != nil {
		return err
	}

	return nil
}
func (p *Plugin) GetPluginId()string{
	return p.PluginId
}

// OnSignal registers the handler for the plugin's signal port — every subject
// under `inflow.plugin.<PLUGIN_ID>.>`, the runtime's one-way broadcast channel
// about processes this plugin is running (see Signal). Call it before Start,
// which does the subscribing; passing nil registers a handler that only logs
// what arrives, which is enough to watch the port during development.
//
// It is entirely OPTIONAL. A plugin that never calls it behaves exactly as
// before, and that is the norm: when a process is stopped or times out, the
// job the plugin took on deliberately keeps running, because a later process
// may pick up where it left off — the runtime hands the previous jobId back in
// `_registry`, so progress made after the stop is not wasted. Register a
// handler only for the cases where the work itself must also stop: a stream to
// close, an upstream call to abort, a reservation to release. Then test
// sig.Conclusion.Canceled() and cancel the work you filed under sig.JobId.
//
// Only the last registered handler is kept. Handlers run on their own
// goroutine, so signals for different jobs may overlap, and a panic inside one
// is recovered and logged rather than taking the process down.
func (p *Plugin) OnSignal(handler SignalHandler) {
	if handler == nil {
		handler = func(sig Signal) {
			log.Printf("signal on %s received: %s", sig.Subject, string(sig.Data))
		}
	}
	p.signalFn = handler
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
// normalizeInfraURL turns whatever INFRA_URL carries into the "host:port" the
// NATS handler dials. Both spellings are accepted: the bare "host:port" the
// Infra API hands out, and a full "nats://host:port" for anyone who writes the
// scheme out.
//
// url.Parse cannot validate the bare form on its own. With no scheme, Go reads
// "inflow-infra:4222" as scheme "inflow-infra" — which happens to succeed — and
// rejects an endpoint whose host does not start with a letter:
//
//	parse "172.28.0.1:4222": first path segment in URL cannot contain colon
//
// That is every deployment pointed at Infra by IP, so parsing under an assumed
// scheme is what makes the two spellings behave the same.
func normalizeInfraURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("INFRA_URL is empty")
	}
	if !strings.Contains(s, "://") {
		s = "nats://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid INFRA_URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid INFRA_URL %q: no host", raw)
	}
	return u.Host, nil
}

func WithDotEnv(envFile string) func(*Plugin) error {
	return func(p *Plugin) error {
		env := NewEnv(envFile)
		p.PluginId = env.getEnvVar("PLUGIN_ID")
		credential := env.getEnvVar("INFRA_CRED")
		infraUrl, err := normalizeInfraURL(env.getEnvVar("INFRA_URL"))
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
		host, err := normalizeInfraURL(infraUrl)
		if err != nil {
			return err
		}
		ic, err := natsHandler.New(credential, host)
		if err != nil {
			return err
		}
		p.infraConn = ic
		return nil
	}
}

func WithConnection(nc *natsHandler.Nats) func(*Plugin) error {
	return func(p *Plugin) error {
		p.infraConn = nc
		return nil
	}
}

