package sdkv1

import "github.com/nats-io/nats.go"

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
	Method         string      `json:"method"`
	Description    string      `json:"description"`
	Title          string      `json:"title"`
	Icon           Icon        `json:"icon"`
	RequestHandler JobHandler  `json:"-"`
	Form           FormBuilder `json:"form"`
}

// Meta is a live, request/response helper method a plugin exposes OUTSIDE the
// job lifecycle. Unlike an Action (which spawns a Job and reports progress), a
// meta method is a plain RPC: the frontend/back-end calls it synchronously to
// fetch data it needs to build a dialog *before* a job runs — e.g. the MCP
// node's "list tools" call, which connects to a server and returns its tools so
// the drawer can render one output port / arg form per tool.
//
// Subject: inflow.v1.<PLUGIN_ID>.<Method>. The handler returns any JSON-able
// value (a struct, a slice, a map) and the SDK marshals it verbatim — so a meta
// method can answer with a bare array (e.g. []McpTool) when that is the shape
// the caller expects, not only the {data,error} Response envelope.
type Meta struct {
	Method         string            `json:"method"`
	RequestHandler func(Request) any `json:"-"`
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

// CommandPayload is the body shipped with every in-job progress command
// (Job.Progress / Job.Done / Job.DoneWithError). Progress selects the stage:
//
//   - Frame  (Progress in [1,99]): a non-terminal update. The core renders
//     Frame on the node — a pie chart from Progress plus the frame's title and
//     content. Details may carry partial data.
//   - Result (Progress 100): the terminal payload. Details is committed to the
//     node's scope, at CommitOn when set. A failed job (DoneWithError) sends its
//     reason as Details["error"].
//
// The core mirrors this as models.CommandPayload — keep the two in sync.
type CommandPayload struct {
	Progress int            `json:"progress" bson:"progress"`
	Frame    Frame          `json:"frame" bson:"frame"`
	Details  map[string]any `json:"details"`
	CommitOn string         `json:"commit_on"`
}

// Frame is the human-readable content of a sub-100 progress update: Title labels
// the frame, Content is the streamed status body shown on the node. Meta is a
// reserved, open bag for frontend-effective extras the frame wants to render
// (e.g. an "items" list) without changing this contract; leave it nil when
// unused.
type Frame struct {
	Title   string         `json:"title" bson:"title"`
	Content string         `json:"content" bson:"content"`
	Meta    map[string]any `json:"meta,omitempty" bson:"meta,omitempty"`
}

// JobBodyContent is the init response (it assigns the JobId subsequent commands
// are addressed to) and the body of a bare commit command (Job.CmdSetOnPath).
// On init failure the reason is carried as Details["error"].
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
	Data   []byte
	Header nats.Header
	Plugin IPlugin
}

type RequestBody[T any] struct {
	Registry map[string]any `json:"_registry"`
	Body     T              `json:"body"`
}
