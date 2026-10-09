package sdkv1_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Inflowenger/go-plugin-sdk/jobstop"
	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// A plugin fronting a service that names the work itself — a Joern server
// answering a query registration with its own queryId — can run the job under
// that id instead of a uuid of its own: the id is bound by a middleware
// function, before the job is accepted, so the runtime is told the external id
// and both sides correlate on one name. See docs/external-job-identity.md.
//
// These exercise the whole request path (sdkv1.RunPipeline), reusing the
// runtime recorder and stopSignal from jobstop_integration_test.go.

// joern stands in for the external service: it names each registration, and
// records the ones that were aborted.
type joern struct {
	failed  error
	aborted chan string
}

func newJoern() *joern { return &joern{aborted: make(chan string, 4)} }

// register answers with the service's own id. It is 10+ characters on purpose:
// fractal-core refuses an init reply whose jobId is shorter than that.
func (j *joern) register(query string) (string, error) {
	if j.failed != nil {
		return "", j.failed
	}
	return "q-" + query + "-8f21c47b", nil
}

func (j *joern) abort(queryId string) { j.aborted <- queryId }

// register is the middleware function at the heart of the pattern: it registers
// the work upstream and binds what came back as the job's id.
func (j *joern) middleware(query string) sdkv1.MiddlewareFunc {
	return func(ctx context.Context, _ sdkv1.Job) (context.Context, error) {
		queryId, err := j.register(query)
		if err != nil {
			return nil, err // the request is rejected: nothing upstream to undo
		}
		return sdkv1.WithJobIDContext(ctx, queryId), nil
	}
}

// The id the service gave is the id the job runs under: the handler's, the
// context's, and the one every command for this job is addressed to.
func TestExternalIdBecomesTheJobId(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	service := newJoern()
	rt := &runtime{}

	var inHandler, inContext string
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware: sdkv1.Use(service.middleware("cpg")),
		RequestHandler: func(job sdkv1.Job) {
			inHandler, inContext = job.JobId, sdkv1.JobIDFromContext(job.Context())
			job.Done(map[string]any{"ok": true})
		},
	}, rt)

	if inHandler != "q-cpg-8f21c47b" || inContext != "q-cpg-8f21c47b" {
		t.Fatalf("handler saw jobId %q (context %q), want the service's id %q", inHandler, inContext, "q-cpg-8f21c47b")
	}
	want := "inflow.cpu.plug-1.q-cpg-8f21c47b.progress"
	if got := rt.sent; len(got) != 1 || got[0] != want {
		t.Errorf("commands went to %v, want [%s]: the runtime must address the job by the shared id", got, want)
	}
}

// Stopping the flow has to reach the service that is doing the work. With the
// namer before stops.Middleware, the registry is keyed by the shared id — the
// one the stop signal carries — and the handler's cleanup aborts the query
// upstream.
func TestStopNotifiesTheExternalService(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	service := newJoern()
	var stops jobstop.Registry

	ids := make(chan string, 1)
	finished := make(chan struct{})
	var cause error
	go func() {
		sdkv1.RunPipeline(p, sdkv1.Action{
			Middleware: sdkv1.Use(service.middleware("cpg"), stops.Middleware),
			RequestHandler: func(job sdkv1.Job) {
				ctx := job.Context()
				context.AfterFunc(ctx, func() {
					if errors.Is(context.Cause(ctx), jobstop.ErrStopped) {
						service.abort(job.JobId) // compensate: the query dies with the flow
					}
				})
				ids <- job.JobId
				<-ctx.Done()
				cause = context.Cause(ctx)
			},
		}, &runtime{})
		close(finished)
	}()

	queryId := <-ids
	stops.OnSignal(stopSignal(queryId)) // the runtime stops the flow, naming q-cpg

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the stop never reached the job filed under the service's id")
	}
	if !errors.Is(cause, jobstop.ErrStopped) {
		t.Errorf("context cause = %v, want jobstop.ErrStopped", cause)
	}
	select {
	case aborted := <-service.aborted:
		if aborted != queryId {
			t.Errorf("aborted %q upstream, want the job's own id %q", aborted, queryId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the service was never told to abort the query")
	}
}

// Order is the whole correctness argument: filed before the job is named, the
// registry holds a key nothing will ever look up, and a real stop — which can
// only carry the accepted id — is lost. The handler runs on to the end.
func TestStopsBeforeTheNamerMissesTheStop(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	service := newJoern()
	var stops jobstop.Registry

	ids := make(chan string, 1)
	gate := make(chan struct{})
	finished := make(chan struct{})
	var stopped bool
	go func() {
		sdkv1.RunPipeline(p, sdkv1.Action{
			Middleware: sdkv1.Use(stops.Middleware, service.middleware("cpg")), // wrong way round
			RequestHandler: func(job sdkv1.Job) {
				ids <- job.JobId
				<-gate
				stopped = job.Context().Err() != nil
			},
		}, &runtime{})
		close(finished)
	}()

	queryId := <-ids
	stops.OnSignal(stopSignal(queryId)) // filed under a uuid: no match
	close(gate)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never returned")
	}
	if stopped {
		t.Error("the stop reached the job: this order would be safe after all")
	}
}

