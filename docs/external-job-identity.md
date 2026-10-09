# One job, one id — adopting an external service's job id

> Advanced pattern. It needs [§ Middleware](jobs-and-commands.md#middleware--functions-run-before-the-job-is-accepted),
> [§ Signals](jobs-and-commands.md#signals--when-the-runtime-ends-a-process) and
> [`jobstop`](jobs-and-commands.md#stopping-a-job-with-its-flow--package-jobstop) first.

A great many plugins are **middlemen**. The node on the canvas does no work
itself: it registers work with a service that has its own API, its own queue and
its own lifetime — Joern's HTTP server for code-property-graph queries, a
rendering farm, a training job, a long-running scan — and then reports back.

Those services almost always **name the work themselves**. You `POST /query` and
Joern answers `{"queryId": "q-8f21c47b3d"}`. Everything afterwards — polling, logs,
cancelling, the service's own dashboard — is addressed by that id.

The runtime names work too. A plugin job is identified by the `jobId` the SDK
replies in the request→job handshake, and every command the job sends travels on
`inflow.cpu.<PLUGIN_ID>.<JOB_ID>.<command>`.

So the naive middleman holds **two names for one piece of work** and a map
between them. This document is about not doing that: about running the job under
the service's own id, which the SDK's middleware makes a three-line change, and
about what that buys — one correlation id end to end, cancellation that reaches
the service, and resumability across runs. It is, in effect, a distributed
transaction over two systems that share nothing but a name.

---

## 1. The cost of two id spaces

Here is the middleman everyone writes first:

```go
var upstream sync.Map // plugin jobId -> joern queryId   ← the thing to delete

func queryHandler(job sdkv1.Job) {
    queryId, err := joern.Register(ctx, project, query)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }
    upstream.Store(job.JobId, queryId)
    defer upstream.Delete(job.JobId)
    // ... poll queryId, report under job.JobId ...
}

func onSignal(sig sdkv1.Signal) {
    if v, ok := upstream.Load(sig.JobId); ok && sig.Conclusion.Canceled() {
        joern.Cancel(context.Background(), v.(string)) // best effort
    }
}
```

It works, until it doesn't. The map is the weak link in five separate ways:

| Problem | Why the map causes it |
|---|---|
| **A window with no mapping** | The job is accepted — the runtime knows the `jobId` and may stop it at any moment — before `Register` has returned. A stop arriving in that window finds nothing to cancel, and the query runs on in Joern with nobody watching it. |
| **It dies with the process** | The map is process memory. A redeploy, a crash or an OOM kill between register and done loses every correlation; the queries stay alive upstream and become unattributable garbage. |
| **It is not where the signal is** | Process signals are broadcast to **every** process of the plugin (`inflow.plugin.<PLUGIN_ID>.proc`). The replica that hears the stop is usually not the replica holding the map entry, so the abort never leaves. |
| **Nobody can join the two sides** | The canvas, the flow's context and the logs show `jobId`; Joern's dashboard and its logs show `queryId`. Debugging a slow query means a human joining two id spaces by timestamp. |
| **Resume is impossible** | The runtime hands the previous run's `jobId` back in `_registry`. With a map that has since died, the previous `jobId` resolves to nothing, so a re-run starts a *second* Joern query instead of reattaching to the first. |

Every one of these is a symptom of the same thing: the identity of the work was
decided twice, independently, in two places.

---

## 2. The mechanism: name the job before it is accepted

The SDK does not mint a `jobId` behind your back. It mints one in a **middleware
function** — `sdkv1.JobID`, the first function of every request's pipeline — and
middleware runs *before* the job is accepted:

```
request ─▶ JobID ─▶ p.Use(...) ─▶ Action.Middleware(...) ─▶ accept: reply jobId ─▶ handler
          └── mints a uuid ──┘   └── any of these may rename the job ──┘
```

After **each** function, the SDK re-reads the id bound to the context and keeps
`Job.JobId` in step with it ([`inflowV1.go`](../sdkv1/inflowV1.go)
`runMiddleware`). So any function in the chain can name the job, and the last
one to do so wins:

```go
func registerQuery(ctx context.Context, job sdkv1.Job) (context.Context, error) {
    queryId, err := joern.Register(ctx, project, query)
    if err != nil {
        return nil, err // the request is rejected; nothing was ever accepted
    }
    return sdkv1.WithJobIDContext(ctx, queryId), nil // ← the job is now "q-8f21c47b3d"
}
```

```go
Middleware: sdkv1.Use(registerQuery, stops.Middleware),
```

That is the whole mechanism. From the moment the pipeline ends, `q-8f21c47b3d` is not
a value the plugin remembers — it **is** the job, everywhere:

| Where | Carries the service's id |
|---|---|
| `job.JobId` in the handler | `q-8f21c47b3d` |
| `sdkv1.JobIDFromContext(job.Context())` | `q-8f21c47b3d` — for a logger or tracer deep in the call chain |
| Every command subject | `inflow.cpu.joern-plugin.q-8f21c47b3d.progress` |
| The handshake reply to the runtime | `{"jobId":"q-8f21c47b3d"}` — the canvas, the flow's record |
| The stop signal's payload | `sig.JobId == "q-8f21c47b3d"` |
| The next run's `_registry["jobId"]` | `q-8f21c47b3d` |

Two id spaces became one. The `sync.Map` is deleted, not improved.

> **Plugin-wide alternative.** `sdkv1.WithJobID(fn)` *replaces* `sdkv1.JobID`
> for every action of the plugin. Use it only when the plugin as a whole is a
> proxy for one service; use `Action.Middleware` when some actions talk to the
> service and others (a settings test, a pure transform) do not.

---

## 3. The scenario: a Joern middleman, end to end

Joern runs as its own HTTP server. The plugin node is the only way an
Inflowenger flow reaches it.

```
  canvas / flow                 plugin process                    joern :8080
        │                                │                                 │
execute │─── inflow.v1.<id>.cpg.query ─▶ │                                 │
        │                                │─── POST /query ───────────────▶ │
        │                                │◀── 201 {queryId: q-8f21c47b3d}  │ ← the name
        │                                │                                 │
        │                                ├─ files q-8f21c47b3d (stops)     │
        │◀── {jobId: q-8f21c47b3d} ───── ┤  accept                         │
        │                                │                                 │
        │◀─ …q-8f21c47b3d.progress ───── ┤◀─ GET /query/q-8f21c47b3d ──▶   │
        │         (repeat)               │                                 │
        │                                │                                 │
  user  │                                │                                 │
stops ─ │── inflow.plugin.<id>.proc ───▶ │                                 │
        │   {jobId: q-8f21c47b3d,        ├─ ctx cancelled (ErrStopped)     │
        │    conclusion:                 │                                 │
        │     flow_stop_by_user}         └─ DELETE /query/q-8f21c47b3d ─▶  │ ← abort
        │                                │                                 │
```

Note what is *not* in that diagram: no lookup table, and no step in which one
side holds a name the other cannot resolve.

### The plugin

```go
var (
    joern = &Joern{base: "http://joern:8080", http: &http.Client{Timeout: 10 * time.Second}}
    stops jobstop.Registry
)

type QueryInput struct {
    Project string `json:"project"`
    Query   string `json:"query"`
}

func main() {
    p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
    if err != nil {
        log.Fatal(err)
    }
    p.Intro(sdkv1.PluginIntro{Name: "JOERN.QUERY", Author: "inflow Dev. Team", Version: "v0.1.0"})

    // Two handlers on the one signal port: cancel the local work, and tell Joern.
    p.OnSignal(sdkv1.ChainSignals(stops.OnSignal, abortUpstream))

    p.AddAction(sdkv1.Action{
        Method:      "cpg.query",
        Title:       "Run CPG query",
        Description: "Register a CPG query on the Joern server and stream its result back",
        // Order matters: the job is named by the service FIRST, so everything
        // after it — stops' registry included — is keyed by the shared id.
        Middleware:     sdkv1.Use(registerQuery, stops.Middleware),
        RequestHandler: queryHandler,
    })

    if err := p.Start(); err != nil {
        log.Fatal(err)
    }
    select {}
}
```

### The middleware: enlist, then name

```go
// registerQuery enlists the work upstream and runs the job under the id Joern
// gave it. It runs before the job is accepted, so the id it binds is the id the
// runtime is told.
func registerQuery(ctx context.Context, job sdkv1.Job) (context.Context, error) {
    in, err := sdkv1.CastRequestTo[QueryInput](job.Req.Data)
    if err != nil {
        return nil, fmt.Errorf("bad request: %w", err)
    }

    // Reattach to this node's previous run instead of starting a second query.
    if prev, ok := in.Registry["jobId"].(string); ok && joern.Alive(ctx, prev) {
        return sdkv1.WithJobIDContext(ctx, prev), nil
    }

    queryId, err := joern.Register(ctx, in.Body.Project, in.Body.Query)
    if err != nil {
        return nil, fmt.Errorf("joern refused the query: %w", err) // nothing enlisted
    }
    if err := usableJobID(queryId); err != nil {
        joern.Cancel(context.WithoutCancel(ctx), queryId) // never accepted: undo it
        return nil, err
    }
    return sdkv1.WithJobIDContext(ctx, queryId), nil
}

// usableJobID rejects an id the runtime or the wire cannot carry: the job's
// command subjects are built from it, and fractal-core refuses an init reply
// whose jobId is shorter than 10 characters (see § 6).
func usableJobID(id string) error {
    if len(id) < 10 {
        return fmt.Errorf("jobId %q from joern is shorter than 10 characters", id)
    }
    if strings.ContainsAny(id, ". *>\t\r\n") {
        return fmt.Errorf("jobId %q from joern is not a usable subject token", id)
    }
    return nil
}
```

Read the three exits in order, because they are the pattern's whole safety
argument:

1. **Joern refuses** → error → the request is rejected. The runtime is told the
   reason instead of a `jobId`; the node never started; there is no upstream
   work and nothing to clean up.
2. **Joern accepts but the id is unusable** → compensate (`Cancel`), then
   reject. We registered something, so we un-register it before giving up.
3. **Joern accepts** → bind the id. The job is accepted *after* this returns, so
   the runtime learns about the job only once the upstream work exists and is
   named.

### The handler: the id is already shared

```go
func queryHandler(job sdkv1.Job) {
    ctx := job.Context()
    queryId := job.JobId // == the Joern queryId

    for {
        status, err := joern.Poll(ctx, queryId)
        switch {
        case ctx.Err() != nil:
            // Stopped: the runtime has concluded this job and stopped
            // listening. The abort is the signal handler's business.
            return
        case err != nil:
            job.DoneWithError(err.Error())
            return
        case status.Done:
            job.Done(map[string]any{"queryId": queryId, "result": status.Result}, "joern")
            return
        }
        job.Progress(status.Percent, sdkv1.Frame{Title: "Joern", Content: status.Stage})

        select {
        case <-ctx.Done():
            return
        case <-time.After(2 * time.Second):
        }
    }
}
```

The handler never translates an id, and the result it commits carries
`queryId` — which is `job.JobId` — into the flow's context, so a downstream node
(or a human reading the run) can address Joern directly.

---

## 4. Cancellation: making a stop cross the boundary

A stop is the moment the two systems must agree, and it is where the shared id
pays for itself twice.

When a user stops the flow, the runtime publishes on
`inflow.plugin.<PLUGIN_ID>.proc` with `{"jobId":"q-8f21c47b3d","conclusion":"flow_stop_by_user"}`
and stops attending the node. Two different things now have to happen, and the
plugin composes them on the one port with `sdkv1.ChainSignals`:

```go
p.OnSignal(sdkv1.ChainSignals(stops.OnSignal, abortUpstream))
```

**Local: stop doing the work.** `stops.OnSignal` looks the job up by
`sig.JobId` and cancels its context with `jobstop.ErrStopped`. The handler's
`Poll` returns, `ctx.Err()` is non-nil, and it returns **without reporting** —
the runtime is already gone, so a `Done` would retry against a subject with no
responder. (See [§ `jobstop`](jobs-and-commands.md#stopping-a-job-with-its-flow--package-jobstop).)

This is exactly where the ordering rule comes from. `stops.Middleware` files the
job under `job.JobId` *as it is when that function runs*. Put it before the
namer and it files a uuid, while the id on the wire is `q-8f21c47b3d` — the stop
arrives, matches nothing, and is silently lost. There is a test for that
mistake, so the rule is not folklore:
[`externalid_integration_test.go`](../sdkv1/externalid_integration_test.go)
`TestStopsBeforeTheNamerMissesTheStop`.

> **Rule.** The function that names the job comes first. Anything keyed on the
> `jobId` — `stops.Middleware`, your own registry, a span — comes after it.

**Remote: tell Joern.** Because the id on the signal *is* the Joern queryId, the
notification needs no local state at all:

```go
// abortUpstream tells Joern to drop the query when the runtime stops the flow.
// It needs no local state: the signal already names the query.
func abortUpstream(sig sdkv1.Signal) {
    if sig.Kind != sdkv1.RuntimeProcessSignal || !sig.Conclusion.Canceled() {
        return
    }
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := joern.Cancel(ctx, sig.JobId); err != nil {
        log.Printf("joern: could not abort %s: %v", sig.JobId, err)
    }
}
```

That handler is **stateless**, and that is a property the map version cannot
have. Signals reach every replica of the plugin, so whichever replica hears the
stop can abort the query — including one that never accepted the job, and
including a replica that started after the job did. The map version could only
abort from the one process still holding the entry.

Two consequences to design for:

- **Make the abort idempotent.** With several replicas, several `DELETE`s may
  arrive for one query. Joern should answer the second one `404`/`204` rather
  than erroring — or the plugin should treat those as success.
- **Filter on the conclusion.** Signals are published for *every* ending,
  `done` included. `Canceled()` is the set that means "cut short"
  (`flow_stop_by_user`, `stop_command`, `timeout`,
  `long_time_without_command`); aborting on `done` would cancel a query that
  already finished.

### Cleanup tied to the job instead of the port

When the compensation belongs to this process's job rather than to any replica —
closing a stream, releasing a lease, deleting a scratch artefact — hang it off
the job's context instead of the signal port. The context ends however the job
ends: handler returned, stop, rejection, `CancelAll`.

```go
context.AfterFunc(ctx, func() {
    if errors.Is(context.Cause(ctx), jobstop.ErrStopped) {
        joern.Cancel(context.WithoutCancel(ctx), queryId)
    }
})
```

Because the context also ends when a **later middleware function rejects the
request**, this is the compensation hook for a half-built pipeline: a function
that enlisted work upstream registers its undo with `AfterFunc`, and a rejection
two functions later undoes it without any special casing. Verified by
`TestRejectionAfterRegistrationCompensates`.

---

## 5. Why this is a distributed transaction

The flow's run and the Joern query are two independent systems that must begin
together, end together, and never disagree about what work exists. There is no
two-phase-commit coordinator between them and there cannot be — but the SDK's
accept stage gives the pattern the one thing 2PC provides: **a boundary where
the two sides are known to agree**, with compensations on either side of it.

| Transaction concept | Where it lives here |
|---|---|
| Transaction id | The shared `jobId` = Joern's `queryId`. One name, both sides. |
| `BEGIN` / enlist the participant | `registerQuery` — a middleware function, before accept. |
| Prepare → commit boundary | **Accept**: the SDK replies the `jobId`. Before it, nothing is visible to the runtime; after it, the job exists on both sides under one name. |
| Abort before commit | A middleware error → the request is rejected. The runtime sees an error, not a failed node. |
| Compensating action | `joern.Cancel` — from `AfterFunc` on the job's context (this job) or from the signal handler (any replica). |
| Work phase | The handler, polling under the shared id. |
| `COMMIT` / `ROLLBACK` reported | `job.Done(...)` / `job.DoneWithError(...)`. |
| Abort broadcast from the coordinator | The `proc` signal with `Conclusion.Canceled()`. |
| Recovery log, read on restart | `_registry["jobId"]` — the previous run's id, which is also the upstream id. |
| Participant discovery | None needed: the id resolves in both systems. |

The invariants that fall out, and the reason each holds:

1. **Accepted ⇒ enlisted.** The namer runs before accept, so the runtime never
   knows a job whose upstream work does not exist.
2. **Enlisted ⇒ nameable.** The id came *from* the service, so there is no
   upstream work the plugin cannot address — not even after a restart.
3. **Rejected ⇒ compensated or never enlisted.** The job's context ends on
   rejection, so `AfterFunc` undos run; a namer that failed registered nothing.
4. **Any stop is actionable by any replica.** The abort needs only `sig.JobId`.
5. **Crash ⇒ recoverable.** The id survives in the flow's own record
   (`_registry`), not in the plugin's memory, so the next run reattaches instead
   of duplicating.

What it is *not*: atomic. A crash between `Register` and the accept reply leaves
a query running that this run will never report (invariant 1 covers the
runtime's view, not the service's). That residue is bounded and
self-describing — the query exists, named, under the project it was registered
for — so the usual answers are a TTL on the service side, or a sweeper that
cancels queries no flow is attending. This is a saga, not 2PC: compensation,
not locking.

### Resuming: job discovery across runs

The same property makes a re-run cheap. `_registry["jobId"]` is the previous
run's id, which *is* the upstream query's id, so the namer can ask the service
whether that work is still alive and adopt it instead of registering again:

```go
if prev, ok := in.Registry["jobId"].(string); ok && joern.Alive(ctx, prev) {
    return sdkv1.WithJobIDContext(ctx, prev), nil
}
```

The new run then reports progress on `inflow.cpu.<id>.q-8f21c47b3d.progress` and
finishes the work the previous process started — an hour of CPG construction not
thrown away because a flow was restarted. Taken to its conclusion, the run need
not wait for the work at all: it can report where the work has got to and end,
leaving a later run to collect it. That is
[detached-work.md](detached-work.md). With two id spaces this is simply not
expressible: the previous `jobId` means nothing to Joern, and the map that once
translated it is gone.

---

## 6. Rules, limits and failure modes

**The id must be at least 10 characters.** This one bites hardest, because the
service decides the length. fractal-core's plugin node refuses an init reply
whose `jobId` is shorter than 10 characters and fails the node with
`init failed. invalid job ID` (`engine/prim_nodes/plugin.go`). A uuid is 36, so
the default namer never trips it — but `q-7`, `#412` and `job12` do. Pad or
prefix a short upstream id into something stable and reversible
(`joern-00000412`, or the scope prefix below), and validate it in the namer
rather than discovering it as a failed node.

**The jobId becomes a NATS subject token.** Commands are published to
`inflow.cpu.<PLUGIN_ID>.<JOB_ID>.<command>`, so an id containing `.`, a space,
`*` or `>` does not merely look odd — it changes the subject's structure and the
job's commands land nowhere. Validate the id you adopt (`subjectSafe` above) and
reject, or encode it (base32/hex of the upstream id) if the service's ids are
arbitrary strings. Never adopt an id straight out of a response without
checking.

**The id must be unique across the plugin, not just per project.** Services that
number work per tenant or per project (`#1`, `#2`) will collide: two flows get
one `jobId`, and their commands and stops interleave. Prefix with the scope —
`proj42-#1` — so the result is unique for the whole plugin.

**Registration happens inside the accept latency budget.** The runtime is
waiting for a `jobId` while your middleware runs, and it waits **15 seconds**
(fractal-core's `initRequestTimeout`) before failing the node as unreachable.
One fast call is fine; a 30-second `POST`, a retry loop, or the work itself is
not. Give the registration call its own short
timeout and keep everything else in the handler.

**Reject or accept-then-fail is a design decision.** A middleware error means
the node *never ran*: the runtime gets an error in place of a `jobId`, and there
is no job for the flow to show as failed. When the flow should see a failed node
with a message, scope or a partial result instead, accept the job and fail it
from the handler with `job.DoneWithError` / `job.DoneWithErrorData`. Rule of
thumb: reject when there is nothing to report, fail the job when there is.

**Requests can be retried; registration should be idempotent.** A redelivered
execution request runs the pipeline again and would register a second query.
Where the service supports a client-supplied idempotency key, derive one from
the request (the flow's node + input hash) and pass it; otherwise check
`_registry` first, as above.

**Nameless jobs are rejected.** If the namer binds no id, the SDK rejects the
request rather than accept a job named `""`. A namer that may legitimately fall
back must bind something — e.g. `sdkv1.WithJobIDContext(ctx, uuid.NewString())`.

### When *not* to adopt the external id

| Situation | Do this instead |
|---|---|
| The service names the work only *after* it starts (the id arrives on the first streamed event) | Keep the SDK's uuid; correlate in the handler, which holds both ids for the whole job's life, and use `jobstop` as usual. |
| Registration is slow or unreliable | Keep the uuid and register in the handler, reporting failure with `DoneWithError`. Accept latency is not the place to fight a flaky backend. |
| The service **accepts** a client-supplied id (idempotency keys, run ids, external ids) | **Impose** instead of adopt: keep `sdkv1.JobID`'s uuid and send `job.JobId` upstream as the service's id. Same single-name benefit, no validation worries, and the registration becomes idempotent by construction. |
| The action does no external I/O at all (a transform, a context read) | Nothing to do: the default `sdkv1.JobID` is right, and the action needs no middleware. |

Adopt or impose, the goal is the same and so is the place it is decided: **one
name for one piece of work, settled before the job is accepted.**

---

## 7. Checklist

- [ ] The namer is the **first** middleware function of the action (or
      `sdkv1.WithJobID` for a whole-plugin proxy).
- [ ] `stops.Middleware` and anything else keyed on the `jobId` comes **after**
      it.
- [ ] The adopted id is validated: **≥ 10 characters**, a usable subject token,
      and unique plugin-wide.
- [ ] Registration has its own timeout and does no real work.
- [ ] Failure to register → reject; failure the flow should see → accept and
      `DoneWithError`.
- [ ] `p.OnSignal(...)` is registered **before** `Start()` — without it the port
      is not subscribed and no stop ever arrives (`Start` logs
      `Signals not subscribed on …`).
- [ ] The signal handler filters `Kind == sdkv1.RuntimeProcessSignal` and
      `Conclusion.Canceled()`.
- [ ] The upstream abort is idempotent across replicas.
- [ ] The handler checks `ctx.Err()` before reporting, and does not report after
      a stop.
- [ ] The namer consults `_registry["jobId"]` if re-running should reattach.

## See also

- [detached-work.md](detached-work.md) — the pattern this one enables: work that
  outlives the flow run, observed across runs through `_registry`.
- [jobs-and-commands.md § Middleware](jobs-and-commands.md#middleware--functions-run-before-the-job-is-accepted) — the pipeline, context flow, rejection semantics.
- [jobs-and-commands.md § Signals](jobs-and-commands.md#signals--when-the-runtime-ends-a-process) — the port, conclusions, why stopping is opt-in.
- [protocol-inflowv1.md](protocol-inflowv1.md) — the handshake that carries the `jobId`, and the signal payload.
- [`sdkv1/externalid_integration_test.go`](../sdkv1/externalid_integration_test.go) — this pattern's properties as executable tests.
- [cookbook.md § Skill 13](../cookbook.md#skill-13--run-the-job-under-an-external-services-id-advanced) — the condensed recipe.
