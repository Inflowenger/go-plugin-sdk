package sdkv1

import (
	"fmt"
	"log"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

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
			res := p.settings.SubmitHandler(Request{Msg: msg, Plugin: p})
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
			res := metafn.RequestHandler(Request{Msg: msg, Plugin: p})
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
			newReq := ActionRequest{ msg: msg, JobId: jId, Action: action.Method, Req: Request{Msg: msg, Plugin: p}}
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
	return fmt.Sprintf("inflow.cpu.%s.%s", p.PluginId, action)
}


// makeFormSubject creates a subject for form requests
func (p *Plugin) makeFormSubject(action string) string {
	return fmt.Sprintf("inflow.v1.%s.%s.@form", p.PluginId, action)
}

