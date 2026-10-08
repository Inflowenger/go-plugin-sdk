package sdkv1

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// The signal port is a broadcast: every process of a plugin hears every one of
// that plugin's signals, for every flow and every replica. So the property that
// matters most is the negative one — a signal stops the job it names and
// nothing else. The other is that the registry empties itself: a cancelable job
// leaves it however it ends, stopped or not.

// tracked reports whether a job is filed under jobId. Test-only: nothing in a
// plugin has a use for asking.
func (c *jobCancels) tracked(jobId string) bool {
	_, ok := c.inflight.Load(jobId)
	return ok
}

// fakeRuntime stands in for a plugin: the runtime end of a job's command
// subjects, and the registry its cancelable jobs are filed with.
type fakeRuntime struct {
	sent    []string // subjects, in order
	err     error    // answer every command with this, when set
	onSend  func()
	cancels jobCancels
}

func (f *fakeRuntime) Send(subject string, _ []byte) (*nats.Msg, error) {
	if f.onSend != nil {
		f.onSend()
	}
	f.sent = append(f.sent, subject)
	if f.err != nil {
		return nil, f.err
	}
	return &nats.Msg{Data: []byte(`{"msg":"ack"}`)}, nil
}

func (f *fakeRuntime) GetPluginId() string      { return "plug-1" }
func (f *fakeRuntime) jobRegistry() *jobCancels { return &f.cancels }

func procSignal(jobId string, c Conclusion) Signal {
	return Signal{Kind: RuntimeProcessSignal, JobId: jobId, Conclusion: c}
}

func alive(ctx context.Context) bool { return ctx.Err() == nil }

// running starts a cancelable handler the way the SDK dispatches one — on its
// own goroutine — that blocks until its context ends. It returns the context
// once the job is filed, and a channel closed when the handler has returned.
func running(t *testing.T, job Job) (context.Context, <-chan struct{}) {
	t.Helper()
	started := make(chan context.Context)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		NewCancelableJobHandler(func(ctx context.Context, _ Job) {
			started <- ctx
			<-ctx.Done()
		})(job)
	}()
	select {
	case ctx := <-started:
		return ctx, returned
	case <-time.After(2 * time.Second):
		t.Fatal("the handler never started")
		return nil, nil
	}
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// ---- stops ------------------------------------------------------------------

func TestCancelableHandlerStoppedBySignal(t *testing.T) {
	rt := &fakeRuntime{}
	ctx, returned := running(t, Job{plugin: rt, JobId: "a"})

	rt.cancels.handleSignal(procSignal("a", ConclusionFlowStopByUser))
	waitFor(t, returned, "the stopped handler to return")

	if ctx.Err() != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", ctx.Err())
	}
	if rt.cancels.tracked("a") {
		t.Error("a stopped job was left filed")
	}
	if len(rt.sent) != 0 {
		t.Errorf("the stop sent commands: %v", rt.sent)
	}
}

// Two flows running over the same plugin, and a signal from a third this
// process never accepted (another flow's job, or another replica's).
func TestCancelableHandlersAreIsolated(t *testing.T) {
	rt := &fakeRuntime{}
	flowA, returnedA := running(t, Job{plugin: rt, JobId: "flow-a-job"})
	flowB, returnedB := running(t, Job{plugin: rt, JobId: "flow-b-job"})

	rt.cancels.handleSignal(procSignal("not-mine", ConclusionFlowStopByUser))
	if !alive(flowA) || !alive(flowB) {
		t.Fatal("a signal for a job this process does not hold stopped one it does")
	}

	rt.cancels.handleSignal(procSignal("flow-b-job", ConclusionTimeout))
	waitFor(t, returnedB, "flow B's handler to return")
	if !alive(flowA) || !rt.cancels.tracked("flow-a-job") {
		t.Error("stopping flow B touched flow A's job")
	}

	rt.cancels.handleSignal(procSignal("flow-a-job", ConclusionCommandStop))
	waitFor(t, returnedA, "flow A's handler to return")
}

func TestSignalOfOtherKindIgnored(t *testing.T) {
	var c jobCancels
	job := Job{JobId: "j"}
	ctx, release := c.track(&job)
	defer release()

	c.handleSignal(Signal{Kind: "future.kind", JobId: "j", Conclusion: ConclusionFlowStopByUser})
	if !alive(ctx) || !c.tracked("j") {
		t.Error("an unmodelled signal kind acted on the job")
	}
}

// ---- endings that are not stops ----------------------------------------------

