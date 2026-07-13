# Inflowenger ecosystem — working notes

> **Purpose of this file.** A staging ground for material about the *broader*
> Inflowenger platform that surfaced while documenting this SDK. It is not
> plugin-SDK reference (that lives in the sibling docs); it's the seed for a future
> comprehensive, cross-repo ecosystem/node-types document. Add to it whenever
> something ecosystem-level turns up. Items marked _(to verify)_ are inferred and
> should be checked against the source repos before they graduate into official
> docs.

## The computational model

Inflowenger is a **runtime for context processing** — a substrate for building
software whose logic is a workflow graph. Positioned as a general-purpose engine
for large classes of systems (ERP, CRM, automation platforms) where an operator or
super-admin must define and change logic without redeploying the base system.
Comparable in spirit to n8n, but as a computational model rather than an app.

Four primitives (from the marketing site,
`inflow-vue/inflow-nuxt/app/components`):

| Concept | One-liner | Computer analogy |
|---------|-----------|------------------|
| **Context**  | The memory. Everything enters as context. | RAM |
| **Workflows**| The logic. Logic as a visible, traceable graph. | Program |
| **Fractals** | The processors. Runtime instances that execute graphs. | Process / OS instance |
| **Adapters** | Connect to the world. | Drivers / I/O |

Stated values of the graph model: *business logic becomes visible*, *execution
becomes traceable*, *change becomes safer*.

The OS analogy the site draws explicitly:

- Traditional Computer ↔ **Fractal Runtime** (process instance; a node can be an
  embedded flow)
- Operating System ↔ **Inflowenger Runtime**
- Extensions, drivers & interrupts ↔ **Plugins, extrinsic nodes, Fractal instances**

## Node taxonomy _(to expand — this is the promised node-types doc's home)_

- **Primitive / principal nodes** — the small built-in set the platform ships.
- **Higher-level nodes** — compile *down* to primitives.
- **Plugin node** — does **not** compile to primitives; it's a live external
  process the Fractal calls into. The most mature, full-featured, extensible node
  type. Everything in this SDK is about this node.
- **Extrinsic nodes** — referenced alongside plugins as an extension mechanism.
  _(to verify: relationship between "plugin" and "extrinsic node")_

> TODO for the full node doc: enumerate the primitive nodes, describe how
> compilation to primitives works, and place plugin/extrinsic nodes precisely in
> the taxonomy.

## Platform components (from `getting-started`)

Two headless services plus optional tooling:

- **Infra** — bootstraps and coordinates everything. Embeds a **NATS** server;
  mints accounts, credentials, and the onboarding portal. Everything starts here.
  Ports: `8022` (HTTP API / onboarding), `8222` (NATS monitor), `4222` (NATS
  client). Holds the **API Secret Key** (shared HMAC secret).
- **Fractal** — the runtime that executes workflow graphs; registers itself
  against Infra over the shared `inflow_net` Docker network.
- **Dev panel** (optional) — `inflow-inspector-api` (Go/Fiber, `:8025`) +
  `inflow-inspector` (Vue SPA, `:8080`). Visual window into context, workflows, and
  Fractals. Itself built on Inflowenger via `inflow-fusion`. Auth model mirrors
  Swagger's "Authorize": the SPA holds no secrets, signs an HS256 `{admin:true}`
  JWT in-browser from the shared secret, backend verifies it.

Related repos seen in the workspace: `inflow-fusion`, `inspector-api`, `infra`,
`inflow-vue` (Nuxt marketing site + inspector packages), `getting-started`.

## NATS as the platform bus

- Everything runs over NATS request/reply. Infra embeds the server; plugins and
  the runtime are clients.
- **Credentials**: decorated NATS `.creds` (JWT + NKey seed). The plugin SDK takes
  them **base64-encoded** in `INFRA_CRED`; it reads the account from the JWT.
  - _Access hardening (internal detail, not user-facing):_ the JWT may carry a
    custom inbox prefix (SDK honors a `_INBOX*` tag). Its point is **hardening
    accessibility when many plugins share one account/space** — scoping each
    plugin's private reply inboxes so co-tenants of the same account can't reach
    them. Keep this out of the conceptual docs; it's a security-hardening knob.
