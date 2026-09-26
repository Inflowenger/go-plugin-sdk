package sdkv1

import (
	"testing"

	"github.com/nats-io/nats.go"
)

// A signal is a publish, not a request: nothing on the wire tells the plugin it
// mis-read one. So the two things parseSignal must get right are checked here —
// the kind is the subject past the plugin's own prefix, and the runtime's
// `{conclusion, jobId}` body lands in the typed fields — plus the case that has
// no answer yet: a future kind with a payload this SDK does not model still
// reaches the handler, with its bytes intact.

func TestParseSignalProc(t *testing.T) {
	p := &Plugin{PluginId: "abc-123"}
	sig := p.parseSignal(&nats.Msg{
		Subject: "inflow.plugin.abc-123.proc",
		Data:    []byte(`{"conclusion":"flow_stop_by_user","jobId":"9f0c1f8e-1111-2222-3333-444455556666"}`),
	})

	if sig.Kind != RuntimeProcessSignal {
		t.Errorf("kind = %q, want %q", sig.Kind, RuntimeProcessSignal)
	}
	if sig.JobId != "9f0c1f8e-1111-2222-3333-444455556666" {
		t.Errorf("jobId = %q", sig.JobId)
	}
	if sig.Conclusion != ConclusionFlowStopByUser {
		t.Errorf("conclusion = %q, want %q", sig.Conclusion, ConclusionFlowStopByUser)
	}
	if !sig.Conclusion.Canceled() || sig.Conclusion.Succeeded() {
		t.Errorf("%q should be Canceled and not Succeeded", sig.Conclusion)
	}
}

func TestParseSignalUnmodelledKind(t *testing.T) {
	p := &Plugin{PluginId: "abc-123"}
	sig := p.parseSignal(&nats.Msg{
		Subject: "inflow.plugin.abc-123.future.kind",
		Data:    []byte("not json"),
	})

	if sig.Kind != "future.kind" {
		t.Errorf("kind = %q, want the whole subject remainder", sig.Kind)
	}
	if string(sig.Data) != "not json" {
		t.Errorf("payload was dropped: %q", sig.Data)
	}
	if sig.JobId != "" || sig.Conclusion != "" {
		t.Errorf("unparseable body should leave typed fields empty, got %q/%q", sig.JobId, sig.Conclusion)
	}
}

func TestSignalSubjectIsWildcard(t *testing.T) {
	p := &Plugin{PluginId: "abc-123"}
	if got, want := p.makeSignalSubject(), "inflow.plugin.abc-123.>"; got != want {
		t.Errorf("signal subject = %q, want %q", got, want)
	}
}
