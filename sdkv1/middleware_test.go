package sdkv1

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Middleware is a list of functions run in order before the job is accepted:
// JobID, the plugin's (p.Use), the action's own (Action.Middleware), then the
// handler. These run requests through runPipeline itself, with the job's
// commands going to a recorder instead of a server.

// recorder is the runtime end of a job's command subjects.
type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) Send(subject string, _ []byte) (*nats.Msg, error) {
	r.mu.Lock()
	r.sent = append(r.sent, subject)
	r.mu.Unlock()
	return &nats.Msg{Data: []byte(`{"msg":"ack"}`)}, nil
}

func (r *recorder) GetPluginId() string { return "plug-1" }

func (r *recorder) subjects() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

// run runs one request of action through p's middleware and handler — it
// returns once the handler has.
func run(p *Plugin, action Action) *recorder {
	rec := &recorder{}
	if action.Method == "" {
		action.Method = "run"
	}
	p.runPipeline(action, Request{Plugin: rec}, &nats.Msg{})
	return rec
}

// record is a middleware function that notes, under name, that it ran.
func record(name string, log *[]string) MiddlewareFunc {
	return func(ctx context.Context, _ Job) (context.Context, error) {
		*log = append(*log, name)
		return ctx, nil
	}
}

// ---- order ---------------------------------------------------------------------

func TestMiddlewareRunsInOrder(t *testing.T) {
	var log []string
	p := &Plugin{PluginId: "plug-1"}
	p.Use(record("plugin", &log))
	run(p, Action{
		Middleware:     Use(record("a1", &log), record("a2", &log)),
		RequestHandler: func(Job) { log = append(log, "handler") },
	})
	if got := strings.Join(log, " "); got != "plugin a1 a2 handler" {
		t.Errorf("order = %q, want %q", got, "plugin a1 a2 handler")
	}
}

func TestUseAndAppend(t *testing.T) {
	var log []string
	base := Use(record("a", &log), nil)
	if len(base) != 1 {
		t.Fatalf("Use kept %d functions, want 1 (nil skipped)", len(base))
	}
	more := base.Append(record("b", &log))
	other := base.Append(record("c", &log))
	if len(base) != 1 || len(more) != 2 || len(other) != 2 {
		t.Fatalf("Append changed the list it was called on: %d %d %d", len(base), len(more), len(other))
	}
	for _, fn := range more {
		fn(context.Background(), Job{})
	}
	if got := strings.Join(log, " "); got != "a b" {
		t.Errorf("Append aliased another list: ran %q, want %q", got, "a b")
	}
}

// ---- the job's name --------------------------------------------------------------

// JobID runs first: every function after it sees the jobId, on the job and on
// the context, and the handler gets the same one.
func TestJobIDNamesTheJobFirst(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	var seen, inCtx, handled string
	run(p, Action{
		Middleware: Use(func(ctx context.Context, job Job) (context.Context, error) {
			seen, inCtx = job.JobId, JobIDFromContext(ctx)
			return ctx, nil
		}),
		RequestHandler: func(job Job) { handled = JobIDFromContext(job.Context()) + "|" + job.JobId },
	})
	if seen == "" || inCtx != seen {
		t.Fatalf("middleware saw jobId %q, context %q: want one assigned before it", seen, inCtx)
	}
	if handled != seen+"|"+seen {
		t.Errorf("handler saw %q, want the same jobId %q", handled, seen)
	}
}

func TestJobIDIsFreshPerRequest(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	ids := map[string]bool{}
	for i := 0; i < 50; i++ {
		run(p, Action{RequestHandler: func(job Job) { ids[job.JobId] = true }})
	}
	if len(ids) != 50 {
		t.Errorf("50 requests got %d distinct jobIds", len(ids))
	}
}

func TestWithJobIDReplacesTheNamer(t *testing.T) {
	p, _ := NewPlugin(WithPluginId("plug-1"), WithJobID(func(ctx context.Context, _ Job) (context.Context, error) {
		return WithJobIDContext(ctx, "custom-1"), nil
	}))
	var got string
	run(p, Action{RequestHandler: func(job Job) { got = job.JobId + "|" + JobIDFromContext(job.Context()) }})
	if got != "custom-1|custom-1" {
		t.Errorf("handler saw %q, want the replacement namer's id", got)
	}
}

// A namer that names nothing must not let a job be accepted under "".
func TestNamelessJobIsRejected(t *testing.T) {
	p, _ := NewPlugin(WithPluginId("plug-1"), WithJobID(func(ctx context.Context, _ Job) (context.Context, error) {
		return ctx, nil
	}))
	called := false
	run(p, Action{RequestHandler: func(Job) { called = true }})
	if called {
		t.Error("a job without a jobId was accepted")
	}
}

// ---- the job's context --------------------------------------------------------

