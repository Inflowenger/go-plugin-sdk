package sdkv1

type Command string

const (
	ProgressCommand       Command = "progress"
	ContextCurrentCommand Command = "context/current"
	ContextPathCommand    Command = "context/path"
	JobCommandCommit      Command = "commit"
	JobCommandNextTags    Command = "next_tags"
	JobCommandRequest     Command = "request/svc"
)

// PluginSignal is the kind of a runtime signal: the subject remainder after
// `inflow.plugin.<PLUGIN_ID>.` — see Signal and Plugin.OnSignal. The signal port
// is a one-way, fire-and-forget channel OUT of the runtime, parallel to the
// `inflow.v1` (describe me) and `inflow.cpu` (run me) planes; nothing on it is a
// request, so a handler never replies.
type PluginSignal string

const (
	// RuntimeProcessSignal ("proc") is published once per plugin node process,
	// the moment the runtime stops attending it — on every outcome, not only
	// cancellation. Its payload is `{"conclusion":"<Conclusion>","jobId":"<uuid>"}`,
	// where jobId is the same id the SDK minted for that job.
	RuntimeProcessSignal PluginSignal = "proc"
)

// Conclusion is how the runtime ended a plugin node process, as carried by a
// RuntimeProcessSignal. It mirrors models.PluginConclusion in fractal-core —
// keep the two in sync.
//
// Whatever the value, the runtime is no longer listening on that job's command
// subjects once the signal is out: further Progress/Done/context calls from a
// still-running handler will find no responder.
type Conclusion string

const (
	// ConclusionDone — the job reported progress 100 and its details were
	// committed to the node's scope. The ordinary, successful ending.
	ConclusionDone Conclusion = "done"
	// ConclusionNext — the process ended on a routing command (`next_tags`).
	ConclusionNext Conclusion = "next"
	// ConclusionFlowStopByUser — a user (or a stop command) halted the running
	// flow. This is the cancellation case: the handler is probably still working.
	ConclusionFlowStopByUser Conclusion = "flow_stop_by_user"
	// ConclusionCommandStop — the flow was stopped by an explicit stop command.
	ConclusionCommandStop Conclusion = "stop_command"
	// ConclusionTimeout — the workflow's own deadline expired while the job ran.
	ConclusionTimeout Conclusion = "timeout"
	// ConclusionLongTimeWithoutCommand — the node's idle window (`idle_min`)
	// passed with no command from the plugin, so the runtime gave up waiting.
	ConclusionLongTimeWithoutCommand Conclusion = "long_time_without_command"
	// ConclusionBadRequest — a command carried a payload or path the runtime
	// could not accept.
	ConclusionBadRequest Conclusion = "bad_request"
	// ConclusionExceededRequestAnomaly — the job issued an abnormal number of
	// commands (>1500) and was cut off.
	ConclusionExceededRequestAnomaly Conclusion = "anomaly_request"
	// ConclusionFailure — the flow was failed with an error.
	ConclusionFailure Conclusion = "failure"
	// ConclusionInternalError — the runtime failed on its own side (e.g. the
	// commit could not be written).
	ConclusionInternalError Conclusion = "internal_error"
	// ConclusionPluginNotResponded — the plugin never acknowledged the execution
	// request with a jobId.
	ConclusionPluginNotResponded Conclusion = "plugin_not_responded"
	// ConclusionUnknownCause — the context was cancelled without a recognizable
	// cause. Note the runtime's spelling ("unknow_cause") is deliberate.
	ConclusionUnknownCause Conclusion = "unknow_cause"
)

// Succeeded reports whether the process ended the way the job intended — the
// handler finished (`done`) or routed onward (`next`).
func (c Conclusion) Succeeded() bool {
	return c == ConclusionDone || c == ConclusionNext
}

// Canceled reports whether the process was cut short by a decision outside the
// job — a user stopping the flow, a workflow timeout, or the idle window
// expiring — rather than by the handler finishing or erroring. This is the
// condition to test when a handler holds work that should be abandoned; see
// Plugin.OnSignal for why abandoning is opt-in and not the default.
func (c Conclusion) Canceled() bool {
	switch c {
	case ConclusionFlowStopByUser, ConclusionCommandStop, ConclusionTimeout, ConclusionLongTimeWithoutCommand:
		return true
	}
	return false
}
