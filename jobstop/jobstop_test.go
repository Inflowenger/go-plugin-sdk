package jobstop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// The registry in isolation. Through a real plugin's request path, see
// sdkv1/jobstop_integration_test.go.

func (r *Registry) filed(jobId string) bool {
	_, ok := r.jobs.Load(jobId)
	return ok
}

func signal(jobId string, c sdkv1.Conclusion) sdkv1.Signal {
	return sdkv1.Signal{Kind: sdkv1.RuntimeProcessSignal, JobId: jobId, Conclusion: c}
}

// file runs the middleware function for jobId, on a context the test ends
// (as the SDK ends a job's when its handler returns).
func file(t *testing.T, r *Registry, jobId string) (context.Context, context.CancelFunc) {
	t.Helper()
	parent, end := context.WithCancel(context.Background())
	ctx, err := r.Middleware(parent, sdkv1.Job{JobId: jobId})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, end
}

// unfiledSoon allows for the unfiling that follows a context's end, which
// context.AfterFunc runs on its own goroutine.
func unfiledSoon(r *Registry, jobId string) bool {
	deadline := time.Now().Add(time.Second)
	for r.filed(jobId) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return !r.filed(jobId)
}

func TestStopCancelsTheJob(t *testing.T) {
	var r Registry
	ctx, end := file(t, &r, "a")
	defer end()
	if !r.filed("a") {
		t.Fatal("Middleware did not file the job")
	}
	r.OnSignal(signal("a", sdkv1.ConclusionFlowStopByUser))
	if !errors.Is(context.Cause(ctx), ErrStopped) {
		t.Errorf("after a stop: cause = %v, want ErrStopped", context.Cause(ctx))
	}
	if r.filed("a") {
		t.Error("a stopped job was left filed")
	}
}

// Two flows running over the same plugin, and a signal from a third this
// process never accepted (another flow's job, or another replica's).
func TestStopsAreIsolatedByJobId(t *testing.T) {
	var r Registry
	a, endA := file(t, &r, "flow-a-job")
	defer endA()
	b, endB := file(t, &r, "flow-b-job")
	defer endB()

	r.OnSignal(signal("not-mine", sdkv1.ConclusionFlowStopByUser))
	if a.Err() != nil || b.Err() != nil {
		t.Fatal("a signal for a job this registry does not hold stopped one it does")
	}
	r.OnSignal(signal("flow-b-job", sdkv1.ConclusionTimeout))
	if a.Err() != nil || !r.filed("flow-a-job") {
		t.Error("stopping flow B touched flow A's job")
	}
	if b.Err() == nil {
		t.Error("stopping flow B left its own job running")
	}
}

// A job that ended any other way is unfiled by its signal, never cancelled.
func TestOtherConclusionsUnfileWithoutCancelling(t *testing.T) {
	for _, c := range []sdkv1.Conclusion{
		sdkv1.ConclusionDone, sdkv1.ConclusionNext, sdkv1.ConclusionFailure,
		sdkv1.ConclusionInternalError, sdkv1.ConclusionBadRequest, sdkv1.ConclusionUnknownCause,
	} {
		var r Registry
		ctx, end := file(t, &r, "j")
		r.OnSignal(signal("j", c))
		if ctx.Err() != nil {
			t.Errorf("conclusion %q cancelled the job", c)
		}
		if r.filed("j") {
			t.Errorf("conclusion %q left the job filed", c)
		}
		end()
	}
}

func TestSignalsItDoesNotModelAreIgnored(t *testing.T) {
	var r Registry
	ctx, end := file(t, &r, "j")
	defer end()
	r.OnSignal(sdkv1.Signal{Kind: "future.kind", JobId: "j", Conclusion: sdkv1.ConclusionFlowStopByUser})
	r.OnSignal(signal("", sdkv1.ConclusionFlowStopByUser))
	if ctx.Err() != nil || !r.filed("j") {
		t.Error("a signal of another kind, or without a jobId, acted on the job")
	}
}

// However the job's context ends — here its parent, as the SDK ends it when the
// handler returns or the request is rejected — the job leaves the registry.
func TestJobLeavesRegistryWhenItsContextEnds(t *testing.T) {
	var r Registry
	_, end := file(t, &r, "j")
	end()
	if !unfiledSoon(&r, "j") {
		t.Error("the job outlived its context in the registry")
	}
}

func TestCancelAll(t *testing.T) {
	var r Registry
	a, endA := file(t, &r, "a")
	defer endA()
	b, endB := file(t, &r, "b")
	defer endB()
	r.CancelAll()
	for name, ctx := range map[string]context.Context{"a": a, "b": b} {
		if !errors.Is(context.Cause(ctx), ErrShutdown) {
			t.Errorf("job %s: cause = %v, want ErrShutdown", name, context.Cause(ctx))
		}
		if r.filed(name) {
			t.Errorf("job %s was left filed", name)
		}
	}
}

// The documented wiring, compile-checked.
func ExampleRegistry() {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("example"))
	var stops Registry

	p.OnSignal(stops.OnSignal)
	p.AddAction(sdkv1.Action{
		Method:     "run",
		Middleware: sdkv1.Use(stops.Middleware),
		RequestHandler: func(job sdkv1.Job) {
			ctx := job.Context()
			<-ctx.Done() // ... work that honours ctx ...
			if ctx.Err() != nil {
				return // stopped by the runtime: do not Done
			}
			job.Done(map[string]any{"ok": true})
		},
	})
}
