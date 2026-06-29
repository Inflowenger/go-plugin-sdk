package sdkv1

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	natsHandler "github.com/Inflowenger/go-plugin-sdk/nats"
	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

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
	// event := NewEventLogger(p.sdk)
	// actionsByte, _ := sonic.Marshal(p.Actions)
	// if len(actionsByte) > 0 {
	// 	actionsList := []map[string]any{}
	// 	if err := sonic.Unmarshal(actionsByte, &actionsList); err == nil {
	// 		event.Log("soren-sdk-init", models.LogLevelInfo, "start plugin", map[string]any{"actions": actionsList})
	// 	}
	// }

	log.Println("Plugin context done, exiting plugin:", p.intro.Name)
	return nil
}
func (p *Plugin) introHandler() error {
	conn := p.infraConn.GetConnection()
	if conn == nil {
		return fmt.Errorf("connection error occurred")
	}
	conn.Subscribe(p.makeIntroSubject(), func(msg *nats.Msg) {
		// Handle the intro message
		introByte, err := sonic.Marshal(p.Intro)
		if err != nil {
			return
		}
		msg.Respond(introByte)
	})
	if p.intro.Settings != nil {
		if strings.TrimSpace(p.intro.Settings.SubmitTo) == "" {
			log.Println("no setting service defined")
			return nil
		}
	}
	return nil
}

func (p *Plugin) settingsHandler() error {
	// show settings form handler
	conn := p.infraConn.GetConnection()
	if conn == nil {
		return fmt.Errorf("connection error occurred")

	}
	conn.Subscribe(p.makeSettingsSubject(), func(msg *nats.Msg) {
		fmt.Println("Settings Called")
		// Handle the settings message
		if p.settings == nil {
			msg.Respond(nil)
			return
		}
		settingsByte, err := sonic.Marshal(p.settings)
		if err != nil {
			return
		}
		msg.Respond(settingsByte)
	})
	// settings submit handler
	if p.settings != nil {
		if strings.TrimSpace(p.settings.SubmitTo) == "" {
			p.settings.SubmitTo = "_settings.config.submit"
			// log.Println("no setting service defined")
			// return nil
		}
		conn.Subscribe(p.makeSubject(p.settings.SubmitTo), func(msg *nats.Msg) {
			if p.settings.SubmitHandler == nil {
				msg.Respond([]byte(`{"status":"not implemented"}`))
				return
			}
			res := p.settings.SubmitHandler(RequestBody{Data: msg.Data, Header: msg.Header, Subject: msg.Subject})
			resByte, err := sonic.Marshal(res)
			if err != nil {
				fmt.Println(err.Error())
				msg.Respond([]byte(`{"error":"error occurred in marshal response"}`))
				return
			}
			msg.Respond(resByte)

		})
	}

	return nil
}

