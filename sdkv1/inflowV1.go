package sdkv1

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// introPayload is the body of an @intro reply.
//
// Split out of the handler so the reply can be tested without a connection,
// because a marshal failure here is invisible on the wire: the handler has
// nothing to send, so it stays silent and the caller sees a plain timeout —
// indistinguishable from a plugin that is not running. That is exactly how this
// regressed. It marshalled `p.Intro`, the setter *method*: a func value, which
// no encoder accepts, so no plugin ever answered @intro. Marshal the `intro`
// FIELD.
func (p *Plugin) introPayload() ([]byte, error) {
	return sonic.Marshal(p.intro)
}

// settingsPayload is the body of a @settings reply. A plugin that requires
// nothing still answers, with an empty object: an empty body is not JSON, so a
// caller could not tell "asks for nothing" from "not running".
func (p *Plugin) settingsPayload() ([]byte, error) {
	if p.settings == nil {
		return []byte("{}"), nil
	}
	return sonic.Marshal(p.settings)
}

func (p *Plugin) introHandler() error {
	conn := p.infraConn.GetConnection()
	if conn == nil {
		return fmt.Errorf("connection error occurred")
	}
	_,err:=conn.Subscribe(p.makeIntroSubject(), func(msg *nats.Msg) {
		introByte, err := p.introPayload()
		if err != nil {
			log.Printf("intro: marshal failed: %v", err)
			return
		}
		msg.Respond(introByte)
	})
	if err!= nil {
		log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeIntroSubject())
		return fmt.Errorf("failed to subscribe to intro subject")
	}else{
		log.Printf("Intro Subscribed on : %s", p.makeIntroSubject())
	}
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
	_,err:=conn.Subscribe(p.makeSettingsSubject(), func(msg *nats.Msg) {
		fmt.Println("Settings Called")
		settingsByte, err := p.settingsPayload()
		if err != nil {
			log.Printf("settings: marshal failed: %v", err)
			return
		}
		msg.Respond(settingsByte)
	})
	if err != nil {
		log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeSettingsSubject())
		return fmt.Errorf("failed to subscribe to settings subject")
	}else{
		log.Printf("Settings Subscribed on : %s", p.makeSettingsSubject())
	}
	// settings submit handler
	if p.settings != nil {
		if strings.TrimSpace(p.settings.SubmitTo) == "" {
			p.settings.SubmitTo = "_settings.config.submit"
			// log.Println("no setting service defined")
			// return nil
		}
		conn.Subscribe(p.makeActionSubject(p.settings.SubmitTo), func(msg *nats.Msg) {
			if p.settings.SubmitHandler == nil {
				msg.Respond([]byte(`{"status":"not implemented"}`))
				return
			}

			res := p.settings.SubmitHandler(Request{Data: slices.Clone(msg.Data), Header: maps.Clone(msg.Header), Plugin: p})
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

// AddMeta registers one or more meta methods (see the Meta type). Each is served
// as a synchronous RPC on inflow.v1.<PLUGIN_ID>.<Method>; call it before Start.
func (p *Plugin) AddMeta(meta ...Meta) {
	p.metaFn = append(p.metaFn, meta...)
}
func (p *Plugin) metaFunchandler() {
	conn := p.infraConn.GetConnection()
	if conn == nil {
		fmt.Printf("connection error occurred")
		return
	}

	for _, metafn := range p.metaFn {
		_, err := conn.Subscribe(p.makeActionSubject(metafn.Method), func(msg *nats.Msg) {
			res := metafn.RequestHandler(Request{Data: slices.Clone(msg.Data), Header: maps.Clone(msg.Header), Plugin: p})
			resByte, err := sonic.Marshal(res)
			if err != nil {
				fmt.Println(err.Error())
				msg.Respond([]byte(`{"error":"error occurred in marshal response"}`))
				return
			}
			msg.Respond(resByte)
		})
		if err != nil {
			log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeActionSubject(metafn.Method))
			return
		}
		log.Printf("Meta Function Service : %s", p.makeActionSubject(metafn.Method))
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
			newReq := ActionRequest{JobId: jId, Action: action.Method, Req: Request{Data: slices.Clone(msg.Data), Header: maps.Clone(msg.Header), Plugin: p}}
			WithJobHandler(action.RequestHandler)(newReq, msg)
		})
		if err != nil {
			log.Printf("subscribe error: %s on %s\n", err.Error(), p.makeActionCpu(action.Method))
			return
		}
		log.Printf("Subscribed Action : %s", p.makeActionCpu(action.Method))
	}

}

func (p *Plugin) makeActionSubject(action string) string {

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
