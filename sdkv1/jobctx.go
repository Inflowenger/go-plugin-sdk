package sdkv1

import (
	"context"
	"sync"
)

// NewCancelableJobHandler declares an action whose job stops with its process.
// The handler is handed the job's lifetime as ctx, cancelled when the runtime
// concludes the job's process as Canceled() — a user stopping the flow, a stop
// command, the workflow's timeout, the node's idle window — so passing ctx down
// to a model call, an HTTP request or an MCP session aborts it in flight.
//
//	p.AddAction(sdkv1.Action{
//		Method:         "run",
//		RequestHandler: sdkv1.NewCancelableJobHandler(runHandler),
//	})
//
//	func runHandler(ctx context.Context, job sdkv1.Job) {
//		// ... pass ctx to whatever must die with the process ...
//		if ctx.Err() != nil {
//			return // the runtime has stopped listening: do not Done
//		}
//		job.Done(result)
//	}
//
// It is opt-in per action, and the plain JobHandler remains the default for a
// reason: a job the plugin accepted keeps running after its flow stops, because
// a later run of the node may build on that progress (the runtime hands the
// previous jobId back in `_registry`). Wrap only the actions whose work must not
// outlive the process — a paid call nobody will read, a stream to close, a lock
// to release — so one plugin can hold an abortable `call_tool` beside a 10-hour
// job that outlives the flow that started it.
//
// Nothing else is needed: no OnSignal, no bookkeeping. The SDK routes stop
// signals to the job itself, before any handler the plugin registered with
// OnSignal runs, so the two never compete for the port.
//
// # Lifetime
//
// ctx ends — and the job leaves the plugin's registry — on the first of:
//
//   - its terminal command. Done, DoneWithError, DoneWithErrorData,
//     DoneWithErrorCode (and a Progress above 99) end the process, so they
//     release the job themselves, after the command is sent. This is the
//     ordinary ending of a job that was never stopped. next_tags routes and
//     does not end anything.
//   - a stop signal for the job.
//   - the handler returning, however it does: a return after a cancellation, a
//     path that forgot to Done, a panic.
//
// And a process signal concluding the job any other way (failure,
// internal_error, bad_request, …) unfiles it without cancelling: that process is
// over, so nothing could stop the job any more, and a handler that hung before
// reaching Done does not leave an entry behind.
//
// Once a terminal command is out, ctx is done: work still holding it stops with
// the process.
//
// # Isolation
//
// Stops are matched by jobId, and only by jobId. The runtime publishes process
// signals on ONE subject per plugin, `inflow.plugin.<PLUGIN_ID>.proc`, so every
// process of a plugin receives every one of that plugin's signals: those of
// jobs in other flows running at the same time, and, when the plugin is
// deployed as several replicas, those of jobs this process never accepted. There
// is no flowId on the wire. A signal for a job this process does not hold is the
// ordinary case, and does nothing.
//
// One gap is inherent: the job is filed when its handler starts, on its own
// goroutine, just after the SDK has acknowledged it — so a stop landing in that
// instant finds nothing filed and the job runs to its end. Closing it would mean
// remembering stops for jobs not yet filed, and on a broadcast port that is
// every other flow's and replica's job: a set that only grows.
func NewCancelableJobHandler(handler func(ctx context.Context, job Job)) JobHandler {
	return func(job Job) {
		ctx, release := trackJob(&job)
		defer release()
		handler(ctx, job)
	}
}

// jobTracker is what a job's plugin offers to file the job under; *Plugin is
// one. Asserted rather than added to IPlugin, which is exported and would break
// any other implementation of it.
type jobTracker interface {
	jobRegistry() *jobCancels
}

// trackJob files the job with its plugin's registry. A job with no plugin behind
// it — built by hand, in a test — has no signal port to be stopped by, but keeps
// the rest of the contract, so a throwaway registry serves it.
func trackJob(job *Job) (context.Context, func()) {
	if t, ok := job.plugin.(jobTracker); ok {
		return t.jobRegistry().track(job)
	}
	return new(jobCancels).track(job)
}

// jobCancels holds a cancel func per in-flight cancelable job, keyed by jobId.
// One per plugin, on Plugin; must not be copied after first use.
type jobCancels struct {
	inflight sync.Map // jobId -> *jobCancel
}

// jobCancel boxes a CancelFunc so an entry can be compared on release: func
// values are not comparable, a pointer is.
type jobCancel struct {
	cancel context.CancelFunc
}

// track files the job and returns its context and release. It stamps the
// release onto the job — that is how the job's own terminal command unfiles it,
// and why it takes the job by pointer — so it runs before the job is handed to
// the handler, and every copy taken from then on carries it.
func (c *jobCancels) track(job *Job) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	entry := &jobCancel{cancel: cancel}
	c.inflight.Store(job.JobId, entry)

	jobId := job.JobId
	var once sync.Once
	release := func() {
		once.Do(func() {
			// Only this track's own entry: a second track for the same jobId
			// has replaced it, and must not be unfiled by the first's release.
			c.inflight.CompareAndDelete(jobId, entry)
			cancel()
		})
	}
	// A job tracked twice keeps both releases, so the first context is still
	// freed when the job ends rather than leaking until it is collected.
	if prev := job.release; prev != nil {
		job.release = func() { release(); prev() }
	} else {
		job.release = release
	}
	return ctx, release
}

// handleSignal routes a process signal to the job it names, when this process
// holds that job: a Canceled() conclusion cancels it; any other only unfiles it,
// leaving its context alone — a job that ended any other way has finished or is
// finishing on its own, and must not be cut short.
func (c *jobCancels) handleSignal(sig Signal) {
	if sig.Kind != RuntimeProcessSignal || sig.JobId == "" {
		return
	}
	if sig.Conclusion.Canceled() {
		c.cancel(sig.JobId)
		return
	}
	c.inflight.Delete(sig.JobId)
}

// cancel cancels the job filed under jobId and reports whether this process was
// holding one. A miss means the job is somebody else's — another flow's,
// another replica's — or already released.
func (c *jobCancels) cancel(jobId string) bool {
	if jobId == "" {
		return false
	}
	v, ok := c.inflight.LoadAndDelete(jobId)
	if !ok {
		return false
	}
	v.(*jobCancel).cancel()
	return true
}
