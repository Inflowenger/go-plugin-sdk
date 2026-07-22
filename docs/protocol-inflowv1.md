# The `inflowv1` protocol

`inflowv1` is the contract between the Inflowenger runtime (Fractal) and a plugin,
carried over NATS request/reply. This SDK is the reference **v1** implementation.
Everything below is namespaced by the plugin's `PLUGIN_ID`.

## Subject map

Two conventions classify every subject, and they carry the meaning:

- **`@`-prefixed segments are the UI / arguments plane.** Any subject segment that
  starts with an at-sign (`@intro`, `@settings`, `@actions`, `@form`) is about how
  the node *presents and configures itself* — its identity, its settings form, its
  action list, and each action's input form. These are metadata lookups the front
  end and runtime read to build the canvas and gather arguments; nothing executes.
- **`inflow.cpu.*` is the execution plane.** The `cpu` family is the **main call**:
  the actual function the Fractal requests at **runtime** when it reaches your node
  in a flow, plus the job commands that call flows back while it runs.

So, read a subject by its markers: a `@` part means "describe/configure me" (UI &
arguments); `cpu` means "run me" (the Fractal's runtime call).

### `inflow.v1.*` — metadata / UI & arguments plane (the `@` subjects)

Everything here is discovery: no work runs, it only feeds the UI and collects
arguments. Note every row carries an `@`-prefixed segment except the meta function.

| Subject | Direction | Purpose | Response |
|---------|-----------|---------|----------|
| `inflow.v1.<PLUGIN_ID>.@intro` | runtime → plugin | Ask who the plugin is. | `PluginIntro` JSON |
| `inflow.v1.<PLUGIN_ID>.@settings` | runtime → plugin | Fetch the settings form. | `Settings` form JSON (or empty) |
| `inflow.v1.<PLUGIN_ID>.@actions` | runtime → plugin | List all actions. | `[]Action` JSON |
| `inflow.v1.<PLUGIN_ID>.<ACTION>.@form` | runtime → plugin | Fetch one action's form (its arguments schema). | `FormBuilder` JSON |
| `inflow.v1.<PLUGIN_ID>.<META>` | runtime → plugin | Call a **meta function** (e.g. live form validation) or submit settings — still UI-support, not the node's main call. | `Response` JSON |

### `inflow.cpu.*` — execution plane (the runtime call, invoked by Fractal)

| Subject | Direction | Purpose | Response |
|---------|-----------|---------|----------|
| `inflow.cpu.<PLUGIN_ID>.<ACTION>` | runtime → plugin | **Execute** an action. Starts a job. | `{"jobId":"<uuid>"}` (immediate ack) |
| `inflow.cpu.<PLUGIN_ID>.<JOB_ID>.<CMD>` | plugin → runtime | A running job's command back to the runtime. | command-specific |

The `<CMD>` values on the last subject are the job commands:

| `<CMD>` | Sent by | Meaning |
|---------|---------|---------|
| `progress` | `job.Progress` / `job.Done` / `job.DoneWithError` | Report progress `0–100` (100 = finished). |
| `context/current` | `job.CmdGetCurrentScope` | Read the current context scope. |
| `context/path` | `job.CmdGetScope` | Read context by JSON path. |
| `commit` | `job.CmdSetOnPath` | Write data into context at a JSON path. |
| `next_tags` | `job.CmdNextFilter` | Route outbound ports: keep only the named tags. |
| `request/svc` | `job.CmdSvcCall` | Call an extrinsics service through the runtime. The runtime forwards the `{data, op}` envelope with an `origin: plugin:<node title>` header so the service can refuse ungranted plugin-originated calls. |
| `stop` | `job.CmdStopFlow` | Stop the whole flow. |

## The request → job handshake

Execution is deliberately two-phase so the runtime gets a fast acknowledgement
while the actual work runs asynchronously:

```
runtime                         plugin
  │                                │
  │  REQUEST inflow.cpu.<id>.<act> │
  │───────────────────────────────►│   subscribe handler fires
  │                                │   • mint jobId (uuid)
  │  REPLY {"jobId":"<uuid>"}      │   • Accept(msg): reply immediately
  │◄───────────────────────────────│
  │                                │   RequestHandler(job) runs now
  │  REQUEST inflow.cpu.<id>.<job>.progress  {progress:20,...}
  │◄───────────────────────────────│
  │  REQUEST inflow.cpu.<id>.<job>.context/path  "$.OPA"
  │◄───────────────────────────────│
  │  REPLY <context bytes>         │
  │───────────────────────────────►│
  │  REQUEST inflow.cpu.<id>.<job>.progress  {progress:100, details:{...}}
  │◄───────────────────────────────│   (job.Done)
```

The immediate `{"jobId":...}` reply is how the runtime correlates all subsequent
job commands to this execution. Every job command is itself a NATS request; the
runtime's reply carries context data (for reads) or an acknowledgement.

## Request payload shape

The body delivered to an action is JSON of the form:

```json
{
  "_registry": { "jobId": "…", "doneAt": 1782773013, "…": "…" },
  "body":      { "…": "action-specific input from the node's form" }
}
```

- **`body`** — the user-supplied input, shaped by the action's form (JSON Schema).
- **`_registry`** — metadata the runtime attaches, notably about this node's
  **previous** run (e.g. the prior `jobId` and its `doneAt` timestamp). Use it for
  idempotency, dedup, or resuming.

Parse it with the generic helper, which fills a typed `RequestBody[T]`:

```go
type Input struct {
    Url    string `json:"url"`
    Method string `json:"method"`
}

req, err := sdkv1.CastRequestTo[Input](job.Req.Data)
// req.Body     -> Input
// req.Registry -> map[string]any
```

## Response shapes

- **Metadata lookups** reply with the marshalled declaration (`PluginIntro`,
  `[]Action`, `FormBuilder`, ...).
- **Meta functions & settings submit** reply with a `Response`:

  ```go
  type Response struct {
      Data  map[string]any `json:"data"`
      Error any            `json:"error"`
  }
  ```

- **Job commands** reply with raw command output — for context reads, the bytes of
  the requested scope; for writes/progress, an acknowledgement.

## Transport details

- **Request/reply with retry.** `Plugin.Send` uses NATS request/reply with a 3s
  timeout and retries up to 5 times on `ErrNoResponders` (backing off), so a job
  command issued a moment before the runtime is listening still lands.
- **Credentials.** The connection authenticates with a decorated NATS `.creds`
  blob (JWT + NKey seed), supplied **base64-encoded** in `INFRA_CRED`. The SDK
  decodes it, reads the account from the JWT, and connects with auto-reconnect.
  See [`nats/natsBox.go`](../nats/natsBox.go).

## Protocol versioning

The `v1` in both the subject prefix (`inflow.v1.*`) and the package name
(`sdkv1`) is the protocol version. A future revision would introduce
`inflow.v2.*` subjects and an `sdkv2` package alongside this one, so plugins and
runtimes can negotiate and migrate independently.
