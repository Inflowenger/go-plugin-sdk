# Jobs & commands

Everything a plugin *does* happens inside a `Job`. When the runtime executes one
of your actions, the SDK acknowledges the request with a fresh `jobId` and hands
your `RequestHandler` a `Job` bound to that id and to the NATS connection. Through
it you report progress, read and write the flow's context, call an extrinsics
service, and finish the job.

```go
type Job struct {
    Action string  // the action method that was invoked
    JobId  string  // uuid correlating all commands for this execution
    Req    Request // the raw request (Data []byte, Header, Plugin)
}
```

Each method on `Job` publishes a request to
`inflow.cpu.<PLUGIN_ID>.<JOB_ID>.<command>` and returns the runtime's reply.

## Reading the request

`job.Req.Data` is the raw JSON body. Decode it into your own type with
`CastRequestTo`, which unwraps the `{ "_registry", "body" }` envelope:

```go
req, err := sdkv1.CastRequestTo[MyInput](job.Req.Data)
if err != nil {
    job.DoneWithError(err.Error())
    return
}

// req.Body     -> MyInput   (the user's form input)
// req.Registry -> map[string]any   (runtime metadata, incl. previous run)

if prev, ok := req.Registry["jobId"]; ok {
    doneAt := time.Unix(int64(req.Registry["doneAt"].(float64)), 0)
    fmt.Printf("previous run %s finished at %v\n", prev, doneAt)
}
```

`job.Req.Header` exposes the NATS message headers if you need transport metadata.

## Progress

Report progress from `0` to `100`. Each update carries a `Frame` — a titled status
message the canvas can show:

```go
job.Progress(10, sdkv1.Frame{Title: "init step", Content: "task is starting"})
job.Progress(50, sdkv1.Frame{Title: "working", Content: "halfway there"})
```

A `Frame` has three fields:

| Field     | Type             | Purpose                                                                 |
| --------- | ---------------- | ----------------------------------------------------------------------- |
| `Title`   | `string`         | Short label for the frame.                                              |
| `Content` | `string`         | Streamed status body shown on the node.                                 |
| `Meta`    | `map[string]any` | Reserved, open bag for frontend-effective extras (e.g. an `items` list) carried through untouched. Omit when unused. |

```go
job.Progress(75, sdkv1.Frame{
    Title:   "indexing",
    Content: "3 of 4 files",
    Meta:    map[string]any{"items": []string{"a.go", "b.go", "c.go"}},
})
```

On the wire a sub-100 update is `{progress, frame}` (see the summary table below).
It may also carry `details` — a partial payload the core forwards alongside the
frame for jobs that surface intermediate data; at `100`, `details` instead is the
terminal payload committed to the node.

Progress is advisory feedback; it does not, by itself, complete the job — only
reaching 100 (via `Done`/`DoneWithError`) does.

## Finishing a job

```go
// Success — commits `data` as this node's output. Progress becomes 100.
job.Done(map[string]any{"status": "ok", "body": result})

// Success, committing on an explicit key path (variadic key segments joined by ".")
job.Done(payload, "result", "http")

// Failure — completes with an error payload.
job.DoneWithError("upstream returned 500")

// Failure that still has something to report/keep — same conclusion, extra details.
job.DoneWithErrorData("upstream returned 500", map[string]any{"messages": conversation})

// Failure carrying the plugin's own error number alongside the message.
job.DoneWithErrorCode(429, "upstream rate limited", nil)
```

Under the hood all four are a `progress` command at `100`: `Done` sends
`{progress:100, details:data, commit_on:key}`, and the error variants add the
`error` field — `{progress:100, error:{code,message}}` — with `details` and
`commit_on` filled exactly as `Done` fills them. A handler should call exactly
one of them before returning.

