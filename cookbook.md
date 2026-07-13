# Plugin developer cookbook

A hands-on cookbook for writing an Inflowenger **Plugin node** with `go-plugin-sdk`.
Each section is a self-contained *skill* — a concrete thing you'll need — with the
minimal code that does it. Everything here is grounded in the SDK's real API.

If you want the concepts behind these recipes, read the docs first:
[architecture](docs/architecture.md) · [inflowv1 protocol](docs/protocol-inflowv1.md)
· [jobs & commands](docs/jobs-and-commands.md) · [form builder](docs/form-builder.md)
· [examples](docs/examples.md).

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

// Failure — completes with an error payload
job.DoneWithError("upstream returned 500")
```

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

---

## Skill 11 — Stop the whole flow

From inside a handler you can abort the entire workflow run — use it for guard
conditions that should halt everything downstream, not just fail this node:

```go
if !allowed {
    job.CmdStopFlow()
    return
}
```

---

## Skill 12 — Require settings (onboarding form)

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

> **Note:** live per-field validation via **meta functions** (`SubmitTo` on an
> action form) is defined in the protocol, but there is **no exported method to
> register a meta function yet** — use the settings `SubmitHandler` above, which is
> fully wired today. See [docs/form-builder.md](docs/form-builder.md).

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
