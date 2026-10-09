package sdkv1

import (
	"context"

	"github.com/google/uuid"
)

// MiddlewareFunc is one function of an action's middleware: run for each of
// the action's requests, in order with the others, before the job is accepted.
// It gets the job's context so far and returns it — with whatever it bound —
// for the next function, and in the end for the handler (Job.Context()).
//
//	func register(ctx context.Context, job sdkv1.Job) (context.Context, error) {
//		runs.Store(job.JobId, &run{status: "running"}) // before the runtime knows the jobId
//		return ctx, nil
//	}
//
// It runs before the job is accepted — before the SDK replies the jobId to the
// runtime — so nothing can happen to the job (a stop, a query from a later run)
// before what a function set up under that jobId is in place. That is where
// per-job registration goes.
//
// Returning an error rejects the request: the runtime gets the error instead of
// a jobId, and neither the functions after it nor the handler run. A panic is
// recovered and rejected the same way. Returning a nil context keeps the one it
// was given.
//
// The job's context ends when the handler returns, or when the request is
// rejected: a function that must clean up when the job ends does it with
// context.AfterFunc on the context it returns.
//
// The functions run on the job's own goroutine, so slow work there delays only
// this request's reply — but the runtime gives up on a jobId it waits too long
// for, so keep them quick.
type MiddlewareFunc func(ctx context.Context, job Job) (context.Context, error)

// Middlewares is an ordered list of middleware functions — the value of
// Action.Middleware. Build one with Use.
type Middlewares []MiddlewareFunc

// Use lists middleware functions, in the order they run — the helper for
// Action.Middleware:
//
//	Middleware: sdkv1.Use(stops.Middleware, trace, register),
//
// Nil functions are skipped.
func Use(fns ...MiddlewareFunc) Middlewares {
	return Middlewares(nil).Append(fns...)
}

// Append returns the list with fns added after it; nil functions are skipped.
// The list it is called on is left as it was.
func (m Middlewares) Append(fns ...MiddlewareFunc) Middlewares {
	out := append(Middlewares(nil), m...)
	for _, fn := range fns {
		if fn != nil {
			out = append(out, fn)
		}
	}
	return out
}

// JobID is the middleware function that names a request's job: it binds a fresh
// UUID to the context as the jobId (read with JobIDFromContext), and the SDK
// sets Job.JobId from it, so every function after it sees the job named. Every
// request runs it first; WithJobID replaces it, for a plugin that names its
// jobs its own way.
func JobID(ctx context.Context, _ Job) (context.Context, error) {
	return WithJobIDContext(ctx, uuid.NewString()), nil
}

type jobIDKey struct{}

// WithJobIDContext returns ctx carrying jobId as the job's id. A replacement
// for JobID binds its id with it; the SDK takes Job.JobId from there.
func WithJobIDContext(ctx context.Context, jobId string) context.Context {
	return context.WithValue(ctx, jobIDKey{}, jobId)
}

// JobIDFromContext returns the jobId bound to ctx, or "" — for code deep in a
// call chain that has the context but not the Job: a logger, a tracer.
func JobIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(jobIDKey{}).(string)
	return id
}
