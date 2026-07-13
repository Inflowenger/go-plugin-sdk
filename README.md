# Inflowenger Go Plugin SDK (`inflowv1`)

> The official Go SDK for building **Plugin nodes** in the [Inflowenger](https://github.com/Inflowenger/getting-started) ecosystem.

`go-plugin-sdk` lets you write a small Go program that shows up on the Inflowenger
workflow canvas as a **node** — with its own UI form, its own actions, live
progress feedback, and full read/write access to the flow's context — while
running as an ordinary external process that you own and deploy anywhere.

It speaks **`inflowv1`**, the message protocol the runtime (Fractal) uses to talk
to plugins over [NATS](https://nats.io). This repository is the reference **v1**
implementation of that protocol.

```go
p, _ := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))

p.Intro(sdkv1.PluginIntro{Name: "HTTP.CALL", Author: "inflow Dev. Team", Version: "v0.0.1"})

p.AddAction(sdkv1.Action{Method: "http.call", RequestHandler: func(job sdkv1.Job) {
    req, _ := sdkv1.CastRequestTo[struct {
        Url    string `json:"url"`
        Method string `json:"method"`
    }](job.Req.Data)

    job.Progress(20, sdkv1.Frame{Title: "calling", Content: req.Body.Url})
    // ... do the work ...
    job.Done(map[string]any{"status": "ok"})
}})

p.Start()
select {} // block forever, serving requests
```

---

## Why a Plugin node?

Inflowenger is a runtime for building software whose **logic is defined as a
workflow graph** — think n8n, but as a general computational substrate for large
classes of systems (ERP, CRM, automation platforms, anything where an operator or
super-admin must be able to define and change logic without redeploying the base
system). The platform ships a handful of **primitive/principal nodes**; every
higher-level node ultimately compiles down to those primitives.

The **Plugin node is the exception** — and the most powerful node type. Rather
than compiling to primitives, a plugin is a live external process that the runtime
calls into. That makes it the full-featured extension point of the whole
ecosystem:

- **Context injection** — read and write the running flow's shared context by JSON
  path, mid-execution.
- **Flow control** — report progress, finish a job, or **stop the flow** from
  inside your handler.
- **Long-running work & events** — a plugin is a persistent process, so it can
  hold connections, run background loops, and surface external systems (queues,
  webhooks, hardware, third-party APIs) as nodes on the canvas.
- **Adapters** — implement any adapter to bridge Inflowenger to the outside world.
- **Its own UI** — every action carries a form (JSON Schema + UI Schema, rendered
  by [JSON Forms](https://jsonforms.io) with Inflowenger's `x-inflow-ui`
  extensions) so users configure your node visually.

> In short: with **only** the plugin node type, anyone can build a full workflow
> automation system on top of Inflowenger.

For how the plugin node fits the larger picture (Context · Workflows · Fractals ·
Adapters), see [docs/architecture.md](docs/architecture.md).

---

## Installation

```bash
go get github.com/Inflowenger/go-plugin-sdk@latest
```

```go
import "github.com/Inflowenger/go-plugin-sdk/sdkv1"
```

Requires **Go 1.26+** and a reachable Inflowenger platform (Infra + at least one
Fractal). To stand one up locally, follow the
[getting-started](https://github.com/Inflowenger/getting-started) guide.

---

## Configuration

A plugin needs three things to connect, supplied via a dotenv file (or directly
through functional options):

| Variable     | Meaning                                                                 |
|--------------|-------------------------------------------------------------------------|
| `PLUGIN_ID`  | The plugin's identity. All of its NATS subjects are namespaced under it. |
| `INFRA_CRED` | **Base64-encoded** NATS user credentials (JWT + NKey seed) minted by Infra. |
| `INFRA_URL`  | NATS endpoint of the platform, e.g. `localhost:4222`.                   |

```env
# .env.inflow
PLUGIN_ID=aa-bbb-ccc-dddd
INFRA_CRED=LS0tLS1CRUdJTiBOQVRTIFVTRVIgSldULS0t...   # base64 of the .creds file
INFRA_URL=localhost:4222
```

Three ways to construct a plugin:

```go
// 1. From a dotenv file (reads PLUGIN_ID / INFRA_CRED / INFRA_URL)
p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))

// 2. Explicit connection + id
p, err := sdkv1.NewPlugin(
    sdkv1.WithInfraConnection("localhost:4222", base64Cred),
    sdkv1.WithPluginId("aa-bbb-ccc-dddd"),
)
```

The credential is a standard decorated NATS `.creds` blob, base64-encoded. The SDK
decodes it, reads the account from the JWT, and connects with automatic
reconnect/retry.

### Where these values come from — provisioning a plugin

A plugin can't just connect; it must first be **defined in the system under a
space**. A *space* is a NATS **account** managed by Infra — it's the unit of
authentication, authorization, and isolation. Registering the plugin in a space is
what makes its `PLUGIN_ID` a real, reachable identity and what scopes which
subjects and domains it may touch.

- **Built-in plugins space.** Inflow ships a default account/space for plugins, so
  a single-tenant setup can register a plugin there and be running immediately.
- **Custom spaces (multi-tenant / enterprise).** For multi-tenant systems, define
  the plugin in a **custom NATS account (space)** to isolate accessibility and
  scope its domains — each tenant or trust boundary gets its own account, and a
  plugin's credentials only reach what that account permits.

Once the plugin is defined in a space, Infra gives you the three env values:

1. **`INFRA_CRED`** — the credential minted for that plugin in that space
   (base64-encoded NATS `.creds`). It carries the account, so it *is* the plugin's
   authorization boundary.
2. **`PLUGIN_ID`** — the identity the plugin was registered under; every subject is
   namespaced by it.
3. **`INFRA_URL`** — where to reach the platform's NATS. In enterprise or
   large deployments Infra may run as a **cluster with multiple instance
   endpoints**, so this is always required (it isn't assumed) — point it at your
   cluster's endpoint(s).

---

## The shape of a plugin

A plugin is declared, then started:

```go
p, _ := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))

// 1. Identity — shown on the canvas / node palette
p.Intro(sdkv1.PluginIntro{
    Name:    "HTTP.CALL",
    Author:  "inflow Dev. Team",
    Version: "v0.0.1",
})

// 2. (optional) Onboarding / settings form + submit handler
p.RequiredParams(&sdkv1.Settings{
    FormBuilder:   sdkv1.FormBuilder{Jsonschema: schema, Jsonui: ui},
    SubmitHandler: func(r sdkv1.Request) sdkv1.Response { /* validate */ },
})

// 3. One or more actions — each is a method the node can perform
p.AddAction(sdkv1.Action{
    Method:         "http.call",
    Title:          "HTTP Call",
    Description:    "Perform an outbound HTTP request",
    Form:           sdkv1.FormBuilder{Jsonschema: schema, Jsonui: ui},
    RequestHandler: func(job sdkv1.Job) { /* the work */ },
})

// 4. Start serving and block
p.Start()
select {}
```

`Start()` wires up all the NATS subscriptions (intro, settings, action list, per-action
forms, and per-action executors) and returns. Because the SDK subscribes
asynchronously, your `main` must block afterwards (`select {}`) to keep the process
alive.

---

## Inside an action handler (`Job`)

When the runtime executes your node, it calls your `RequestHandler` with a `Job`.
The job is your handle back into the running flow:

```go
func(job sdkv1.Job) {
    // Parse the request body into your own typed struct.
    // RequestBody wraps { "_registry": {...}, "body": <your type> }
    req, err := sdkv1.CastRequestTo[MyInput](job.Req.Data)
    if err != nil {
        job.DoneWithError(err.Error())
        return
    }

    // The registry carries metadata about this node's *previous* runs.
    if prev, ok := req.Registry["jobId"]; ok {
        fmt.Println("previous run:", prev)
    }

    // Stream progress back to the canvas (0–100).
    job.Progress(20, sdkv1.Frame{Title: "working", Content: "calling upstream"})

    // Read the flow's shared context.
    current := job.CmdGetCurrentScope()          // whole current scope
    opa     := job.CmdGetScope("$.OPA")          // by JSON path

    // Write into the flow's context at a JSON path.
    job.CmdSetOnPath(`$["result"]`, map[string]any{"count": 42})

    // Optionally abort the whole flow.
    // job.CmdStopFlow()

    // Finish. Progress hits 100 and the payload is committed as the node's output.
    job.Done(map[string]any{"ok": true})
}
```

| Method | Effect |
|--------|--------|
| `job.Progress(pct, Frame)` | Report progress `0–100` with a titled status frame. |
| `job.Done(data, key...)`   | Complete the job (progress 100) and emit `data` as output; optional key path to commit on. |
| `job.DoneWithError(msg)`   | Complete with an error payload. |
| `job.CmdGetCurrentScope()` | Fetch the current context scope (raw bytes). |
| `job.CmdGetScope(path)`    | Fetch a slice of context by JSON path (e.g. `$.OPA`). |
| `job.CmdSetOnPath(path, m)`| Commit data into the flow context at a JSON path. |
| `job.CmdStopFlow()`        | Stop the entire workflow run. |

Full details, semantics, and the underlying subjects are in
[docs/jobs-and-commands.md](docs/jobs-and-commands.md).

---

## Documentation

| Doc | What's in it |
|-----|--------------|
| [cookbook.md](cookbook.md) | **Start here to build one** — a task-organized cookbook: scaffold, actions, input, progress, context, forms, recipes, ship checklist. |
| [docs/architecture.md](docs/architecture.md) | Where the plugin node sits in Inflowenger (Context / Workflows / Fractals / Adapters), and the plugin lifecycle. |
| [docs/protocol-inflowv1.md](docs/protocol-inflowv1.md) | The `inflowv1` wire protocol: every NATS subject, request/response shape, and the request↔job handshake. |
| [docs/jobs-and-commands.md](docs/jobs-and-commands.md) | The `Job` API in depth — progress, done, context read/write, stop. |
| [docs/form-builder.md](docs/form-builder.md) | Building action & settings UIs with JSON Forms + `x-inflow-ui`. |
| [docs/examples.md](docs/examples.md) | Annotated walkthrough of the `HTTP.CALL` and `RPC` sample plugins. |
| [docs/inflow-ecosystem.md](docs/inflow-ecosystem.md) | Working notes on the broader Inflowenger platform (seed for the full ecosystem doc). |

---

## Building with an AI assistant (Agent Skill)

If you build your plugin with an AI coding agent (Claude Code / the Claude Agent
SDK, or any tool that supports **Agent Skills** — `SKILL.md` files), this repo ships
a ready-made **skill** that teaches the agent how to use this SDK correctly —
scaffolding, the one-`Done`-per-path rule, `CastRequestTo`, context commands, forms,
and the known gotchas.

It lives at **[`skills/inflow-plugin/SKILL.md`](skills/inflow-plugin/SKILL.md)**: a
`SKILL.md` with frontmatter (a `description` that tells the agent *when* to use it)
plus agent-directed instructions. It's the machine-facing counterpart to the
human [`cookbook.md`](cookbook.md).

**How to use it.** Because `go-plugin-sdk` is imported as a library, you install the
skill in **your own plugin project** (not here) so your agent auto-loads it there:

```bash
# from the root of the repo where you're building your plugin
mkdir -p .claude/skills
# copy the skill folder out of the SDK (adjust the source path to your checkout / module cache)
cp -r "$(go env GOMODCACHE)"/github.com/\!inflowenger/go-plugin-sdk@*/skills/inflow-plugin .claude/skills/
```

Or just grab it from GitHub:
[`skills/inflow-plugin/SKILL.md`](https://github.com/Inflowenger/go-plugin-sdk/tree/main/skills/inflow-plugin).

Once it's under `.claude/skills/inflow-plugin/`, a compatible agent discovers it by
its `description` and pulls it in whenever you ask it to build or extend an inflow
plugin — no need to paste docs into the prompt. (The skill's own links point at the
SDK's GitHub docs, so they keep working after you copy it out.)

---

## Running the samples

Two runnable samples live in [`sdkv1_test.go`](sdkv1_test.go): an `HTTP.CALL`
plugin and an `RPC` plugin. They connect to a live platform using `.env.inflow`,
so point that file at your running Infra first, then:

```bash
go test -run TestInit -v     # HTTP.CALL plugin
go test -run TestCommands -v # RPC plugin
```

Both block on `select {}` and serve until interrupted — they are long-running
plugin processes rather than one-shot tests.

---

## Repository layout

```
go-plugin-sdk/
├── sdkv1/                 the v1 SDK
│   ├── plugin.go          Plugin type, construction, options, NATS Send
│   ├── inflowV1.go        subject wiring: intro / settings / actions / forms
│   ├── job.go             Job: progress, done, context commands
│   ├── req.go             request parsing, CastRequestTo, job handshake
│   ├── models.go          protocol data types (Intro, Action, FormBuilder, ...)
│   ├── types.go           command constants (progress/stop/context/commit)
│   └── dotenv.go          env loading
├── nats/
│   └── natsBox.go         NATS connection from base64 decorated credentials
├── sdkv1_test.go          runnable sample plugins
├── cookbook.md            human-facing build guide
├── docs/                  concept & protocol docs
└── skills/
    └── inflow-plugin/
        └── SKILL.md       Agent Skill — copy into your plugin project's .claude/skills/
```

---

## License

See the repository for license details.
