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
```

Under the hood all three are a `progress` command at `100`: `Done` sends
`{progress:100, details:data, commit_on:key}`, `DoneWithError` sends
`{progress:100, details:{"error":msg}}`, and `DoneWithErrorData` sends the same
with `data` merged in beside the reason (`error` always wins) and the optional
`commit_on` key. A handler should call exactly one of them before returning.

**Failing does not stop the flow.** All three conclude the node the same way — the
error variants are still a completed job, just one whose committed output is an
error. Any node can fail; the platform treats that as information rather than
flow control, reporting the reason on the event stream and writing it into the
context, then continuing to the next node. That is what makes an error something
a downstream Rule can branch on. If a failure should change where the flow goes,
express that on the canvas, not by trying to halt it from inside the plugin.

Reach for `DoneWithErrorData` when the failure is not the whole story. The details
of a terminal command are what gets committed onto the node's scope, so a bare
`DoneWithError` — reporting only `error` — drops whatever the node had persisted
there. Passing that state back through `data` keeps it readable on the next run,
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

## Command reference

| Method | Command subject suffix | Payload → | Returns |
|--------|------------------------|-----------|---------|
| `Progress(pct, Frame)`      | `progress`        | `{progress, frame}` | ack |
| `Done(data, key...)`        | `progress`        | `{progress:100, details, commit_on}` | ack |
| `DoneWithError(msg)`        | `progress`        | `{progress:100, details:{error}}` | ack |
| `DoneWithErrorData(msg, data, key...)` | `progress` | `{progress:100, details:{...data, error}, commit_on}` | ack |
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
