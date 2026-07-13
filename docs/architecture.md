# Architecture — the Plugin node in Inflowenger

This document places the Plugin node in the wider Inflowenger runtime and
describes a plugin's lifecycle. For the exact messages on the wire, see
[protocol-inflowv1.md](protocol-inflowv1.md).

## The Inflowenger computational model

Inflowenger is a **runtime for context processing**. Its model has four parts:

| Concept       | Role                                                                 |
|---------------|----------------------------------------------------------------------|
| **Context**   | The memory. Everything enters the system as context; a running flow reads and writes a shared context tree. |
| **Workflows** | The logic. Business logic is expressed as a **workflow graph** — visible, traceable, and safe to change. |
| **Fractals**  | The processors. The runtime instances that actually execute workflow graphs. |
| **Adapters**  | The edges. They connect the runtime to the outside world. |

By analogy to a computer: a **Fractal** is the process/OS, a **Workflow** is a
program, **Context** is memory, and **Plugins** are the extensions, drivers, and
interrupts.

A workflow is a graph of **nodes**. Inflowenger provides a small set of
**primitive nodes**; higher-level node types are compiled down to those
primitives. The **Plugin node** is different — it is not compiled away. It is a
live external process the Fractal calls into at run time, which is what makes it
the ecosystem's richest and most extensible node type.

## Where a plugin runs

A plugin is **your** process. It does not run inside the Fractal; it connects to
the platform's NATS server (exposed by Infra) and subscribes to a set of subjects
namespaced under its `PLUGIN_ID`. The Fractal, when it reaches your node in a
flow, publishes a request to one of those subjects and consumes the responses.

```
                 workflow graph (on the canvas)
                          │
                   ┌──────▼───────┐
                   │  Fractal     │   executes the graph
                   │  (runtime)   │
                   └──────┬───────┘
                          │  NATS  (subjects: inflow.v1.* / inflow.cpu.*)
        ┌─────────────────▼──────────────────┐
        │  Infra  (embedded NATS + accounts)  │
        └─────────────────┬──────────────────┘
                          │  NATS
                   ┌──────▼───────┐
                   │ Your Plugin  │   this SDK — an ordinary Go process
                   │  process     │   holds connections, runs loops, calls APIs
                   └──────────────┘
```

Because the plugin is a persistent process rather than a compiled node, it can:

- **hold long-running state** — open DB/queue/socket connections and keep them warm;
- **originate events** — run background loops that surface external happenings as
  node activity;
- **be an adapter** — translate any external protocol into Inflowenger context;
- **scale independently** — deploy and version on your own cadence, separate from
  the platform.

## Lifecycle of a plugin process

1. **Construct.** `sdkv1.NewPlugin(opts...)` loads credentials and opens the NATS
   connection (see [`WithDotEnv` / `WithInfraConnection`](../sdkv1/plugin.go)).
2. **Declare identity.** `p.Intro(PluginIntro{...})` sets the name/author/version
   the platform shows for this plugin.
3. **Declare requirements (optional).** `p.RequiredParams(&Settings{...})`
   registers an onboarding/settings form and a submit handler — configuration the
   plugin needs before it can act.
4. **Declare actions.** `p.AddAction(Action{...})` adds one or more methods the
   node can perform, each with its own form and `RequestHandler`.
5. **Start.** `p.Start()` subscribes to every subject: intro, settings, the action
   list, each action's form, and each action's executor. It returns immediately.
6. **Block & serve.** `select {}` keeps the process alive. From here on it is
   request-driven: the runtime asks for metadata (intro/forms/actions) and
   dispatches executions.

```go
p, _ := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow"))
p.Intro(sdkv1.PluginIntro{Name: "HTTP.CALL", Author: "inflow Dev. Team", Version: "v0.0.1"})
p.AddAction(sdkv1.Action{Method: "http.call", RequestHandler: handler})
p.Start()
select {}
```

## Two kinds of interaction

The runtime talks to a plugin in two registers, and the **subject naming tells you
which is which**:

- **UI & arguments (discovery).** "What are you? What can you do? What does this
  action's form look like?" These are request/reply lookups on `inflow.v1.*`, and
  every one carries an **`@`-prefixed** segment (`@intro`, `@settings`,
  `@actions`, `@form`). The `@` marks it as UI/arguments metadata — answered from
  the values you declared, nothing executes.
- **Execution (the `cpu` call).** "Run action X with this input." This is the
  node's **main call**, requested by the Fractal at runtime on `inflow.cpu.*`. It
  starts a **Job**: the plugin immediately acknowledges with a `jobId`, then works
  asynchronously — streaming `progress`, reading/writing context, and finally
  `done` — all on `inflow.cpu.*` subjects keyed by that `jobId`.

Rule of thumb: a subject with a `@` part is *describe/configure me*; a subject
under `cpu` is *run me*.

The execution register is where the plugin's power lives: progress feedback,
context injection, and flow control all happen through the `Job`. See
[jobs-and-commands.md](jobs-and-commands.md).