// A service that cannot take the work rejects the request: no jobId is replied,
// the handler never runs, and nothing was registered that now needs undoing.
func TestRegistrationFailureRejectsTheRequest(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	service := newJoern()
	service.failed = errors.New("joern: connection refused")
	rt := &runtime{}

	called := false
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware:     sdkv1.Use(service.middleware("cpg")),
		RequestHandler: func(sdkv1.Job) { called = true },
	}, rt)

	if called {
		t.Error("the job was accepted although the service never took the query")
	}
	if len(rt.sent) != 0 {
		t.Errorf("a rejected request sent %v, want no commands", rt.sent)
	}
}

// Work the plugin did take on must be undone when a function after it rejects
// the request: the job's context ends on rejection too, so the compensation
// hangs off it exactly as a stop's does.
func TestRejectionAfterRegistrationCompensates(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	service := newJoern()

	registerWithUndo := func(ctx context.Context, job sdkv1.Job) (context.Context, error) {
		ctx, err := service.middleware("cpg")(ctx, job)
		if err != nil {
			return nil, err
		}
		queryId := sdkv1.JobIDFromContext(ctx)
		context.AfterFunc(ctx, func() { service.abort(queryId) })
		return ctx, nil
	}
	refuse := func(ctx context.Context, _ sdkv1.Job) (context.Context, error) {
		return nil, errors.New("over quota")
	}

	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware:     sdkv1.Use(registerWithUndo, refuse),
		RequestHandler: func(sdkv1.Job) { t.Error("a rejected request ran its handler") },
	}, &runtime{})

	select {
	case aborted := <-service.aborted:
		if aborted != "q-cpg-8f21c47b" {
			t.Errorf("aborted %q, want q-cpg-8f21c47b", aborted)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the registered query was orphaned: no compensation ran")
	}
}

// fractal-core refuses an init reply whose jobId is shorter than 10 characters
// (engine/prim_nodes/plugin.go: `init failed. invalid job ID`), so a namer that
// adopts a service's id validates it and rejects the request itself rather than
// accepting a job the runtime will throw away.
func TestShortExternalIdIsRefusedBeforeAccept(t *testing.T) {
	p, _ := sdkv1.NewPlugin(sdkv1.WithPluginId("plug-1"))
	namer := func(ctx context.Context, _ sdkv1.Job) (context.Context, error) {
		id := "q-7" // what a terse service answers
		if len(id) < 10 {
			return nil, errors.New("jobId from joern is shorter than 10 characters")
		}
		return sdkv1.WithJobIDContext(ctx, id), nil
	}
	rt := &runtime{}
	sdkv1.RunPipeline(p, sdkv1.Action{
		Middleware:     sdkv1.Use(namer),
		RequestHandler: func(sdkv1.Job) { t.Error("a job the runtime would reject was accepted") },
	}, rt)
	if len(rt.sent) != 0 {
		t.Errorf("a rejected request sent %v, want no commands", rt.sent)
	}
}
