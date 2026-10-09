# Plugin developer cookbook

A hands-on cookbook for writing an Inflowenger **Plugin node** with `go-plugin-sdk`.
Each section is a self-contained *skill* — a concrete thing you'll need — with the
minimal code that does it. Everything here is grounded in the SDK's real API.

If you want the concepts behind these recipes, read the docs first:
[architecture](docs/architecture.md) · [inflowv1 protocol](docs/protocol-inflowv1.md)
· [jobs & commands](docs/jobs-and-commands.md) · [form builder](docs/form-builder.md)
· [external job identity](docs/external-job-identity.md) ·
[detached work](docs/detached-work.md) · [examples](docs/examples.md).

Also worth keeping open: the **[plugin catalog](https://github.com/Inflowenger/plugin-catalog)** — the developer
knowledge base ([concepts](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/concepts.md) ·
[build a plugin](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/build-a-plugin.md) ·
[run a plugin](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/run-a-plugin.md) ·
[dependent fields](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/dependent-fields.md) ·
[SDK matrix](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/sdks.md) ·
[publishing](https://github.com/Inflowenger/plugin-catalog/blob/main/docs/publishing.md)) and
[`plugins/`](https://github.com/Inflowenger/plugin-catalog/tree/main/plugins), an entry per shipped plugin pointing at its
real source — the best worked examples there are.

> **Using an AI coding agent?** This repo ships a companion **Agent Skill** at
> [`skills/inflow-plugin/SKILL.md`](skills/inflow-plugin/SKILL.md) — a `SKILL.md`
> (frontmatter + agent-directed rules) distilling this guide for a code agent.
> Since `go-plugin-sdk` is imported as a **library**, drop it into *your* plugin
> project so an agent auto-loads it there: copy this folder to
> `.claude/skills/inflow-plugin/` in the repo where you're building the plugin.

---

## Skill 0 — Set up & provision

Before any code, the plugin must exist **in a space** (a NATS account managed by
Infra) so it has an identity and credentials. See
[README → provisioning](README.md#where-these-values-come-from--provisioning-a-plugin).
Infra hands you three values; put them in a dotenv file:

```env
# .env.inflow
PLUGIN_ID=aa-bbb-ccc-dddd
INFRA_CRED=LS0tLS1CRUdJTiBOQVRTIFVTRVIgSldULS0t...   # base64 of the .creds blob
INFRA_URL=localhost:4222
```

Add the dependency:

```bash
go get github.com/Inflowenger/go-plugin-sdk@latest
```

> **Checklist:** the plugin is registered in a space · you have `PLUGIN_ID` ·
> `INFRA_CRED` (base64) · `INFRA_URL` · Infra + at least one Fractal are running.

---

## Skill 1 — Scaffold a runnable plugin (`main.go`)

A plugin is an ordinary long-running program. The `sdkv1_test.go` samples run as
tests only for convenience; your real plugin is a `main` package. This is the whole
skeleton — construct, declare, `Start()`, then **block**:

```go
package main

import (
    "log"

    "github.com/Inflowenger/go-plugin-sdk/sdkv1"
)

func main() {
    p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
    if err != nil {
        log.Fatal(err)
    }

    p.Intro(sdkv1.PluginIntro{
        Name:    "HTTP.CALL",
        Author:  "you",
        Version: "v0.0.1",
    })

    p.AddAction(sdkv1.Action{
        Method: "http.call",
        Title:  "HTTP Call",
        RequestHandler: func(job sdkv1.Job) {
            job.Done(map[string]any{"ok": true})
        },
    })

    if err := p.Start(); err != nil {   // subscribes to all subjects, returns immediately
        log.Fatal(err)
    }
    select {}                            // keep the process alive to serve requests
}
```

```bash
go run .
```

> **Gotcha:** `Start()` returns right away — it only wires up subscriptions. Without
> the trailing `select {}` (or any other block) `main` exits and the plugin dies.

Three ways to construct, pick one:

```go
// From dotenv (reads PLUGIN_ID / INFRA_CRED / INFRA_URL from the file+env)
p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))

// Explicit — note you need BOTH the connection and the id
p, err := sdkv1.NewPlugin(
    sdkv1.WithInfraConnection("localhost:4222", base64Cred),
    sdkv1.WithPluginId("aa-bbb-ccc-dddd"),
)
```

---

## Skill 2 — Declare who you are (`Intro`)

`Intro` is the identity the platform shows for your plugin. Set it once before
`Start()`:

```go
p.Intro(sdkv1.PluginIntro{
    Name:    "HTTP.CALL",
    Author:  "inflow Dev. Team",
    Version: "v0.0.1",
})
```

---

## Skill 3 — Add an action

An **action** is one method your node can perform. A plugin can expose many; call
`AddAction` per action (it's variadic, so you can pass several at once):

```go
p.AddAction(sdkv1.Action{
    Method:         "http.call",              // the method id used on the wire
    Title:          "HTTP Call",              // shown to users
    Description:    "Perform an outbound HTTP request",
    Icon:           sdkv1.Icon{Icon: "mdi-web"},
    Form:           sdkv1.FormBuilder{Jsonschema: schema, Jsonui: ui}, // Skill 8
    RequestHandler: myHandler,                // the work (Skill 4+)
})
```

Every action needs a unique `Method` and a `RequestHandler`. `Form` is optional but
almost always wanted so users can configure the node.

---

## Skill 4 — Read the request (typed input)

Your handler receives a `Job`. The raw body is `job.Req.Data`; decode it with the
generic `CastRequestTo`, which unwraps the `{ "_registry", "body" }` envelope into
your own struct:

```go
type Input struct {
    Url     string            `json:"url"`
    Method  string            `json:"method"`
    Headers map[string]string `json:"headers"`
    Body    map[string]any    `json:"body"`
}

func myHandler(job sdkv1.Job) {
    req, err := sdkv1.CastRequestTo[Input](job.Req.Data)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }

    // req.Body     -> Input          (the user's form input)
    // req.Registry -> map[string]any (runtime metadata; see Skill 5)
    _ = req.Body.Url
}
```

The field names in your struct's JSON tags must match your action's form
(`Jsonschema`) — the form defines the shape that arrives in `body`.

---

## Skill 5 — Use previous-run metadata (`_registry`)

The `_registry` carries metadata the runtime attaches, including this node's
**previous** run — useful for idempotency, dedup, or resume:

```go
if prevJobId, ok := req.Registry["jobId"]; ok {
    doneAt := time.Unix(int64(req.Registry["doneAt"].(float64)), 0)
    fmt.Printf("previous run %s finished at %v\n", prevJobId, doneAt)
}
```

> **Gotcha:** JSON numbers decode as `float64`. Convert (`int64(v.(float64))`)
> before using them as timestamps/ints, and guard the type assertions.

---

## Skill 6 — Report progress

Stream progress `0–100` with a titled status `Frame`. Progress is advisory feedback
shown on the canvas; it does **not** finish the job:

```go
job.Progress(10, sdkv1.Frame{Title: "init step", Content: "starting"})
job.Progress(50, sdkv1.Frame{Title: "working", Content: "calling upstream"})
job.Progress(80, sdkv1.Frame{Title: "almost done"})
```

---

## Skill 7 — Finish (success or error)

Exactly one of these must run before your handler returns. Both drive progress to
100 and terminate the job:

```go
// Success — `data` becomes this node's output
job.Done(map[string]any{"status": "ok", "result": result})

// Success, committing on an explicit key path (segments joined by ".")
job.Done(payload, "result", "http")

// Failure — completes as failed, reporting the reason
job.DoneWithError("upstream returned 500")

// Failure that still has data to report/commit
job.DoneWithErrorData("upstream returned 500", map[string]any{"messages": conversation})

// Failure carrying the plugin's own error number too
job.DoneWithErrorCode(429, "upstream rate limited", nil)
```

The reason travels on the command's own `error` field (`{code, message}`), not as a
detail — so `details` are yours alone, and a bare `DoneWithError` commits nothing.
`code` is the plugin's own numbering; the core carries it without interpreting it,
so pass `0` when the plugin has none.

> **Pattern:** on every error branch, `job.DoneWithError(...)` **and `return`**, so
> the job always terminates once and only once.

---

## Skill 8 — Give the action a UI form

Forms are **JSON Schema** (the data model + validation) plus a **UI Schema**
(layout), rendered by JSON Forms. What the user fills in becomes the `body` of the
request (Skill 4):

```go
schema := `{
  "type": "object",
  "properties": {
    "url":    { "type": "string", "title": "URL", "format": "uri" },
    "method": { "type": "string", "enum": ["GET","POST","PUT","DELETE"] }
  },
  "required": ["url", "method"]
}`

ui := `{
  "type": "VerticalLayout",
  "elements": [
    { "type": "Control", "scope": "#/properties/url" },
    { "type": "Control", "scope": "#/properties/method" }
  ]
}`

p.AddAction(sdkv1.Action{
    Method:         "http.call",
    Form:           sdkv1.FormBuilder{Jsonschema: schema, Jsonui: ui},
    RequestHandler: myHandler,
})
```

Keep the schema and your input struct (Skill 4) in sync. More in
[docs/form-builder.md](docs/form-builder.md).

---

## Skill 9 — Read the flow's context

A running flow has a shared **context** tree. Read all of it, or a slice by JSON
path. Both return `any` — the reply bytes on success, or an `error` value — so
type-assert to `[]byte`:

```go
// whole current scope
if b, ok := job.CmdGetCurrentScope().([]byte); ok {
    fmt.Println("current:", string(b))
}

// a slice addressed by JSON path
if b, ok := job.CmdGetScope("$.OPA").([]byte); ok {
    fmt.Println("$.OPA:", string(b))
}

// `$this` is the location this run was handed — the slice the node's `scope`
// selected. With scope `$.tickets[*]` the node runs once per ticket and each
// run's `$this` is its own ticket, so the plugin never hardcodes an index.
if b, ok := job.CmdGetScope("$this.customer.id").([]byte); ok {
    fmt.Println("customer:", string(b))
}
```

---

## Skill 10 — Write into the flow's context (inject results)

Commit data back into the context at a JSON path so **downstream nodes** can read
it:

```go
job.CmdSetOnPath(`$["doc appendix"]`, map[string]any{
    "itemXterm": []uint64{1, 3, 42, 2300},
})
```

This is separate from `job.Done(...)` output: `CmdSetOnPath` writes into shared
context mid-run; `Done` emits the node's own result.

The path may start at `$this` to write relative to the node's own location —
`job.CmdSetOnPath("$this.verdict", …)` lands inside whichever slice this run was
handed, so the same plugin works wherever the designer points its `scope`.

---

## Skill 11 — Require settings (onboarding form)

For config the plugin needs before any action runs (credentials, a base URL),
register a settings form plus a submit handler:

```go
p.RequiredParams(&sdkv1.Settings{
    FormBuilder: sdkv1.FormBuilder{
        Jsonschema: settingsSchema,
        Jsonui:     settingsUi,
        // SubmitTo defaults to "_settings.config.submit" if left blank
    },
    SubmitHandler: func(r sdkv1.Request) sdkv1.Response {
        // validate / persist r.Data; return feedback
        return sdkv1.Response{Data: map[string]any{"ok": true}}
    },
})
```

> **Note:** the submit handler is a **validator, not a store** — the platform owns
> the values. For helpers a form calls while it is still being filled in
> (connection tests, lookups, fields that depend on another field), register
> **meta functions** with `p.AddMeta(...)` before `Start()`. See
> [docs/form-builder.md](docs/form-builder.md).

---

## Skill 12 — React when a process ends (signals, optional)

The runtime broadcasts on `inflow.plugin.<PLUGIN_ID>.proc` every time a plugin
node process ends — with the `jobId` and a conclusion (`done`, `flow_stop_by_user`,
`timeout`, …). `p.OnSignal` subscribes to that port; call it **before `Start()`**.

```go
p.OnSignal(func(sig sdkv1.Signal) {
    log.Printf("job %s ended: %s", sig.JobId, sig.Conclusion)
})
```

**Skip this skill unless you need it.** A stopped or timed-out process does *not*
stop the job you accepted, by design: the next run of that node may build on the
progress this one made — the runtime hands the previous `jobId` back in
`_registry`. Only reach for `OnSignal` when the work itself must die with the
process: an open stream, a paid upstream call, a held lock.

The working pattern is to file the cancel under the `jobId` and let the signal
find it. Package `jobstop` is that, as a capability you add to the actions that
need it — a middleware on the action, a signal handler on the port:

```go
var stops jobstop.Registry       // one per plugin; zero value is ready
p.OnSignal(stops.OnSignal)       // before Start()

p.AddAction(sdkv1.Action{
    Method: "long.export",
    Middleware: sdkv1.Use(stops.Middleware), // only on actions that should stop with the flow
    RequestHandler: func(job sdkv1.Job) {
        ctx := job.Context()     // cancelled when the flow is stopped
        // ... work that honours ctx ...
        if ctx.Err() != nil {
            return // stopped: the runtime is gone, do not Done
        }
        job.Done(map[string]any{"ok": true})
    },
})
```

Middleware runs before the runtime is told the jobId, so a stop can never arrive
for a job not yet filed. A middleware is a plain function —
`func(ctx context.Context, job sdkv1.Job) (context.Context, error)` — so your own
capabilities (a long-running job kept in your map, a trace) are middleware too,
listed in order: `sdkv1.Use(trace, stops.Middleware)` on an action,
`p.Use(trace)` on every action, `sdkv1.ChainSignals(stops.OnSignal, yours)` on
the port. An error from one rejects the request.

Gotchas:

- Cancellation is **per `jobId`**. One subject carries every signal of the
  plugin, so a process hears the endings of other flows' jobs (and other
  replicas'); those find nothing filed and do nothing.
- Signals arrive on **success too** — `stops.OnSignal` filters on
  `sig.Conclusion.Canceled()`; a handler of your own should too.
- When a stop lands the runtime has already stopped listening to that job, so a
  stopped handler's `Progress`/`Done` will find no responder. Wind down quietly.
- Handlers run on their own goroutine; only the last one registered is kept —
  compose several with `sdkv1.ChainSignals`.
- Registering your own handler replaces the logging `OnSignal(nil)` gives you.
  Chain `sdkv1.LogSignals("<plugin>")` to keep a line per signal that arrives:
  `p.OnSignal(sdkv1.ChainSignals(sdkv1.LogSignals("my-plugin"), stops.OnSignal))`.
  `jobstop` logs the other half — the job it actually cancelled — so a signal
  with no cancel line beside it was not about work this process is running.

Full treatment: [docs/jobs-and-commands.md § Signals](docs/jobs-and-commands.md#signals--when-the-runtime-ends-a-process).

---

## Skill 13 — Run the job under an external service's id (advanced)

When your plugin is a **middleman** for a service that names work itself — Joern's
HTTP server answering `POST /query` with `{"queryId":"q-8f21"}`, a render farm, a
scan — do not keep a `map[pluginJobId]upstreamId`. Register the work in a
middleware function and bind what came back as the job's id: middleware runs
**before** the job is accepted, so the id you bind is the id the runtime is told.

```go
func registerQuery(ctx context.Context, job sdkv1.Job) (context.Context, error) {
    in, err := sdkv1.CastRequestTo[QueryInput](job.Req.Data)
    if err != nil {
        return nil, err // rejects the request: the node never ran
    }
    // Re-running? The previous jobId IS the upstream id — reattach, don't duplicate.
    if prev, ok := in.Registry["jobId"].(string); ok && joern.Alive(ctx, prev) {
        return sdkv1.WithJobIDContext(ctx, prev), nil
    }
    queryId, err := joern.Register(ctx, in.Body.Project, in.Body.Query)
    if err != nil {
        return nil, err // nothing was enlisted upstream: nothing to undo
    }
    return sdkv1.WithJobIDContext(ctx, queryId), nil
}

p.OnSignal(sdkv1.ChainSignals(stops.OnSignal, abortUpstream))
p.AddAction(sdkv1.Action{
    Method:         "cpg.query",
    Middleware:     sdkv1.Use(registerQuery, stops.Middleware), // namer FIRST
    RequestHandler: queryHandler,
})
```

From then on one name serves both systems: `job.JobId`, every command subject
(`inflow.cpu.<id>.q-8f21.progress`), the stop signal's `jobId`, and the next
run's `_registry["jobId"]`. Cancellation needs no local lookup at all —

```go
func abortUpstream(sig sdkv1.Signal) {
    if sig.Kind != sdkv1.RuntimeProcessSignal || !sig.Conclusion.Canceled() {
        return
    }
    joern.Cancel(context.Background(), sig.JobId) // sig.JobId IS the queryId
}
```

— so any replica that hears the stop can abort the query, which a process-local
map could never do.

Gotchas:

- **Name the job first.** `stops.Middleware` (and anything else keyed on the
  jobId) files under `job.JobId` *as of when it runs*; placed before the namer it
  files a uuid nothing will look up, and the stop is lost.
- **Validate the id you adopt.** It must be **at least 10 characters** —
  fractal-core refuses a shorter `jobId` with `init failed. invalid job ID` — a
  usable NATS subject token (no `.`, space, `*`, `>`, since the command subjects
  are built from it), and unique plugin-wide (prefix per-project counters).
- **Register fast.** The runtime is waiting for the jobId while middleware runs —
  one timeout-bounded call, never the work itself.
- **Reject vs fail**: a middleware error means the node never ran (the runtime
  gets the error, not a failed job). When the flow should *see* a failed node,
  accept and use `job.DoneWithError`.
- If the service accepts a **client-supplied** id instead, do the mirror image:
  keep the SDK's uuid and send `job.JobId` upstream.

Full treatment, with the distributed-transaction model and the failure modes:
[docs/external-job-identity.md](docs/external-job-identity.md).

---

## Skill 14 — Report and observe instead of waiting (advanced)

Work that takes hours does not fit in a job. Three budgets say so: the runtime
waits **15s** for your `jobId`, gives up on a job that sends no command for the
node's `idle_min`, and ends the run at `ExecuteTimeOut`. So do not wait — **report
where the work has got to, route a "not yet" port, and end the job in seconds.**
The flow's process finishes; the external work doesn't; a later run of the same
node picks it up through `_registry`.

```go
// Accept stage. Only two inputs exist here: body, and _registry — the node's
// memory of its own previous run (job commands need a jobId, which this decides).
func attachOrStart(ctx context.Context, job sdkv1.Job) (context.Context, error) {
    in, err := sdkv1.CastRequestTo[QueryInput](job.Req.Data)
    if err != nil {
        return nil, err
    }
    if prev, ok := in.Registry["jobId"].(string); ok && prev != "" && joern.Has(ctx, prev) {
        return sdkv1.WithJobIDContext(ctx, prev), nil // observe what the last run started
    }
    queryId, err := joern.Register(ctx, in.Body.Project, in.Body.Query) // start new work
    if err != nil {
        return nil, err
    }
    return sdkv1.WithJobIDContext(ctx, queryId), nil
}

func observe(job sdkv1.Job) {
    status, err := joern.Poll(job.Context(), job.JobId) // job.JobId IS the upstream id
    switch {
    case err != nil:
        job.DoneWithError(err.Error())
    case status.Failed:
        job.CmdNextFilter([]string{"_exception"}) // fail AND route
        job.DoneWithErrorData(status.Error, map[string]any{"queryId": job.JobId}, "joern")
    case !status.Done:
        job.CmdNextFilter([]string{"pending"})    // a SUCCESSFUL "not yet"
        job.Done(map[string]any{"state": "running", "percent": status.Percent}, "joern")
    default:
        joern.Release(context.WithoutCancel(job.Context()), job.JobId)
        job.CmdNextFilter([]string{"ready"})
        job.Done(map[string]any{"state": "done", "result": status.Result}, "joern")
    }
}
```

Declare the ports so the canvas shows them before anything runs:

```go
Outbound: []sdkv1.OutboundPort{
    {Title: "Still running", Tags: []string{"pending"}},
    {Title: "Result ready",  Tags: []string{"ready"}},
    {Title: "Query failed",  Tags: []string{"_exception"}},
},
```

Then the flow closes the loop: the `pending` branch ends in a delay/Continue
After node, a schedule re-runs it, or a loop edge returns to the node — all of
which must re-enter **over the same context document**, because `_registry` is
the node's entry in that document.

Gotchas:

- **"Still running" is `Done`, not an error.** The job did look; the answer is
  "not yet". `DoneWithError` there would route the flow's error branch for a
  perfectly healthy query.
- **Commit, don't just report.** `job.Done(data, "joern")` commits at that key —
  a bare `Done` with no key commits nothing, so the next run (and the next node)
  sees an empty scope.
- **`_registry` is per call site and lives in the context document.** Two `GoTo`s
  onto the same sub-flow keep separate memories; a run over a *new* context
  starts fresh and will correctly start new upstream work.
- **`doneAt` / `conclusion` describe the run, not the work.** A "pending" run
  ends `done`. Only the service knows the work's state. `reqAt` is the useful
  one — it dates the handle, so you can give up on a stale one.
- **No `stops.Middleware` on an observer action.** Outliving the flow is the
  point; a stop should not abort the upstream work unless an abandoned job
  genuinely costs you (then abort it from the signal handler by `sig.JobId`).
- **Handle the stale handle.** If the service has forgotten the id, start fresh
  rather than reporting `pending` forever.
- **Give the loop a floor** — an attempt counter in a contract, or a `reqAt` age
  limit in the plugin.

Full treatment, with the three budgets, the registry fields and the limits:
[docs/detached-work.md](docs/detached-work.md).

---

## Recipe A — An adapter action (external I/O)

The canonical shape: typed input → progress → external work → shaped output. (This
is the `HTTP.CALL` sample, condensed.)

```go
p.AddAction(sdkv1.Action{Method: "http.call", RequestHandler: func(job sdkv1.Job) {
    req, err := sdkv1.CastRequestTo[Input](job.Req.Data)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }

    job.Progress(20, sdkv1.Frame{Title: "working", Content: req.Body.Url})

    body, _ := sonic.Marshal(req.Body.Body)
    hreq, err := http.NewRequest(req.Body.Method, req.Body.Url, bytes.NewReader(body))
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }
    hreq.Header.Set("Content-Type", "application/json")
    for k, v := range req.Body.Headers {
        hreq.Header.Add(k, v)
    }

    resp, err := (&http.Client{}).Do(hreq)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }
    defer resp.Body.Close()
    raw, _ := io.ReadAll(resp.Body)

    out := map[string]any{}
    if json.Unmarshal(raw, &out) != nil {
        out["rawBody"] = string(raw) // fall back to raw if not JSON
    }
    job.Done(out)
}})
```

## Recipe B — A pure context/transform action (no I/O)

The minimal functional node — read context, compute, write, done. (This is the
`RPC` sample.)

```go
p.AddAction(sdkv1.Action{Method: "fn", RequestHandler: func(job sdkv1.Job) {
    if b, ok := job.CmdGetScope("$.OPA").([]byte); ok {
        fmt.Println("$.OPA:", string(b))
    }
    job.CmdSetOnPath(`$["result"]`, map[string]any{"computed": 42})
    job.Done(map[string]any{"action": "done"})
}})
```

## Recipe C — A long-running / event plugin

Because the plugin is a persistent process, an action can kick off background work,
or the plugin can hold connections and run loops between requests. Keep any shared
state on your own types and guard it; each `RequestHandler` runs per invocation.
This is the plugin shape most likely to want [Skill 12](#skill-12--react-when-a-process-ends-signals-optional):
background work that should be torn down when the process that started it is
stopped.

```go
func main() {
    p, _ := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
    p.Intro(sdkv1.PluginIntro{Name: "QUEUE.WATCH", Author: "you", Version: "v0.0.1"})

    // e.g. open a DB/queue connection once, reuse across handlers
    // conn := mustConnect()

    p.AddAction(sdkv1.Action{Method: "enqueue", RequestHandler: func(job sdkv1.Job) {
        // use conn ...
        job.Done(map[string]any{"queued": true})
    }})

    _ = p.Start()
    select {}
}
```

---

## Run & iterate locally

1. Point `.env.inflow` at your running Infra (`PLUGIN_ID` / `INFRA_CRED` / `INFRA_URL`).
2. `go run .` (or copy a handler into a test and `go test -run TestInit -v`).
3. On startup the SDK logs each subscribed subject (form/action/job) — that
   confirms the plugin registered.
4. Add your node to a flow in the inspector panel, run the flow, watch progress
   frames and output appear.

---

## Ship checklist

- [ ] Plugin is defined in a space; `PLUGIN_ID` / `INFRA_CRED` / `INFRA_URL` set.
- [ ] `main` blocks after `Start()` (`select {}`).
- [ ] Every action has a unique `Method` and a `RequestHandler`.
- [ ] Every handler ends in exactly one `Done` / `DoneWithError` on all paths.
- [ ] Each action's `Jsonschema` matches its input struct's JSON tags.
- [ ] Long-running/shared state is concurrency-safe.
- [ ] Errors are surfaced via `DoneWithError`, not just logged.