- **Spaces = NATS accounts** (auth / authz / isolation unit). A plugin must be
  *defined in a space* before it can connect; that registration is what makes its
  `PLUGIN_ID` reachable and scopes its subjects/domains. Inflow ships a **built-in
  plugins space** for single-tenant use; **multi-tenant / enterprise** setups
  define plugins in **custom accounts** to isolate accessibility and domain scope
  (per tenant / trust boundary). Infra mints `INFRA_CRED` (carrying the account),
  `PLUGIN_ID`, for the plugin once defined.
  - _(to verify: exact Infra API/flow to define a plugin in a space and mint its
    credential; how domain scopes map to NATS subject permissions / import-export.)_
- **Infra clustering**: enterprise/large deployments may run Infra as a **cluster
  with multiple instance endpoints**, so `INFRA_URL` is always explicitly required
  (never assumed) — points at the cluster endpoint(s). _(to verify: multi-endpoint
  URL format the SDK/NATS accepts — comma-separated seed URLs?)_
- **Subject conventions** (plugin-facing; see
  [protocol-inflowv1.md](protocol-inflowv1.md)) — two markers carry the meaning:
  - **`@`-prefixed segment ⇒ UI / arguments plane.** `@intro`, `@settings`,
    `@actions`, `@form` are how the node presents and configures itself; pure
    metadata, nothing runs.
  - **`cpu` ⇒ execution plane.** `inflow.cpu.<PLUGIN_ID>.*` is the node's main
    call, requested by the Fractal at runtime (action dispatch + job commands).
  - `inflow.v1.<PLUGIN_ID>.*` — metadata/control (the `@` subjects, plus meta
    functions).
  - `inflow.cpu.<PLUGIN_ID>.<JOB_ID>.<cmd>` — per-job commands
    (`progress` / `context/current` / `context/path` / `commit` / `stop`).

## Context as a JSON-path-addressable tree

The flow context is addressable by **JSON path** (`$.OPA`, `$["doc appendix"]`,
...). Plugins read scopes (`context/current`, `context/path`) and commit values
(`commit`) at paths. _(to verify: exact JSON-path dialect / library used by the
runtime; behavior when a path doesn't exist; merge vs. replace semantics on
commit.)_

## The `_registry` envelope

Execution requests wrap input as `{ "_registry": {...}, "body": {...} }`. The
registry carries runtime metadata including this node's **previous** run
(`jobId`, `doneAt` seen in samples). _(to verify: full set of `_registry` fields
and their guarantees — useful for idempotency/resume patterns.)_

## Forms / UI

- Forms are **JSON Schema + UI Schema**, rendered by **JSON Forms**
  (jsonforms.io).
- Inflowenger ships `@inflowenger/inflow-ui` (Vue 3 + TS) extending JSON Forms with
  `x-inflow-ui` custom renderers: control, enum, layout, one-of, action button,
  plus a theme config and action registry. Lives at
  `inflow-vue/inflow-inspector/packages/inflow-ui`.
- _(note: the SDK author referred to "formjson.io"; the actual library in use is
  **JSON Forms / jsonforms.io**.)_

## Open questions / to verify before official docs

- Exact primitive node set and the compile-to-primitives pipeline.
- Public registration API for **meta functions** (type + wiring exist in the SDK;
  no exported setter yet).
- Intro serialization: `introHandler` marshals the `Intro` *method value* rather
  than the stored `intro` field — confirm intended behavior / whether this is a
  bug to fix. _(code observation, not user-facing)_
- Whether `PluginIntro.Settings` (intro-attached form) and `RequiredParams`
  settings are meant to coexist or are two iterations of the same idea.
- Semantics of `CmdStopFlow` (does it fail the run, or complete it early?).
- Scaling model: multiple plugin instances sharing a `PLUGIN_ID` (NATS queue
  groups?) for horizontal scale.