// The ordinary ending: no signal, the handler reports its result. Every
// terminal command releases the job on its own, after it is sent — checked
// from inside the handler, so it is the command doing it and not the wrapper's
// return.
func TestTerminalCommandReleasesJob(t *testing.T) {
	for name, finish := range map[string]func(*Job){
		"Done":              func(j *Job) { j.Done(map[string]any{"ok": true}) },
		"DoneWithError":     func(j *Job) { j.DoneWithError("boom") },
		"DoneWithErrorData": func(j *Job) { j.DoneWithErrorData("boom", map[string]any{"k": 1}) },
		"DoneWithErrorCode": func(j *Job) { j.DoneWithErrorCode(7, "boom", nil) },
		"Progress100":       func(j *Job) { j.Progress(100, Frame{}) },
	} {
		t.Run(name, func(t *testing.T) {
			rt := &fakeRuntime{}
			NewCancelableJobHandler(func(ctx context.Context, job Job) {
				var aliveAtSend bool
				rt.onSend = func() { aliveAtSend = alive(ctx) }
				finish(&job)

				if len(rt.sent) != 1 || !strings.HasSuffix(rt.sent[0], ".progress") {
					t.Fatalf("sent %v, want one progress command", rt.sent)
				}
				if !aliveAtSend {
					t.Error("the context was released before the terminal command went out")
				}
				if rt.cancels.tracked("j") || alive(ctx) {
					t.Error("the job outlived its terminal command")
				}
			})(Job{plugin: rt, JobId: "j"})
		})
	}
}

// Helpers take the job by value (the builtin LLM node's Exception does), so a
// copy handed on must release just the same.
func TestTerminalCommandOnCopyReleases(t *testing.T) {
	rt := &fakeRuntime{}
	NewCancelableJobHandler(func(ctx context.Context, job Job) {
		func(copied Job) { copied.DoneWithError("from a helper") }(job)
		if rt.cancels.tracked("j") || alive(ctx) {
			t.Error("Done on a copy of the job did not release it")
		}
	})(Job{plugin: rt, JobId: "j"})
}

// The handler declared the job over; that it could not be told is no reason to
// keep the entry.
func TestTerminalCommandReleasesEvenWhenSendFails(t *testing.T) {
	rt := &fakeRuntime{err: errors.New("no responders")}
	NewCancelableJobHandler(func(ctx context.Context, job Job) {
		job.Done(nil)
		if rt.cancels.tracked("j") || alive(ctx) {
			t.Error("a failed terminal send left the job filed")
		}
	})(Job{plugin: rt, JobId: "j"})
}

func TestNonTerminalCommandsKeepJob(t *testing.T) {
	rt := &fakeRuntime{}
	NewCancelableJobHandler(func(ctx context.Context, job Job) {
		job.Progress(50, Frame{Title: "half"})
		job.Progress(99, Frame{Title: "almost"})
		job.CmdNextFilter([]string{"_exception"}) // routes; does not end the process
		job.CmdGetCurrentScope()
		job.CmdSetOnPath("a.b", map[string]any{"x": 1})

		if len(rt.sent) != 5 {
			t.Fatalf("sent %d commands, want 5", len(rt.sent))
		}
		if !rt.cancels.tracked("j") || !alive(ctx) {
			t.Error("a non-terminal command released the job")
		}
	})(Job{plugin: rt, JobId: "j"})
}

// A path that forgot to Done, or returned on its own: the wrapper's return
// still releases.
func TestHandlerReturnReleasesJob(t *testing.T) {
	rt := &fakeRuntime{}
	var ctx context.Context
	NewCancelableJobHandler(func(c context.Context, _ Job) { ctx = c })(Job{plugin: rt, JobId: "j"})

	if rt.cancels.tracked("j") || alive(ctx) {
		t.Error("returning without a Done left the job filed")
	}
}

// A panic unwinds through the wrapper — WithJobHandler recovers it further up —
// and must not leave the job behind on the way.
func TestPanicReleasesJob(t *testing.T) {
	rt := &fakeRuntime{}
	var ctx context.Context
	func() {
		defer func() { _ = recover() }()
		NewCancelableJobHandler(func(c context.Context, _ Job) {
			ctx = c
			panic("handler bug")
		})(Job{plugin: rt, JobId: "j"})
	}()

	if rt.cancels.tracked("j") || alive(ctx) {
		t.Error("a panicking handler left the job filed")
	}
}

// A job the runtime concluded without a stop — the handler hung, or the runtime
// failed on its own side — is unfiled by the signal, never cancelled by it.
func TestOtherConclusionsUnfileWithoutCancelling(t *testing.T) {
	for _, conc := range []Conclusion{
		ConclusionDone, ConclusionNext, ConclusionFailure, ConclusionInternalError,
		ConclusionBadRequest, ConclusionExceededRequestAnomaly, ConclusionUnknownCause,
	} {
		var c jobCancels
		job := Job{JobId: "j"}
		ctx, release := c.track(&job)

		c.handleSignal(procSignal("j", conc))
		if !alive(ctx) {
			t.Errorf("conclusion %q cancelled the job", conc)
		}
		if c.tracked("j") {
			t.Errorf("conclusion %q left the job filed", conc)
		}
		release()
	}
}

