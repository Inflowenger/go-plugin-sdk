package sdkv1

import (
	"fmt"
	"log"
	"net/url"
	"time"

	natsHandler "github.com/Inflowenger/go-plugin-sdk/nats"
	"github.com/nats-io/nats.go"
)

type IPlugin interface {
	Send(subject string, data []byte) (*nats.Msg, error)
	GetPluginId()string
}
type Plugin struct {
	PluginId  string
	infraConn *natsHandler.Nats
	intro     PluginIntro
	settings  *Settings
	actions   []Action
	metaFn    []Meta
}

func NewPlugin(opts ...func(*Plugin) error) (*Plugin, error) {
	p := &Plugin{}
	for _, o := range opts {
		err := o(p)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
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
	for retry := range 5 {
		msg, err := conn.Request(subject, data, 3*time.Second)
		if err != nil {
			if err == nats.ErrNoResponders {
				if retry > 2 {
					log.Default().Printf("No responders - retry :%d", retry)
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
