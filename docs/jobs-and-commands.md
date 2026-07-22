# Jobs & commands

Everything a plugin *does* happens inside a `Job`. When the runtime executes one
of your actions, the SDK acknowledges the request with a fresh `jobId` and hands
your `RequestHandler` a `Job` bound to that id and to the NATS connection. Through
it you report progress, read and write the flow's context, call an extrinsics
service, finish the job, or stop the flow.

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
```

Under the hood both are a `progress` command at `100`: `Done` sends
`{progress:100, details:data, commit_on:key}`, `DoneWithError` sends
`{progress:100, details:{"error":msg}}`. A handler should call exactly one of them
before returning.

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

## Writing to the flow context (context injection)

Commit data back into the flow's context at a JSON path. This is how a plugin
**injects** results other downstream nodes will read:

```go
job.CmdSetOnPath(`$["doc appendix"]`, map[string]any{
    "itemXterm": []uint64{1, 3, 42, 2300},
})
```

The path is a JSON path into the context tree; the map is the value written there.
This is a `commit` command carrying `{commit_on: path, details: data}`.

## Stopping the flow

From inside a handler you can halt the entire workflow run:

```go
job.CmdStopFlow()
```

Use it for guard conditions — a validation failure or a business rule that should
abort everything downstream, not just fail this one node.

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
| `CmdGetCurrentScope()`      | `context/current` | — | context bytes |
| `CmdGetScope(jsonPath)`     | `context/path`    | `jsonPath` | context bytes |
| `CmdSetOnPath(jsonPath, m)` | `commit`          | `{commit_on, details}` | ack |
| `CmdNextFilter(tags)`       | `next_tags`       | comma-joined tags | ack |
| `CmdSvcCall(action, data, op)` | `request/svc.<action>` | `{data, op}` | service reply bytes |
| `CmdStopFlow()`             | `stop`            | — | ack |

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
