package sdkv1

import (
	"github.com/nats-io/nats.go"
)

// PluginIntro represents the plugin introduction response
// Subject: inflow.v1.<PLUGIN_ID>.@intro
type PluginIntro struct {
	Name     string       `json:"name"`
	Author   string       `json:"author"`
	Version  string       `json:"version"`
	Settings *FormBuilder `json:"settings,omitempty"` // this field is same with setting as requirement data before any action and its can be use as onboard stage
}

// PluginAction represents a single plugin action
// Subject: inflow.v1.<PLUGIN_ID>.@actions
type Action struct {
	Method         string              `json:"method"`
	Description    string              `json:"description"`
	Title          string              `json:"title"`
	Icon           Icon                `json:"icon"`
	RequestHandler func(ActionRequest) `json:"-"`
	Form           FormBuilder         `json:"form"`
}

type Meta struct {
	Method         string                 `json:"method"`
	RequestHandler func(Request) Response `json:"-"`
}

// Icon represents an icon for an action
type Icon struct {
	Ref  string `json:"ref"`
	Icon string `json:"icon"`
}

// FormBuilder represents the action form configuration
// Subject: inflow.v1.<PLUGIN_ID>.<ACTION>.@form
type FormBuilder struct {
	SubmitTo   string `json:"submit_to"` // name of a meta func for live validation
	Jsonui     string `json:"jsonui"`
	Jsonschema string `json:"jsonschema"`
}
type Settings struct {
	FormBuilder
	SubmitHandler func(Request) Response
}
type CommandPayload struct {
	Progress int            `json:"progress" bson:"progress"`
	Frame    Frame          `json:"frame" bson:"frame"`
	Details  map[string]any `json:"details"`
}

type Frame struct {
	Title   string `json:"title" bson:"title"`
	Content string `json:"content" bson:"content"`
}

type JobBodyContent struct {
	JobId    string         `json:"jobId"`
	Progress int            `json:"progress"`
	Details  map[string]any `json:"details"`
	CommitOn string         `json:"commit_on"`
}

type ActionRequestContent struct {
	Registry map[string]any `json:"_registry"`
	Body     map[string]any `json:"body"`
}
type Request struct {
	Msg *nats.Msg
	Plugin IPlugin

}
