# Jobs & commands

Everything a plugin *does* happens inside a `Job`. When the runtime executes one
of your actions, the SDK acknowledges the request with a fresh `jobId` and hands
your `RequestHandler` a `Job` bound to that id and to the NATS connection. Through
it you report progress, read and write the flow's context, finish the job, or stop
the flow.

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

## Command reference

| Method | Command subject suffix | Payload → | Returns |
|--------|------------------------|-----------|---------|
| `Progress(pct, Frame)`      | `progress`        | `{progress, frame}` | ack |
| `Done(data, key...)`        | `progress`        | `{progress:100, details, commit_on}` | ack |
| `DoneWithError(msg)`        | `progress`        | `{progress:100, details:{error}}` | ack |
| `CmdGetCurrentScope()`      | `context/current` | — | context bytes |
| `CmdGetScope(jsonPath)`     | `context/path`    | `jsonPath` | context bytes |
| `CmdSetOnPath(jsonPath, m)` | `commit`          | `{commit_on, details}` | ack |
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
