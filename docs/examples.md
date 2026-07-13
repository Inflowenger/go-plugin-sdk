# Examples

Two runnable sample plugins live in [`sdkv1_test.go`](../sdkv1_test.go). They are
written as Go tests only so `go test` can launch them; each one connects to a live
platform and blocks on `select {}`, so they behave as long-running plugin
processes rather than assertions.

Point [`.env.inflow`](../.env.inflow) at your running Infra
(`PLUGIN_ID` / `INFRA_CRED` / `INFRA_URL`) first, then run one:

```bash
go test -run TestInit -v      # the HTTP.CALL plugin
go test -run TestCommands -v  # the RPC plugin
```

Stop with `Ctrl-C`.

---

## 1. `HTTP.CALL` — an outbound HTTP adapter (`TestInit`)

A plugin exposing two actions: `http.call` (a real HTTP request driven by the
node's form) and `fn` (a context read/write demo). This is the canonical shape of
an **adapter plugin**: take typed input, do external work, stream progress, commit
a result.

```go
p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
if err != nil {
    panic(err)
}
p.Intro(sdkv1.PluginIntro{Name: "HTTP.CALL", Author: "inflow Dev. Team", Version: "v0.0.1"})

p.AddAction(sdkv1.Action{Method: "http.call", RequestHandler: func(job sdkv1.Job) {
    // 1. Parse the request body into a typed struct.
    recvMsg, err := sdkv1.CastRequestTo[struct {
        Url     string            `json:"url"`
        Method  string            `json:"method"`
        Headers map[string]string `json:"headers"`
        Body    map[string]any    `json:"body"`
    }](job.Req.Data)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }

    // 2. The registry carries this node's previous run (idempotency, resume, ...).
    if prevJobId, ok := recvMsg.Registry["jobId"]; ok {
        fmt.Printf("previous run %s done at %v\n",
            prevJobId, time.Unix(int64(recvMsg.Registry["doneAt"].(float64)), 0))
    }

    // 3. Stream progress to the canvas.
    job.Progress(10, sdkv1.Frame{Title: "init step", Content: "given task is in progress"})
    job.Progress(20, sdkv1.Frame{Title: "working", Content: "task is being process"})

    // 4. Do the actual work — an outbound HTTP request built from the form input.
    bodyByte, _ := sonic.Marshal(recvMsg.Body.Body)
    httpreq, err := http.NewRequest(recvMsg.Body.Method, recvMsg.Body.Url, bytes.NewReader(bodyByte))
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }
    httpreq.Header.Add("Content-type", "application/json")
    for k, v := range recvMsg.Body.Headers {
        httpreq.Header.Add(k, v)
    }

    resp, err := (&http.Client{}).Do(httpreq)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }
    defer resp.Body.Close()
    resBody, _ := io.ReadAll(resp.Body)

    // 5. Shape the output; fall back to a raw body if it isn't JSON.
    doneBody := map[string]any{}
    if err := json.Unmarshal(resBody, &doneBody); err != nil {
        doneBody["rawBody"] = string(resBody)
    }

    job.Progress(80, sdkv1.Frame{Title: "almost done"})

    // 6. Finish — commits doneBody as this node's output.
    job.Done(doneBody)
}})
```

The second action shows context injection:

```go
p.AddAction(sdkv1.Action{Method: "fn", RequestHandler: func(job sdkv1.Job) {
    // read the whole current scope
    if d, ok := job.CmdGetCurrentScope().([]byte); ok {
        fmt.Println("GetCurrent", string(d))
    }
    // read a slice of context by JSON path
    if d, ok := job.CmdGetScope("$.OPA").([]byte); ok {
        fmt.Println("Scope :", string(d))
    }
    // write into context at a JSON path
    job.CmdSetOnPath(`$["doc appendix"]`, map[string]any{"itemXterm": []uint64{1, 3, 42, 2300}})
    // job.CmdStopFlow()  // uncomment to abort the whole flow here

    job.Done(map[string]any{"action": "done finally...."})
}})

p.Start()
select {}
```

Takeaways:

- **One plugin, many actions.** Each `AddAction` is a separately-invokable method
  on the same node/process.
- **Typed input via generics.** `CastRequestTo[T]` unwraps the
  `{ _registry, body }` envelope into your struct.
- **Fail fast.** Any error path calls `job.DoneWithError` and returns, so the job
  always terminates exactly once.
- **Progress is cosmetic; `Done` is terminal.** Only `Done`/`DoneWithError`
  finishes the job.

---

## 2. `RPC` — a pure context function (`TestCommands`)

A minimal plugin whose single `fn` action only reads context and returns — no
external I/O. This is the shape of a **logic/transform node**.

```go
p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
if err != nil {
    panic(err)
}
p.Intro(sdkv1.PluginIntro{Name: "RPC", Author: "inflow Dev. Team", Version: "v0.0.1"})

p.AddAction(sdkv1.Action{Method: "fn", RequestHandler: func(job sdkv1.Job) {
    if d, ok := job.CmdGetCurrentScope().([]byte); ok {
        fmt.Println("GetCurrent", string(d))
    }
    if d, ok := job.CmdGetScope("$.OPA").([]byte); ok {
        fmt.Println("Scope :", string(d))
    }
    // job.CmdStopFlow()
    job.Done(map[string]any{"action": "done"})
}})

p.Start()
select {}
```

Same skeleton, no adapter work — proof of how little a functional node needs:
construct, declare an action, read/write context, `Done`.

---

## Adapting these into your own plugin

1. Copy one handler into a `main` package (not a test).
2. Give the plugin a real `PLUGIN_ID` and point `INFRA_CRED` / `INFRA_URL` at your
   platform.
3. Add a `Form` (JSON Schema + UI Schema) to each action so users can configure it
   — see [form-builder.md](form-builder.md).
4. `p.Start()` then block with `select {}`.
