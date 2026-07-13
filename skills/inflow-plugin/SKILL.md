---
name: inflow-plugin
description: Build an Inflowenger Plugin node with the Go go-plugin-sdk (sdkv1). Use when the user asks to create, scaffold, or extend an inflow/Inflowenger plugin — adding an action, parsing request input, reporting progress, reading/writing flow context, building the action's UI form, or wiring settings. Not for extrinsic nodes (those belong to inflow-fusion).
---

# Building an Inflowenger Plugin node

Instructions for writing a plugin with `github.com/Inflowenger/go-plugin-sdk`
(`sdkv1`), imported as a **library**. A plugin is a long-running Go process that
appears as a node on the Inflowenger workflow canvas and is called by the Fractal
runtime over NATS.

Fuller reference lives in the SDK's GitHub repo (this skill is meant to be copied
into a consuming plugin project, so links point there rather than at local paths):
the human cookbook at
[`cookbook.md`](https://github.com/Inflowenger/go-plugin-sdk/blob/main/cookbook.md) and
concept docs under
[`docs/`](https://github.com/Inflowenger/go-plugin-sdk/tree/main/docs). Read those
for detail — this file is the operational checklist. Verify the current API against
the installed `go-plugin-sdk/sdkv1` package before relying on any signature; do not
invent methods.

## When to use

Use this when the task involves creating or modifying an inflow **plugin** node:
scaffolding a plugin, adding/editing an `Action`, decoding request bodies, progress
reporting, flow-context read/write, stopping a flow, or action/settings forms.

Do **not** use this for **extrinsic** nodes (internal service calls) — those are
registered via `inflow-fusion`, a different repo, and are out of scope here.

## The non-negotiable rules (get these right)

1. **`main` must block after `Start()`.** `p.Start()` only wires NATS subscriptions
   and returns immediately. End `main` with `select {}` (or equivalent) or the
   process exits and the plugin dies.
2. **Every handler ends in exactly one `job.Done(...)` or `job.DoneWithError(...)`
   on every path.** On each error branch call `job.DoneWithError(err.Error())` **and
   `return`**. Never finish twice, never finish zero times.
3. **Decode input with `sdkv1.CastRequestTo[T](job.Req.Data)`**, which unwraps the
   `{ "_registry", "body" }` envelope → `req.Body` (type `T`) + `req.Registry`
   (`map[string]any`). JSON numbers arrive as `float64`; convert before use.
4. **Keep each action's `Jsonschema` in sync with its input struct's JSON tags** —
   the form defines the shape delivered as `body`.
5. **Provisioning is a prerequisite, not code.** The plugin must be defined in a
   space (a NATS account in Infra) to get `PLUGIN_ID`, `INFRA_CRED` (base64), and
   `INFRA_URL`. If these are missing, tell the user to provision first; don't
   fabricate credentials.

## Procedure

1. **Confirm prerequisites**: `PLUGIN_ID`, `INFRA_CRED`, `INFRA_URL` (usually in a
   dotenv like `.env.inflow`), and that Infra + a Fractal are running. Add the dep
   with `go get github.com/Inflowenger/go-plugin-sdk@latest`.
2. **Scaffold `main`** (a `main` package, not a test):
   ```go
   p, err := sdkv1.NewPlugin(sdkv1.WithDotEnv(".env.inflow")) // or WithInfraConnection + WithPluginId
   // handle err
   p.Intro(sdkv1.PluginIntro{Name: "MY.PLUGIN", Author: "…", Version: "v0.0.1"})
   p.AddAction(sdkv1.Action{Method: "do.thing", Title: "…", Form: form, RequestHandler: handler})
   if err := p.Start(); err != nil { /* handle */ }
   select {}
   ```
3. **Write each `RequestHandler(job sdkv1.Job)`** using only these verified `Job`
   operations:
   - `sdkv1.CastRequestTo[T](job.Req.Data)` — typed input (rule 3).
   - `job.Progress(pct, sdkv1.Frame{Title, Content})` — advisory, 0–100; does not finish.
   - `job.Done(map[string]any, key ...string)` — success + output (finishes).
   - `job.DoneWithError(string)` — failure (finishes).
   - `job.CmdGetCurrentScope()` / `job.CmdGetScope("$.path")` — read context; both
     return `any`, type-assert to `[]byte`.
   - `job.CmdSetOnPath("$.path", map[string]any{...})` — write into flow context.
   - `job.CmdStopFlow()` — abort the whole flow.
4. **Add forms** when the node needs configuration:
   `sdkv1.FormBuilder{Jsonschema: <JSON Schema>, Jsonui: <UI Schema>}` (JSON Forms).
   For plugin-level onboarding/config use `p.RequiredParams(&sdkv1.Settings{...})`
   with a `SubmitHandler`.
5. **Build & run**: `go build ./...`, then `go run .`; the SDK logs each subscribed
   subject on startup. Verify by adding the node to a flow and running it.

## Known limitations to respect

- **Meta functions** (live per-field form validation via `SubmitTo`) are defined in
  the protocol but have **no exported registration method yet**. Use the settings
  `SubmitHandler`, which is wired. Do not write code calling a non-existent
  `AddMeta`/meta registration API.
- If asked for anything about **extrinsic** nodes, redirect to `inflow-fusion`; it
  is not part of this SDK.

## Verify before finishing

- `go build ./...` passes.
- `main` blocks after `Start()`.
- Each action: unique `Method`, a `RequestHandler`, exactly one finish per path.
- Each `Jsonschema` matches its input struct.
- No fabricated SDK methods — every `Job`/`Plugin` call exists in `sdkv1/`.
