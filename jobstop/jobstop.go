// Package jobstop stops a plugin's job when the runtime stops its flow.
//
// It is built from the SDK's public pieces only, and the plugin composes it in
// itself — a middleware function on each action that should stop with its
// flow, and a signal handler on the plugin's signal port:
//
//	var stops jobstop.Registry // one per plugin; the zero value is ready
//
//	p.OnSignal(stops.OnSignal) // before p.Start()
//	p.AddAction(sdkv1.Action{
//		Method:         "run",
//		Middleware:     sdkv1.Use(stops.Middleware),
//		RequestHandler: runHandler,
//	})
//
//	func runHandler(job sdkv1.Job) {
//		ctx := job.Context() // cancelled when the flow is stopped
//		// ... pass ctx to whatever must die with the process ...
//		if ctx.Err() != nil {
//			return // the runtime has stopped listening: do not Done
//		}
//		job.Done(result)
//	}
//
// Each piece sits beside others the same way: sdkv1.Use(trace,
// stops.Middleware) on an action (or p.Use for every action),
// sdkv1.ChainSignals(stops.OnSignal, audit) on the port.
//
// It is opt-in per action, and that is the point: a job the plugin accepted
// keeps running after its flow stops — a later run of the node may build on its
// progress, the runtime handing the previous jobId back in `_registry` — so only
// the actions whose work must not outlive the process take stops.Middleware: a
// paid call nobody will read, a stream to close, a lock to release.
//
// # Isolation
//
// Stops are matched by jobId, and only by jobId. The runtime publishes process
// signals on ONE subject per plugin, `inflow.plugin.<PLUGIN_ID>.proc`, so every
// process of a plugin receives every one of that plugin's signals: those of
// jobs in other flows running at the same time, and, when the plugin is
// deployed as several replicas, those of jobs this process never accepted. There
// is no flowId on the wire. A signal for a job this registry does not hold is the
// ordinary case, and does nothing.
//
// Because middleware runs before the runtime is told the jobId, a job is always
// filed before any stop for it can arrive.
package jobstop

import (
	"context"
	"errors"
	"sync"

	"github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

// The causes a job's context ends with — read them with context.Cause(ctx).
var (
	// ErrStopped: the runtime stopped the job's process — a user stop, a stop
	// command, the workflow's timeout, the node's idle window. It has concluded
	// the job and stopped listening: return without reporting.
	ErrStopped = errors.New("jobstop: the runtime stopped the job's process")
	// ErrShutdown: the plugin cancelled every job it holds (Registry.CancelAll),
	// typically because it is exiting.
	ErrShutdown = errors.New("jobstop: the plugin is shutting down")
)

// Registry holds the jobs filed by its Middleware, keyed by jobId, from before
// each is accepted until its context ends. One per plugin; the zero value is
// ready; must not be copied after first use.
type Registry struct {
	jobs sync.Map // jobId -> *entry
}

type entry struct {
	cancel context.CancelCauseFunc
}

// Middleware is an sdkv1.MiddlewareFunc: it files the job under its jobId with
// a context derived from ctx — the one the handler gets — which OnSignal
// cancels when the runtime stops the job's process. It runs before the runtime
// knows the jobId; the job leaves the registry whenever its context ends — a
// stop, CancelAll, or the SDK ending it when the handler returns or the request
// is rejected — so nothing is left behind however the job ends.
func (r *Registry) Middleware(ctx context.Context, job sdkv1.Job) (context.Context, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	e := &entry{cancel: cancel}
	r.jobs.Store(job.JobId, e)
	context.AfterFunc(ctx, func() { r.jobs.CompareAndDelete(job.JobId, e) })
	return ctx, nil
}

// OnSignal is an sdkv1.SignalHandler. A process signal for a job this registry
// holds unfiles it, and when its conclusion is Canceled() — flow_stop_by_user,
// stop_command, timeout, long_time_without_command — cancels its context with
// ErrStopped. Any other ending (done, failure, …) leaves the context alone: the
// job has finished or is finishing on its own, and must not be cut short.
func (r *Registry) OnSignal(sig sdkv1.Signal) {
	if sig.Kind != sdkv1.RuntimeProcessSignal || sig.JobId == "" {
		return
	}
	v, ok := r.jobs.LoadAndDelete(sig.JobId)
	if !ok {
		return // somebody else's job — another flow's, another replica's
	}
	if sig.Conclusion.Canceled() {
		v.(*entry).cancel(ErrStopped)
	}
}

// CancelAll cancels every job the registry holds, with ErrShutdown — for a
// plugin about to exit, so its handlers see their contexts end and wind down.
// It sends nothing to the runtime: what a job reports, if anything, is its
// handler's call.
func (r *Registry) CancelAll() {
	r.jobs.Range(func(jobId, v any) bool {
		if r.jobs.CompareAndDelete(jobId, v) {
			v.(*entry).cancel(ErrShutdown)
		}
		return true
	})
}
