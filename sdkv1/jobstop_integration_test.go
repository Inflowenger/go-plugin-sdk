package sdkv1_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Inflowenger/go-plugin-sdk/jobstop"
	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
	"github.com/nats-io/nats.go"
)

// jobstop on a real plugin's request path: the property it exists for — a stop
// reaches the job it names, and only that job — has to hold through the SDK's
// middleware, not just in isolation.

type runtime struct {
	mu   sync.Mutex
	sent []string
}

func (r *runtime) Send(subject string, _ []byte) (*nats.Msg, error) {
	r.mu.Lock()
	r.sent = append(r.sent, subject)
	r.mu.Unlock()
	return &nats.Msg{Data: []byte(`{"msg":"ack"}`)}, nil
}
func (r *runtime) GetPluginId() string { return "plug-1" }

func stopSignal(jobId string) sdkv1.Signal {
	return sdkv1.Signal{Kind: sdkv1.RuntimeProcessSignal, JobId: jobId, Conclusion: sdkv1.ConclusionFlowStopByUser}
}

// The earliest a stop could ever arrive is after the reply. Sent by a function
// after stops.Middleware — the job filed, the jobId not yet replied — it is
// earlier still, and must already reach the job: so a real one always will.
func TestJobstopFilesBeforeTheJobIsAccepted(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	var stops jobstop.Registry
	stopNow := func(ctx context.Context, job sdkv1.Job) (context.Context, error) {
		stops.OnSignal(stopSignal(job.JobId))
		return ctx, nil
	}
	var cause error
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware:     sdkv1.Use(stops.Middleware, stopNow),
		RequestHandler: func(job sdkv1.Job) { cause = context.Cause(job.Context()) },
	}, &runtime{})
	if !errors.Is(cause, jobstop.ErrStopped) {
		t.Errorf("handler's context cause = %v, want jobstop.ErrStopped: the stop was lost", cause)
	}
}

// Two actions on one plugin: only the one using stops.Middleware stops with its
// flow; the other — a long-running job — runs to the end.
func TestJobstopIsPerAction(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	var stops jobstop.Registry
	ids := make(chan string, 2)
	idOf := func(ctx context.Context, job sdkv1.Job) (context.Context, error) {
		ids <- job.JobId
		return ctx, nil
	}

	callDone := make(chan struct{})
	go func() {
		sdkv1.RunPipeline(p, sdkv1.Action{Method: "call",
			Middleware:     sdkv1.Use(idOf, stops.Middleware),
			RequestHandler: func(job sdkv1.Job) { <-job.Context().Done() },
		}, &runtime{})
		close(callDone)
	}()
	callId := <-ids

	gate := make(chan struct{})
	var trainedToEnd atomic.Bool
	trainDone := make(chan struct{})
	go func() {
		sdkv1.RunPipeline(p, sdkv1.Action{Method: "train",
			Middleware: sdkv1.Use(idOf),
			RequestHandler: func(sdkv1.Job) {
				<-gate // ... hours of work, during which the flow is stopped ...
				trainedToEnd.Store(true)
			},
		}, &runtime{})
		close(trainDone)
	}()
	trainId := <-ids

	stops.OnSignal(stopSignal(trainId)) // not filed: does nothing
	stops.OnSignal(stopSignal(callId))
	select {
	case <-callDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the stop did not reach the job using stops.Middleware")
	}
	close(gate)
	select {
	case <-trainDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the long-running job never finished")
	}
	if !trainedToEnd.Load() {
		t.Error("a stop stopped an action without stops.Middleware")
	}
}

// Side by side: a plugin-wide trace function and the action's stops.Middleware,
// with stops.OnSignal chained with another handler on the port.
func TestJobstopComposes(t *testing.T) {
	type traceKey struct{}
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	var stops jobstop.Registry
	var audited atomic.Int32
	port := sdkv1.ChainSignals(func(sdkv1.Signal) { audited.Add(1) }, stops.OnSignal)
	p.Use(func(ctx context.Context, job sdkv1.Job) (context.Context, error) {
		return context.WithValue(ctx, traceKey{}, "span-"+job.JobId), nil
	})

	traced := make(chan [2]string, 1)
	finished := make(chan struct{})
	go func() {
		sdkv1.RunPipeline(p, sdkv1.Action{
			Middleware: sdkv1.Use(stops.Middleware),
			RequestHandler: func(job sdkv1.Job) {
				span, _ := job.Context().Value(traceKey{}).(string)
				traced <- [2]string{span, job.JobId}
				<-job.Context().Done()
			},
		}, &runtime{})
		close(finished)
	}()

	got := <-traced
	if got[0] != "span-"+got[1] {
		t.Errorf("handler saw trace %q for job %q", got[0], got[1])
	}
	port(stopSignal(got[1]))
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the chained stop did not reach the job")
	}
	if audited.Load() != 1 {
		t.Errorf("the chained audit handler saw %d signals, want 1", audited.Load())
	}
}

// The SDK ends a job's context when its handler returns — which is what takes
// the job out of stops' registry (see jobstop's own tests for the unfiling).
func TestJobstopContextEndsWithTheHandler(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	var stops jobstop.Registry
	var jobCtx context.Context
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware:     sdkv1.Use(stops.Middleware),
		RequestHandler: func(job sdkv1.Job) { jobCtx = job.Context() },
	}, &runtime{})

	if jobCtx.Err() == nil {
		t.Error("the job's context outlived its handler, so stops would keep it filed")
	}
	if errors.Is(context.Cause(jobCtx), jobstop.ErrStopped) {
		t.Error("a job that was never stopped ended as stopped")
	}
}