// What a function returns is what the next one, and the handler, get; and, like
// an http.Request's, the context ends when the handler returns.
func TestContextFlowsToTheHandlerAndEndsWithIt(t *testing.T) {
	type key struct{}
	p := &Plugin{PluginId: "plug-1"}
	var ctx context.Context
	run(p, Action{
		Middleware: Use(
			func(c context.Context, _ Job) (context.Context, error) {
				return context.WithValue(c, key{}, "bound"), nil
			},
			func(context.Context, Job) (context.Context, error) {
				return nil, nil // keeps the context it was given
			},
		),
		RequestHandler: func(job Job) {
			if job.Context().Value(key{}) != "bound" {
				t.Error("the handler did not get the middleware's context")
			}
			ctx = job.Context()
		},
	})
	if ctx.Err() == nil {
		t.Error("the job's context outlived its handler")
	}
}

func TestJobContextAndWithContext(t *testing.T) {
	var job Job
	if ctx := job.Context(); ctx == nil || ctx.Done() != nil {
		t.Errorf("zero job's Context() = %v, want Background", ctx)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if with := job.WithContext(ctx); with.Context() != ctx || job.Context() == ctx {
		t.Error("WithContext must return a copy carrying ctx, leaving the original")
	}
}

// ---- rejection and failure ---------------------------------------------------------

// An error rejects the request: no later function and no handler run, nothing
// is sent on the job's subjects, and the context is ended.
func TestMiddlewareErrorRejects(t *testing.T) {
	var log []string
	p := &Plugin{PluginId: "plug-1"}
	var given context.Context
	called := false
	rec := run(p, Action{
		Middleware: Use(
			func(ctx context.Context, _ Job) (context.Context, error) {
				given = ctx
				return nil, errors.New("already training this model")
			},
			record("after", &log),
		),
		RequestHandler: func(Job) { called = true },
	})
	if called || len(log) != 0 {
		t.Errorf("a rejected request went on: handler=%v, later functions=%v", called, log)
	}
	if len(rec.subjects()) != 0 {
		t.Errorf("a rejected request sent %v", rec.subjects())
	}
	if given.Err() == nil {
		t.Error("a rejected request's context was not ended")
	}
}

func TestMiddlewarePanicRejects(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	called := false
	rec := run(p, Action{
		Middleware:     Use(func(context.Context, Job) (context.Context, error) { panic("middleware bug") }),
		RequestHandler: func(Job) { called = true },
	})
	if called || len(rec.subjects()) != 0 {
		t.Error("a request whose middleware panicked went on")
	}
}

// After the job is accepted the runtime is waiting on it: a handler's panic is
// reported as the job's failure.
func TestHandlerPanicIsReported(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	rec := run(p, Action{RequestHandler: func(Job) { panic("handler bug") }})
	sent := rec.subjects()
	if len(sent) != 1 || !strings.HasSuffix(sent[0], ".progress") {
		t.Errorf("sent %v, want the panic reported as one terminal command", sent)
	}
}

// ---- dispatch ----------------------------------------------------------------------

// dispatchAction hands the request to a goroutine of its own: a slow middleware
// function holds up only its own request, never the subscription.
func TestDispatchDoesNotWaitForMiddleware(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	gate := make(chan struct{})
	var reached atomic.Bool
	returned := make(chan struct{})
	go func() {
		p.dispatchAction(Action{Method: "run",
			Middleware: Use(func(ctx context.Context, _ Job) (context.Context, error) {
				<-gate
				reached.Store(true)
				return ctx, nil
			}),
			RequestHandler: func(Job) {},
		}, &nats.Msg{})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch waited on a blocked middleware function")
	}
	if reached.Load() {
		t.Error("test setup: the middleware ran past its gate")
	}
	close(gate)
}

// ---- signals -----------------------------------------------------------------------

func TestChainSignals(t *testing.T) {
	var got []string
	handler := func(name string) SignalHandler { return func(Signal) { got = append(got, name) } }
	panicking := func(Signal) { panic("handler bug") }

	ChainSignals(handler("a"), panicking, nil, handler("b"))(Signal{Subject: "s"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("handlers saw the signal as %v, want [a b] despite the panic between them", got)
	}
}

// Without an OnSignal handler the port is not subscribed, and Start says so —
// naming where middleware is added, the likely victims of a missing handler.
func TestSignalPortNote(t *testing.T) {
	p := &Plugin{PluginId: "plug-1"}
	p.AddAction(Action{Method: "plain"})
	note := p.signalPortNote()
	if !strings.Contains(note, "inflow.plugin.plug-1.>") || strings.Contains(note, "middleware") {
		t.Errorf("note without middleware = %q", note)
	}

	fn := func(ctx context.Context, _ Job) (context.Context, error) { return ctx, nil }
	p.AddAction(Action{Method: "run", Middleware: Use(fn)}, Action{Method: "call", Middleware: Use(fn)})
	if note := p.signalPortNote(); !strings.HasSuffix(note, "actions with middleware: run, call") {
		t.Errorf("note with action middleware = %q", note)
	}
	p.Use(fn)
	if note := p.signalPortNote(); !strings.Contains(note, "plugin middleware is set") {
		t.Errorf("note with plugin middleware = %q", note)
	}
}
