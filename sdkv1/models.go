package sdkv1

import "github.com/nats-io/nats.go"

// PluginIntro represents the plugin introduction response
// Subject: inflow.v1.<PLUGIN_ID>.@intro
type PluginIntro struct {
	Name     string       `json:"name"`
	Author   string       `json:"author"`
	Version  string       `json:"version"`
	Settings *FormBuilder `json:"settings,omitempty"` // this field is same with setting as requirement data before any action and its can be use as onboard stage
	// Manual is an optional Markdown document the host renders on the plugin's
	// page (the FloMorphic Extensions view) — a README/help panel the developer
	// writes to explain the plugin. Beyond prose, the host upgrades a fenced
	// ```inflow-meta block, whose body is a meta method name, into a Run button
	// that calls inflow.v1.<PLUGIN_ID>.<method> through the host proxy and shows
	// the raw JSON reply beneath it. Since the doc author is also the meta author,
	// no extra descriptor is needed — the method name in the fence is enough.
	// Optional; leave empty for no manual.
	Manual string `json:"manual,omitempty"`
}

// PluginAction represents a single plugin action
// Subject: inflow.v1.<PLUGIN_ID>.@actions
type Action struct {
	Method         string         `json:"method"`
	Description    string         `json:"description"`
	Title          string         `json:"title"`
	Icon           Icon           `json:"icon"`
	RequestHandler JobHandler     `json:"-"`
	Form           FormBuilder    `json:"form"`
	Outbound       []OutboundPort `json:"outbound,omitempty"`
	// Tags is an open bag of string labels for grouping and classifying an
	// action. It lets a single plugin binary host several logical products —
	// e.g. a Google plugin that bundles Docs, Sheets and Calendar actions — and
	// tell them apart on the `@actions` list.
	//
	// The reserved key "class" names the sub-product an action belongs to, so
	// the frontend can group ports by it: tags["class"] = "sheet" / "docs" /
	// "calendar". Any other keys are free-form metadata. Optional; leave nil for
	// a single-class plugin.
	Tags map[string]string `json:"tags,omitempty"`
}

// OutboundPort statically declares one of a node's outbound branches. It is the
// design-time counterpart of runtime tag routing (Job.CmdNextFilter /
// `next_tags`): the whole Action.Outbound slice is served on the `@actions`
// subject, so the frontend renders one output port per entry — labelled by
// Title, explained by Description — and stamps every edge drawn from that port
// with the port's Tags. At runtime the handler calls
// `job.CmdNextFilter(port.Tags)` to fire only the branch(es) whose tags it
// names; edges carrying other tags are skipped.
//
// The field is optional and, deliberately, not essential. The same branching is
// achievable today by mixing a plugin with a downstream **contract node** that
// fans out on the committed result — declaring Outbound here is only a
// convenience that keeps the port topology, its tags, and its documentation on
// the action itself instead of wiring them by hand on the canvas. Leave it nil
// for the common single-output action.
type OutboundPort struct {
	Title       string   `json:"title"`
	Tags        []string `json:"tags"`
	Description string   `json:"description,omitempty"`
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
	// Excluded from JSON like every other handler on this file: @settings
	// marshals this struct to serve the form, and a func field makes that
	// marshal fail — which silently drops the response.
	SubmitHandler func(Request) Response `json:"-"`
}

// CommandPayload is the body shipped with every in-job progress command
// (Job.Progress / Job.Done / Job.DoneWithError). Progress selects the stage:
//
//   - Frame  (Progress in [1,99]): a non-terminal update. The core renders
//     Frame on the node — a pie chart from Progress plus the frame's title and
//     content. Details may carry partial data.
//   - Result (Progress 100): the terminal payload. Details is committed to the
//     node's scope, at CommitOn when set. Error is set only by DoneWithError
//     (and its variants) and is what makes the finished job a failed one —
//     Details is still committed either way.
//
// The core mirrors this as models.CommandPayload — keep the two in sync.
type CommandPayload struct {
	Progress int            `json:"progress" bson:"progress"`
	Frame    Frame          `json:"frame" bson:"frame"`
	Details  map[string]any `json:"details"`
	CommitOn string         `json:"commit_on"`
	Error    *ErrorPayload  `json:"error,omitempty" bson:"error,omitempty"`
}

// ErrorPayload is how a terminal command reports a failure. Its presence — not
// its contents — is the verdict: the core concludes the job failed whenever the
// field is there, even with an empty Message.
//
// Code is the plugin's own error number, in the plugin's own numbering. The core
// does not interpret it or map it onto a fractal status; it carries it so the
// plugin's owner can be asked what it means. Leave it 0 when the plugin has no
// such numbering.
type ErrorPayload struct {
	Code    int    `json:"code" bson:"code"`
	Message string `json:"message" bson:"message"`
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
type CallSvcBody struct{
	Data any `json:"data"`
	OperationData map[string]any `json:"op"`
}

// SignalHandler receives every message that lands on the plugin's signal port.
// Registered with Plugin.OnSignal.
type SignalHandler func(sig Signal)

// Signal is one runtime message on the plugin's signal port,
// `inflow.plugin.<PLUGIN_ID>.<KIND>` — a broadcast OUT of the runtime about a
// process, not a request: nothing is expected back and no reply is read.
//
// Today the only kind is RuntimeProcessSignal ("proc"), published when the
// runtime finishes with a plugin node process; the port is a wildcard
// subscription, so future kinds arrive at the same handler with a different
// Kind and, possibly, a payload this struct does not model — hence Data.
type Signal struct {
	// Kind is the subject remainder after `inflow.plugin.<PLUGIN_ID>.`, e.g.
	// "proc". Switch on it before trusting the parsed fields below.
	Kind PluginSignal
	// Subject is the full NATS subject the signal arrived on.
	Subject string
	// JobId is the job this signal is about — the very uuid the SDK minted in
	// the request→job handshake and handed to the handler as Job.JobId, so a
	// plugin can match a signal to the work it still has in flight.
	JobId string
	// Conclusion is how the runtime ended that process. Set for "proc" signals;
	// empty for a kind that carries no conclusion.
	Conclusion Conclusion
	// Data is the raw payload, kept verbatim so an unmodelled future kind is
	// still readable.
	Data []byte
	// Msg is the underlying NATS message (headers, subject, reply). Present for
	// the escape hatch; a signal is a publish, so do not respond to it.
	Msg *nats.Msg
}

// signalBody is the JSON the runtime publishes on a "proc" signal. Parsed into
// Signal's typed fields; a payload that does not fit leaves them zero and is
// still delivered as Data.
type signalBody struct {
	Conclusion string `json:"conclusion"`
	JobId      string `json:"jobId"`
}
