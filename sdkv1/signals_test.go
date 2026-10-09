package sdkv1

import (
	"bytes"
	"log"
	"strings"
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

// LogSignals is the only sight of the port a plugin has once it registers a
// handler of its own, so what it prints is part of what it offers: the jobId and
// the conclusion for a "proc" signal, and the raw payload for a kind whose
// typed fields would be empty.

func TestLogSignalsProc(t *testing.T) {
	line := captureLog(t, func() {
		LogSignals("ai-decision")(Signal{
			Kind:       RuntimeProcessSignal,
			Subject:    "inflow.plugin.abc-123.proc",
			JobId:      "9f0c1f8e-1111-2222-3333-444455556666",
			Conclusion: ConclusionFlowStopByUser,
		})
	})

	for _, want := range []string{
		"ai-decision: signal proc",
		"job=9f0c1f8e-1111-2222-3333-444455556666",
		"conclusion=flow_stop_by_user",
		"canceled=true",
		"succeeded=false",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line %q is missing %q", line, want)
		}
	}
}

func TestLogSignalsUnmodelledKindKeepsPayload(t *testing.T) {
	line := captureLog(t, func() {
		LogSignals("")(Signal{
			Kind:    "future.kind",
			Subject: "inflow.plugin.abc-123.future.kind",
			Data:    []byte("not json"),
		})
	})

	if !strings.Contains(line, "subject=inflow.plugin.abc-123.future.kind") || !strings.Contains(line, "data=not json") {
		t.Errorf("log line %q should carry the subject and the raw payload", line)
	}
	if strings.Contains(line, "job=") || strings.Contains(line, "conclusion=") {
		t.Errorf("log line %q reports typed fields a non-proc signal does not carry", line)
	}
}

// captureLog runs fn with the standard logger redirected, and returns what it
// wrote.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(out); log.SetFlags(flags) })
	fn()
	return buf.String()
}