func WithDotEnv(envFile string) func(*Plugin) error {
	return func(p *Plugin) error {
		env := NewEnv(envFile)
		p.PluginId = env.getEnvVar("PLUGIN_ID")
		credential := env.getEnvVar("INFRA_CRED")
		infraUrl := env.getEnvVar("INFRA_URL")
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
func (p *Plugin) Intro(i PluginIntro) {
	p.intro = i
}
func (p *Plugin) RequiredParams(requirements *Settings) {
	p.settings = requirements
}
func (p *Plugin) AddAction(act ...Action) {
	p.actions = append(p.actions, act...)
}
func (p *Plugin) metaFunchandler() {
	conn := p.infraConn.GetConnection()
	if conn == nil {
		fmt.Printf("connection error occurred")
		return
	}

	for _, metafn := range p.metaFn {
		_, err := conn.Subscribe(p.makeSubject(metafn.Method), func(msg *nats.Msg) {
			res := metafn.RequestHandler(RequestBody{Data: msg.Data, Header: msg.Header, Subject: msg.Subject})
			resByte, err := sonic.Marshal(res)
			if err != nil {
				fmt.Println(err.Error())
				msg.Respond([]byte(`{"error":"error occurred in marshal response"}`))
				return
			}
			msg.Respond(resByte)
		})
		if err != nil {
			log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeSubject(metafn.Method))
			return
		}
		log.Printf("Meta Function Service : %s", p.makeSubject(metafn.Method))
	}
}

func (p *Plugin) actionsHandler() {
	conn := p.infraConn.GetConnection()
	if conn == nil {
		fmt.Printf("connection error occurred")
		return
	}
	conn.Subscribe(p.makeActionsListSubject(), func(msg *nats.Msg) {
		// Handle the actions list message
		listBytes, err := sonic.Marshal(p.actions)
		if err != nil {
			log.Printf("Failed to marshal actions: %v", err)
			return
		}
		msg.Respond(listBytes)
	})
	for _, action := range p.actions {
		_, err := conn.Subscribe(p.makeFormSubject(action.Method), func(msg *nats.Msg) {
			// Handle the action message
			formBody, err := sonic.Marshal(action.Form)
			if err != nil {
				log.Println("action form ", action.Title, " error:", err)
				return
			}
			msg.Respond(formBody)
		})
		if err != nil {
			log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeFormSubject(action.Method))
			return
		}
		log.Printf("Form Builder Service : %s", p.makeFormSubject(action.Method))
		// request handler make a jobId and respond it with the result
		_, err = conn.Subscribe(p.makeActionCpu(action.Method), func(msg *nats.Msg) {
			if action.RequestHandler == nil {
				fmt.Printf("recv new request message on action %s\n", action.Method)
				return
			}
			jId := uuid.New().String()
			newReq := ActionRequest{JobId: jId, Action: action.Method, Body: RequestBody{Data: msg.Data, Header: msg.Header, Subject: msg.Subject}}
			action.RequestHandler(newReq)

		})
		if err != nil {
			log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeActionCpu(action.Method))
			return
		}
		log.Printf("Subscribed Action : %s", p.makeActionCpu(action.Method))
	}

}

func (p *Plugin) makeSubject(action string) string {

	return fmt.Sprintf("inflow.v1.%s.%s", p.PluginId, action)
}

// makeSettingsSubject creates a subject with the inflow.v1 prefix
func (p *Plugin) makeSettingsSubject() string {
	return fmt.Sprintf("inflow.v1.%s.@settings", p.PluginId)
}

// makeActionsListSubject creates a subject with the inflow.v1 prefix
func (p *Plugin) makeActionsListSubject() string {
	return fmt.Sprintf("inflow.v1.%s.@actions", p.PluginId)
}

// makeIntroSubject creates a subject with the inflow.v1 prefix
func (p *Plugin) makeIntroSubject() string {
	return fmt.Sprintf("inflow.v1.%s.@intro", p.PluginId)
}

// makeActionCpu creates a subject for CPU/job processing (original purpose)
func (p *Plugin) makeActionCpu(action string) string {
	return fmt.Sprintf("soren.cpu.%s.%s", p.PluginId, action)
}

// makeJobSubject creates a subject for job updates (CPU pattern)
func (p *Plugin) makeJobSubject(jobID, jobUpdate string) string {
	return fmt.Sprintf("soren.cpu.%s.%s.%s", p.PluginId, jobID, jobUpdate)
}

// makeFormSubject creates a subject for form requests
func (p *Plugin) makeFormSubject(action string) string {
	return fmt.Sprintf("inflow.v1.%s.%s.@form", p.PluginId, action)
}

// "context/path"
// makeFormSubject creates a subject for getting data from current context at runtime
func (p *Plugin) makeGetDataSubject(jobID string) string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", p.PluginId, jobID, "context/path")
}

// "context/current"
func (p *Plugin) makeGetCurrentPathSubject(jobID string) string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", p.PluginId, jobID, "context/current")
}

// "commit"
func (p *Plugin) makeCommitOnPathSubject(jobID string) string {
	return fmt.Sprintf("inflow.cpu.%s.%s.%s", p.PluginId, jobID, "commit")
}