The reason travels on its own `error` field, **not** as a detail. Two things
follow: nothing in `data` is reserved any more (a key named `error` is the
plugin's to use), and the presence of that field — not its contents — is what
makes the finished job a failed one, so an empty message still fails the job.
`code` is the plugin's own number in the plugin's own numbering: the core carries
it next to the message so the plugin's owner can be asked what it means, and
never maps it onto a platform status. Leave it `0` when the plugin has none.

**Failing does not stop the flow.** All three conclude the node the same way — the
error variants are still a completed job, just one that reports a failure —
its `details` are committed either way. Any node can fail; the platform treats that as information rather than
flow control, reporting the reason on the event stream and writing it into the
context, then continuing to the next node. That is what makes an error something
a downstream Rule can branch on. If a failure should change where the flow goes,
express that on the canvas, not by trying to halt it from inside the plugin.

Reach for `DoneWithErrorData` when the failure is not the whole story. The details
of a terminal command are what gets committed onto the node's scope, so a bare
`DoneWithError` — which sends no details at all — drops whatever the node had
persisted there. Passing that state back through `data` keeps it readable on the next run,
and gives the canvas (and any downstream branch the node routed to before
concluding) the context to act on rather than just a message.

## Reading the flow context

A running flow has a shared **context** tree. A plugin can read it mid-execution:

```go
// The whole current scope (raw bytes — usually JSON).
cur := job.CmdGetCurrentScope()
if b, ok := cur.([]byte); ok {
    fmt.Println("current scope:", string(b))
}

// A slice of context addressed by JSON path.
scope := job.CmdGetScope("$.OPA")
if b, ok := scope.([]byte); ok {
    fmt.Println("$.OPA =", string(b))
}
```

Both return `any`: the runtime's reply bytes on success, or an `error` value if the
command failed — type-assert to `[]byte` to read the data, as the samples do.

## `$this` — the node's own location

Every JSON path a plugin hands the runtime may use **`$this`**, a root of
inflow's own that is not part of the JSON path spec. It stands for the location
the node is running on — the slice its `scope` selected — so a plugin can address
the data it was handed without knowing where in the context tree it sits.

```go
// Node scope is `$.tickets[*]`, so this run was handed `$.tickets[2]`.
job.CmdGetScope("$this")             // → the whole ticket
job.CmdGetScope("$this.customer.id") // → $.tickets[2].customer.id
job.CmdSetOnPath("$this.verdict", map[string]any{"ok": true})
job.Done(map[string]any{"ok": true}, "$this", "verdict") // commit_on: $this.verdict
```

The runtime rewrites `$this` to the run's location before parsing the path, so
it works anywhere a path is accepted: `CmdGetScope`, `CmdSetOnPath`, the optional
commit key on `Done` / `DoneWithErrorData`, and inside the `{{ }}` variables of a
[`CmdSvcCall` `op` payload](#calling-an-extrinsics-service). A path without
`$this` is untouched, and `$thisOne` is an ordinary field name, not the keyword.

Why it matters: a node whose scope selects **many** locations (`$.tickets[*]`)
runs once per location. A hardcoded `$.tickets[0]` reads the same ticket every
time; `$this` follows the run. It is also the only way to write a plugin that
does not care where the designer pointed its scope.

`$this` is not the same as `CmdGetCurrentScope()`: that returns the node's *own
output* slot (the location plus the node's `key`), whereas `$this` is the input
location the node was pointed at.

## Writing to the flow context (context injection)

Commit data back into the flow's context at a JSON path. This is how a plugin
**injects** results other downstream nodes will read:

```go
job.CmdSetOnPath(`$["doc appendix"]`, map[string]any{
    "itemXterm": []uint64{1, 3, 42, 2300},
})
```

The path is a JSON path into the context tree; the map is the value written there.
This is a `commit` command carrying `{commit_on: path, details: data}`. It may be
written against `$this` to commit relative to the node's own location.

## Routing outbound ports at runtime

A node can have several **outbound ports**, each carrying a route **tag**. By
default the flow follows every port; `CmdNextFilter` narrows that to just the
tag(s) you name, so downstream branching is decided at runtime by your handler:

```go
// Fire only the ports tagged "approved" and "notify" next; others are skipped.
job.CmdNextFilter([]string{"approved", "notify"})
```

The canonical use is **LLM tool routing**: an LLM node binds one function per
outbound port (the function name *is* the port tag), and when the model answers
with a tool call the handler routes the flow out of the matching port —
`job.CmdNextFilter([]string{calledFunctionName})`. Call it before `Done`; skip it
entirely to let the flow follow its default route.

### Declaring outbound ports on the action

`CmdNextFilter` decides *which* tags fire at runtime; `Action.Outbound` is the
design-time half that declares *what ports exist* so the canvas can draw them
ahead of time. It is an optional slice of `OutboundPort`:

```go
p.AddAction(sdkv1.Action{
    Method: "review",
    Title:  "Review",
    Outbound: []sdkv1.OutboundPort{
        {Title: "Approved", Tags: []string{"approved"}, Description: "passed review"},
        {Title: "Rejected", Tags: []string{"rejected"}, Description: "needs changes"},
    },
    RequestHandler: func(job sdkv1.Job) {
        // ...decide the outcome, then route out the matching branch:
        job.CmdNextFilter([]string{"approved"})
        job.Done(map[string]any{"status": "ok"})
    },
})
```

The whole `Outbound` slice ships with the action on the `@actions` subject, so
the frontend renders **one output port per entry** — labelled by `Title`,
explained by `Description` — and stamps every edge drawn from that port with the
port's `Tags`. Your handler then names the tag(s) to follow via `CmdNextFilter`;
edges carrying other tags are skipped. Leave `Outbound` nil for the common
single-output action.

This is a convenience, **not** an essential feature. The same branching already
works by mixing a plugin with a downstream **contract node** that fans out on the
committed result — `Outbound` just keeps the port topology, its tags, and its
documentation on the action itself instead of wiring them by hand on the canvas.

## Calling an extrinsics service

`CmdSvcCall` invokes an **extrinsics service** through the runtime — the same
backend call an extrinsics node makes, but issued mid-job from your handler:

```go
resp := job.CmdSvcCall(
    "add.db.record",                     // action — what you ask the service to do
    map[string]any{"rows": batch},       // data   — the payload for the service
    map[string]any{"table": "events"},   // op     — operation metadata
)
if b, ok := resp.([]byte); ok {
    fmt.Println("svc replied:", string(b))
}
```

**`op` is resolved by the runtime before the call goes out.** Any root-level
string value in `op` containing `{{ $.path }}` — or `{{ $this.path }}` — is
replaced with the live context value, so you can defer a lookup to send time
instead of fetching it yourself with `CmdGetScope`:

```go
job.CmdSvcCall("add.db.record", data, map[string]any{
    "table":   "events",
    "orderId": "{{$this.id}}",      // this run's order
    "limit":   "{{$.cfg.limit}}",   // arrives as a number, not a string
})
```

A value that is exactly one placeholder keeps the scope value's JSON type; a
placeholder inside longer text is interpolated as text. Only root-level strings
are walked — a placeholder nested inside a map or slice in `op` is left alone.
`data` is **not** resolved; it is sent as you built it.

The action is required — an empty one returns an `error` without sending
anything. On the wire the action becomes a suffix of the command subject —
`inflow.cpu.<PLUGIN_ID>.<JOB_ID>.request/svc.<ACTION>` (e.g. `request/svc.log`,
`request/svc.add.db.record`) — and the body is a `CallSvcBody` envelope
(`{data, op}`).

The action is deliberately **not** a registered extrinsics subject. It names
*what you want done*; the runtime cuts the `request/svc.` prefix and re-issues
the call as a plain request addressed to the bare action (`add.db.record`) on
the plugin space, attaching the current node to the body. The backend decides
which actions it serves and what each maps to, so a plugin can never address an
arbitrary registered service subject directly — which keeps this surface safe. Two more things distinguish a plugin-originated call:

- **Origin tagging.** The runtime stamps the egress request with an
  `origin: plugin:<node title>` header. The receiving service can always tell
  the call came from *inside a plugin*, not from an extrinsics node the flow
  author placed on the canvas.
- **Grant enforcement.** Because this is effectively running an extrinsics node
  from within a plugin, a backend may not permit it. That policy lives on the
  service side: its svc handler inspects the `origin` header and refuses calls
  it hasn't granted — the refusal comes back as the service's reply. A transport
  failure (no service, timeout) nacks the command and ends the job with a
  bad-request conclusion, failing the node.

The canonical use is a **feeder plugin**: a plugin that ingests from an external
system and pushes into the main system — feeding a store or similar sink through
the extrinsics service — instead of only committing results into the flow
context.

On success the return value is the service's raw reply bytes (type-assert to
`[]byte`, as with the context reads); on failure it is an `error`.

The receiving side — subscribing to action subjects on the plugin space and the
grant policy — is implemented with **inflow-fusion**; see that repo's
`docs/plugin-svc-calls.md`.

## Signals — when the runtime ends a process

Everything above is the job talking *to* the runtime. The signal port is the one
channel that runs the other way: the runtime publishes on
`inflow.plugin.<PLUGIN_ID>.proc` the moment it stops attending a plugin node
process, saying which job ended and how.

```go
p.OnSignal(func(sig sdkv1.Signal) {
    if sig.Kind != sdkv1.RuntimeProcessSignal {
        return
    }
    fmt.Printf("job %s ended: %s\n", sig.JobId, sig.Conclusion)
})
```

Register it **before `Start()`** — `Start()` does the subscribing. `OnSignal(nil)`
installs a handler that just logs the port, which is handy while developing.

Registering a handler of your own replaces that logging — the port goes quiet
just as the plugin starts acting on it. `sdkv1.LogSignals("<plugin>")` is that
same line as a handler you can keep beside your own:

```go
p.OnSignal(sdkv1.ChainSignals(sdkv1.LogSignals("ai-decision"), stops.OnSignal))
// ai-decision: signal proc job=<uuid> conclusion=flow_stop_by_user canceled=true succeeded=false
// jobstop: job <uuid> cancelled: the runtime concluded its process flow_stop_by_user
```

Two lines, because they are two events: the signal **arriving**, and a job of
this process being **cut short** by it — the second comes from
`jobstop.Registry.OnSignal`, which logs only the jobs it holds. One subject
carries every signal of the plugin, so the first line also appears for jobs of
other flows, and of other replicas, that this process never accepted; those get
no second line.

```go
type Signal struct {
    Kind       PluginSignal // "proc" — the subject past inflow.plugin.<PLUGIN_ID>.
    Subject    string
    JobId      string       // the job this is about: the same id as Job.JobId
    Conclusion Conclusion   // how the runtime ended it
    Data       []byte       // raw payload, for kinds this SDK does not model
    Msg        *nats.Msg    // escape hatch; a signal is a publish — never respond
}
```

`Conclusion` has the full list of verdicts (see
[protocol-inflowv1.md](protocol-inflowv1.md#inflowplugin--signal-port-one-way-optional))
plus two predicates:

| Predicate | True for |
|-----------|----------|
| `sig.Conclusion.Succeeded()` | `done`, `next` — the job ended the way it intended. |
| `sig.Conclusion.Canceled()`  | `flow_stop_by_user`, `stop_command`, `timeout`, `long_time_without_command` — something outside the job cut it short. |

### Why this is optional, and why stopping is not the default

**A stopped process does not mean a stopped job.** When a user cancels a flow or a
workflow times out, the work the plugin took on deliberately keeps running. A
later process on the same node may rely on the progress this one made: the
runtime hands the previous `jobId` back in `_registry` (and the plugin sees it
again in the next execution's request), so a half-built export, an open import
cursor or a warmed cache is an asset, not garbage. Dropping it on every
cancellation would throw that away.

So the SDK does nothing about `proc` signals unless you ask. A plugin that never
calls `OnSignal` behaves exactly as it always has — **nothing breaks by ignoring
this**. Register a handler only for work that genuinely must not outlive the
process: a stream to close, an upstream request to abort, a lock or reservation
to release, a spend to stop.

The pattern is to file cancellable work under its `jobId` and let the signal find
it — and to file it *before the runtime knows the jobId*, which is what
middleware is for.

### Middleware — functions run before the job is accepted

Every action request runs a list of **middleware functions**, in order, before
the job is accepted:

```go
type MiddlewareFunc func(ctx context.Context, job sdkv1.Job) (context.Context, error)
type Middlewares    []MiddlewareFunc
```

```
request ─▶ JobID ─▶ plugin's (p.Use) ─▶ action's (Action.Middleware) ─▶ accept: reply jobId ─▶ handler
```

You list them with the `Use` helper — on one action, or for every action:

```go
p.Use(trace)                                   // every action
p.AddAction(sdkv1.Action{
    Method:     "run",
    Middleware: sdkv1.Use(stops.Middleware, register), // this action, in this order
    ...
})
```

- **`sdkv1.JobID`** runs first: it binds a fresh jobId (a UUID) to the context —
  `sdkv1.JobIDFromContext(ctx)` reads it — and the SDK sets `job.JobId` from it,
  so every function after it sees the job named. It is a function like any
  other: `sdkv1.WithJobID(fn)` replaces it for a plugin that names its jobs its
  own way. A function later in the chain may rename the job too — the SDK keeps
  `job.JobId` in step with the context after *every* function — which is how a
  plugin runs a job under an id its upstream service minted: see
  [external-job-identity.md](external-job-identity.md).
- Each function gets the context the one before it returned, and returns it —
  with whatever it bound — for the next; the last one's is the handler's
  `job.Context()`. Returning a nil context keeps the one it was given.
- Then the job is **accepted** — the jobId replied to the runtime — and the
  handler runs.

Because they run before the reply, the runtime does not know the jobId while
they run: nothing can happen to the job — a stop, a query from a later run —
before what a function set up under that jobId is in place.

```go
func register(ctx context.Context, job sdkv1.Job) (context.Context, error) {
    runs.Store(job.JobId, &run{status: "running"}) // before the runtime knows the jobId
    return ctx, nil
}
```

- **An error rejects the request**: the runtime gets the error instead of a
  jobId, and neither the functions after it nor the handler run. A panic is
  recovered and rejected the same way.
- **The job's context ends when the handler returns** (like an
  `http.Request`'s), or when the request is rejected. A function that must clean
  up when the job ends does it with `context.AfterFunc(ctx, cleanup)`.
- The functions run on the job's own goroutine, so a slow one delays only its
  own request's reply — but the runtime gives up on a jobId it waits too long
  for, so keep them quick.

The SDK adds no capability to a job on its own. Each one is a middleware function
you list where it is needed — and, if it reacts to how processes end, a signal
handler:

| Capability | Middleware function | Signal port |
|------------|---------------------|-------------|
| Stop with the flow | `stops.Middleware` | `stops.OnSignal` |
| A long-running job kept for a later run | yours, filing the jobId in your own map | yours, if it reacts to endings |
| Tracing | yours: start a span, end it with `context.AfterFunc` | — |
| [An external service's job id as the jobId](external-job-identity.md) | yours: register upstream, bind the id with `sdkv1.WithJobIDContext` | yours: abort upstream by `sig.JobId` |

The port keeps one handler: `sdkv1.ChainSignals(h1, h2, …)` composes several
into one.

### Stopping a job with its flow — package `jobstop`

`jobstop` is the stop capability, built from the two pieces above:

```go
var stops jobstop.Registry // one per plugin; the zero value is ready

p.OnSignal(stops.OnSignal) // before Start() — without it, no stop ever arrives
p.AddAction(sdkv1.Action{
    Method:         "long.export",
    Middleware:     sdkv1.Use(stops.Middleware),
    RequestHandler: exportHandler,
})

func exportHandler(job sdkv1.Job) {
    ctx := job.Context()
    if err := exportWithContext(ctx, job); err != nil {
        if ctx.Err() != nil {
            return // stopped by the runtime: nobody is listening, do not Done
        }
        job.DoneWithError(err.Error())
        return
    }
    job.Done(map[string]any{"ok": true})
}
```

`stops.Middleware` files the job under its jobId before it is accepted, and
unfiles it when its context ends; `stops.OnSignal` cancels it when the runtime
concludes its process as `Canceled()` (cause `jobstop.ErrStopped`) and unfiles
it on any other ending. `stops.CancelAll()` cancels every job it holds (cause
`jobstop.ErrShutdown`), for a plugin about to exit; it sends nothing to the
runtime. Without `p.OnSignal(stops.OnSignal)` the signal port is not
subscribed — `Start` logs `Signals not subscribed on …` — and no stop arrives.

**Isolation is by `jobId`, and it has to be.** The runtime publishes every
process signal of a plugin on one subject, `inflow.plugin.<PLUGIN_ID>.proc`, so
every process of that plugin hears all of them: the endings of jobs in other
flows running at the same time, and — when the plugin runs as several replicas —
of jobs this process never accepted. The payload carries no flowId; the `jobId`
is the only discriminator on the wire. A stop for a job the registry does not
hold does nothing.

Two things to keep in mind:

- **Signals arrive for every ending, including `done`.** `stops.OnSignal`
  cancels on `Canceled()` only; a hand-written handler should filter on
  `Conclusion` the same way.
- **The runtime is already gone.** By the time a stop cancels `ctx`, that job's
  command subjects have no responder: a `Progress` or `Done` from the stopped
  handler retries and fails, slowly. Check `ctx.Err()` and return — not the
  error a library returned, which may not say it was cancelled.

Handlers run on their own goroutine (so a slow one does not stall the port) and a
panic inside one is recovered and logged. Only the last registered handler is
kept.

### Beyond stopping: work that outlives the process

Two patterns build on the pieces above, and both start from the same place — a
middleware function that decides what the job *is* before the runtime is told:

#### One id across two systems

The pieces above compose into something larger than cancellation. A plugin that
fronts a service with its own job ids — a Joern server, a render farm — can
**adopt that id as the jobId** in a middleware function, so the runtime, the
plugin and the service all name the work the same way: no correlation map, a
stop that any replica can forward upstream, and a re-run that reattaches to the
previous run's upstream work through `_registry`. That pattern, its invariants
and its failure modes are
[external-job-identity.md](external-job-identity.md).

#### The flow as an observer

Work measured in hours fits in no job: the runtime waits 15s for a `jobId`,
gives up on a job that goes quiet for the node's `idle_min`, and ends the run at
`ExecuteTimeOut`. So a plugin can decline to wait — report *where the work has
got to*, route a "not yet" port with `CmdNextFilter`, and `Done` in seconds. The
flow's process finishes, the external work does not, and a later run of the same
node reads `_registry["jobId"]`, finds the work still running and reports again
— until the run that finds it finished commits the result and routes the ready
branch. Asynchrony stays inside the plugin, where the knowledge is; the graph
needs no node type for it. See [detached-work.md](detached-work.md).

## Command reference

| Method | Command subject suffix | Payload → | Returns |
|--------|------------------------|-----------|---------|
| `Progress(pct, Frame)`      | `progress`        | `{progress, frame}` | ack |
| `Done(data, key...)`        | `progress`        | `{progress:100, details, commit_on}` | ack |
| `DoneWithError(msg)`        | `progress`        | `{progress:100, error:{message}}` | ack |
| `DoneWithErrorData(msg, data, key...)` | `progress` | `{progress:100, details, commit_on, error:{message}}` | ack |
| `DoneWithErrorCode(code, msg, data, key...)` | `progress` | `{progress:100, details, commit_on, error:{code,message}}` | ack |
| `CmdGetCurrentScope()`      | `context/current` | — | context bytes |
| `CmdGetScope(jsonPath)`     | `context/path`    | `jsonPath` | context bytes |
| `CmdSetOnPath(jsonPath, m)` | `commit`          | `{commit_on, details}` | ack |
| `CmdNextFilter(tags)`       | `next_tags`       | comma-joined tags | ack |
| `CmdSvcCall(action, data, op)` | `request/svc.<action>` | `{data, op}` | service reply bytes |

Every `jsonPath` / `commit_on` above accepts `$this` for the node's own location
— see [`$this`](#this--the-nodes-own-location).

## A complete handler

```go
p.AddAction(sdkv1.Action{Method: "fn", RequestHandler: func(job sdkv1.Job) {
    // read context
    if b, ok := job.CmdGetCurrentScope().([]byte); ok {
        fmt.Println("current:", string(b))
    }
    if b, ok := job.CmdGetScope("$.OPA").([]byte); ok {
        fmt.Println("$.OPA:", string(b))
    }

    // write context
    job.CmdSetOnPath(`$["doc appendix"]`, map[string]any{
        "itemXterm": []uint64{1, 3, 42, 2300},
    })

    // finish
    job.Done(map[string]any{"action": "done finally...."})
}})
```