// A second track for the same jobId replaces the first. The first one's
// release must not unfile it, and the job's terminal command must free both.
func TestTrackedTwice(t *testing.T) {
	rt := &fakeRuntime{}
	job := Job{plugin: rt, JobId: "j"}
	ctxOld, releaseOld := rt.cancels.track(&job)
	ctxNew, _ := rt.cancels.track(&job)

	releaseOld()
	if !rt.cancels.tracked("j") || !alive(ctxNew) {
		t.Fatal("the stale release unfiled the newer track")
	}

	job.Done(nil)
	if rt.cancels.tracked("j") || alive(ctxNew) || alive(ctxOld) {
		t.Error("the terminal command did not free both contexts")
	}
}

func TestEmptyJobId(t *testing.T) {
	var c jobCancels
	if c.cancel("") {
		t.Error("cancel(\"\") reported a job")
	}
	c.handleSignal(procSignal("", ConclusionFlowStopByUser)) // must not panic
}

// ---- the plain handler is untouched ---------------------------------------------

func TestPlainHandlerJobIsNotFiled(t *testing.T) {
	rt := &fakeRuntime{}
	job := Job{plugin: rt, JobId: "j"}
	job.Done(nil) // no release stamped: must not panic

	if rt.cancels.tracked("j") {
		t.Error("a plain job was filed")
	}
	if len(rt.sent) != 1 {
		t.Errorf("sent %v, want the one Done", rt.sent)
	}
}

// A job with no plugin behind it still gets a context, released on return.
func TestCancelableHandlerWithoutPlugin(t *testing.T) {
	var ctx context.Context
	NewCancelableJobHandler(func(c context.Context, _ Job) {
		if c == nil || !alive(c) {
			t.Fatal("no live context for a job without a plugin")
		}
		ctx = c
	})(Job{JobId: "j"})
	if alive(ctx) {
		t.Error("the context outlived the handler")
	}
}

// ---- through the plugin, from the wire -------------------------------------------

// End to end through a real Plugin, from the bytes fractal-core publishes
// (engine/prim_nodes/plugin.go: `{"conclusion":"%s","jobId":"%s"}`): parsed,
// dispatched, routed to the job — before the plugin's own OnSignal handler,
// which must still be called, and must find the job already stopping.
func TestPluginRoutesStopToJobThenOnSignal(t *testing.T) {
	const jobId = "9f0c1f8e-1111-2222-3333-444455556666"
	p := &Plugin{PluginId: "plug-1"}

	var ctx context.Context
	observed := make(chan error, 1)
	p.OnSignal(func(sig Signal) {
		if sig.JobId == jobId {
			observed <- ctx.Err()
		}
	})

	ctx, returned := running(t, Job{plugin: p, JobId: jobId})
	p.dispatchSignal(p.parseSignal(&nats.Msg{
		Subject: "inflow.plugin.plug-1.proc",
		Data:    []byte(`{"conclusion":"flow_stop_by_user","jobId":"` + jobId + `"}`),
	}))
	waitFor(t, returned, "the stopped handler to return")

	select {
	case err := <-observed:
		if err != context.Canceled {
			t.Errorf("OnSignal saw the job's ctx err = %v, want it already cancelled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the plugin's own OnSignal handler was not called")
	}
	if p.cancels.tracked(jobId) {
		t.Error("the stopped job was left filed on the plugin")
	}
}

// Without OnSignal, the SDK alone still routes the stop.
func TestPluginRoutesStopWithoutOnSignal(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	ctx, returned := running(t, Job{plugin: p, JobId: "j"})

	p.dispatchSignal(procSignal("j", ConclusionTimeout))
	waitFor(t, returned, "the stopped handler to return")
	if alive(ctx) {
		t.Error("a stop reached no job without an OnSignal handler")
	}
}

// The documented wiring, compile-checked.
func ExampleNewCancelableJobHandler() {
	p := &Plugin{}
	p.AddAction(Action{
		Method: "long.export",
		RequestHandler: NewCancelableJobHandler(func(ctx context.Context, job Job) {
			<-ctx.Done() // ... work that honours ctx ...
			if ctx.Err() != nil {
				return // stopped by the runtime: do not Done
			}
			job.Done(map[string]any{"ok": true}) // ends the process and releases the job
		}),
	})
}
