# Detached work — the plugin as an async function, the flow as observer

> Advanced pattern, and the companion to
> [external-job-identity.md](external-job-identity.md): that doc gives the work
> **one name**, this one gives it **a lifetime longer than the flow's**.

Some work does not fit inside a flow run, and no amount of engineering will make
it. A code-property-graph build takes six hours. A nightly scan takes two. A
video render, a training job, a migration, a third-party export: the external
service measures these in hours or days, and the flow that asked for them
measures itself in seconds.

The usual instinct is to make the plugin wait. This document is about the
alternative: the plugin **returns immediately** with the work's *state*, the
flow's process finishes normally, the external work carries on, and a later run
of the same node picks the story up where it left off. The flow becomes an
**observer** of an external process rather than its container — and when the
work finally finishes, the node routes to a different branch of the graph and
the flow continues as if it had been waiting all along.

The point worth keeping in view: **nothing in the runtime knows about any of
this.** There is no "async node", no "poll node", no job type. One plugin node
type, and a plugin that decides its own temporal shape.

---

## 1. Why waiting does not work

Three budgets bound a plugin node, and all three are the runtime protecting
itself from a plugin that hangs:

| Budget | Value | What trips it |
|---|---|---|
| **Accept** | 15s (`initRequestTimeout`) | The plugin has not replied a `jobId`. The node fails as unreachable. |
| **Idle between commands** | the node's `idle_min` (`CancelAfterIdle`), defaulting to the run's `ExecuteTimeOut` in minutes | The job sent no command for that long. The node concludes `long_time_without_command` and gives up on it. |
| **The whole run** | `Settings.ExecuteTimeOut` | The process is over, whatever it was doing. |

A plugin that blocks on a six-hour query has two bad options:

- **Block silently** → the node is concluded `long_time_without_command`, and
  the flow moves on (or fails) while the job runs on in the plugin, unwatched.
- **Block with keepalive progress** → every heartbeat is a NATS round trip, a
  live engine process is pinned open for six hours, a canvas node sits at 40%
  for an afternoon, and the run still dies at `ExecuteTimeOut`. Nothing was
  gained except the illusion of attention.

There is a third option the protocol was built for, stated in fractal-core's own
words at the accessor that serves `_registry`:

> *"A plugin's job can outlive the process that started it, so on the next run
> the plugin is handed back its own last jobId and can ask its own side how that
> job is getting on. Nothing here is per-execution — it is a node's memory."*
> — `engine/task_accessors.go`, `GetRegistry`

That is the whole pattern. The rest of this document is how to use it.

---

## 2. Three stances a plugin can take

A plugin author chooses, per action, how the action relates to time. The node on
the canvas looks the same in all three cases.

| Stance | Work takes | The job | Needs |
|---|---|---|---|
| **Synchronous** | ms–seconds | does the work, `Done` | nothing — the default |
| **Streaming** | seconds–minutes | works, `Progress` as it goes, honours `ctx` | [`jobstop`](jobs-and-commands.md#stopping-a-job-with-its-flow--package-jobstop) if it must die with the flow |
| **Detached + observed** | hours–days | reports *state*, `Done` in seconds | `_registry`, a shared id, and a branch for "not yet" |

The third is this document. The same plugin can hold all three: a settings test
is synchronous, an upload streams, a CPG build is detached. **The decision is
the plugin author's, encapsulated behind an ordinary action.** A flow author
draws a node and wires its ports; they never learn which stance it took, only
that one of its ports means *not yet*.

---

## 3. The mechanism

### What the plugin knows at the accept stage

Exactly two things, and this is worth being precise about because the pattern
lives here:

```json
{ "_registry": { "jobId": "…", "reqAt": 1782773000, "doneAt": 0, "conclusion": "done" },
  "body":      { "project": "acme/api", "query": "…" } }
```

- **`body`** — the node's form input, as always.
- **`_registry`** — **the node's memory**: what the engine recorded about this
  call site's *previous* run over this same context document.

Job commands are **not** available yet. The runtime subscribes to a job's
command subjects only once it has been told the `jobId`, which is precisely what
the middleware is deciding — so there is no context read, no progress, no
routing until the job is accepted. `_registry` is the accept stage's only
channel, and it is enough.

What the engine puts there (`engine/task.go`, `pluginTypeProcess`):

| Key | Written | Meaning |
|---|---|---|
| `jobId` | at accept | the id this node's last run accepted — **the handle** |
| `reqAt` | at accept | unix seconds when that run started |
| `doneAt` | `0` at accept, a timestamp on a clean finish | `0` means that run did not finish cleanly |
| `conclusion` | when the job ends | `done`, `failure`, `flow_stop_by_user`, `timeout`, … |

Two properties of the registry matter as much as its contents:

- **It is keyed by call site, not by node id.** A `GoTo` that mounts the same
  sub-flow twice gives each copy its own memory, so one call's handle cannot
  overwrite the other's (`registryKey`, with a test to that effect in
  fractal-core's `engine/registry_test.go`).
- **It lives in the context document.** Anything running over the *same
  context* — a resumed run, a scheduled continuation, a re-entry through a loop
  edge — sees it. A run over a *new* context starts with an empty memory, by
  design: it is a different piece of work.

### Attach, or start

```go
// attachOrStart decides, at the accept stage, whether this execution starts new
// work or observes work a previous execution of this same node started.
func attachOrStart(ctx context.Context, job sdkv1.Job) (context.Context, error) {
    in, err := sdkv1.CastRequestTo[QueryInput](job.Req.Data)
    if err != nil {
        return nil, fmt.Errorf("bad request: %w", err)
    }

    // What this call site left behind last time it ran.
    if prev, ok := in.Registry["jobId"].(string); ok && prev != "" {
        if joern.Has(ctx, prev) {
            // The service still holds that query — running, or finished and not
            // yet collected. Observe it; start nothing.
            return sdkv1.WithJobIDContext(ctx, prev), nil
        }
        log.Printf("joern: previous query %s is gone; starting a new one", prev)
    }

    queryId, err := joern.Register(ctx, in.Body.Project, in.Body.Query)
    if err != nil {
        return nil, fmt.Errorf("joern refused the query: %w", err)
    }
    if err := usableJobID(queryId); err != nil {
        joern.Cancel(context.WithoutCancel(ctx), queryId)
        return nil, err
    }
    return sdkv1.WithJobIDContext(ctx, queryId), nil
}
```

The decision is a single question — *does the service still hold the work my
last run started?* — and the answer either adopts the old id or mints a new one
from a fresh registration. Either way the job runs under **the upstream id**,
which is what makes `_registry["jobId"]` a handle on the external service rather
than a plugin-local token. That is the whole dependency on
[external-job-identity.md](external-job-identity.md): adopt the id once, and the
node's memory becomes durable, storage-free, and meaningful to both systems.

> **Why `Has`, not `Alive`.** A query that *finished* while nobody was looking
> must also be adopted — that run is the one that collects the result. The
> predicate is "the service still knows this id", not "it is still running".

### Report, route, end

```go
// observe reports where the query has got to and ends the job — in seconds,
// whatever the query is doing. The flow's process finishes; the query does not.
func observe(job sdkv1.Job) {
    ctx := job.Context()
    queryId := job.JobId // the Joern queryId, this run and every later one

    status, err := joern.Poll(ctx, queryId)
    if err != nil {
        job.DoneWithError(fmt.Sprintf("joern: cannot read query %s: %v", queryId, err))
        return
    }

    switch {
    case status.Failed:
        // Fail the node *and* route: the flow's error branch still fires.
        job.CmdNextFilter([]string{tagFailed})
        job.DoneWithErrorData(status.Error, map[string]any{
            "queryId": queryId,
            "stage":   status.Stage,
        }, "joern")

    case !status.Done:
        // The observer's normal outcome: a successful, uninformative job that
        // says "not yet" and hands the branch to whatever re-checks later.
        job.CmdNextFilter([]string{tagPending})
        job.Done(map[string]any{
            "queryId":    queryId,
            "state":      "running",
            "percent":    status.Percent,
            "stage":      status.Stage,
            "observedAt": time.Now().Unix(),
        }, "joern")

    default:
        // Collecting consumes the upstream query, so the next execution of this
        // node starts a fresh one instead of re-collecting this result.
        if err := joern.Release(context.WithoutCancel(ctx), queryId); err != nil {
            log.Printf("joern: could not release %s: %v", queryId, err)
        }
        job.CmdNextFilter([]string{tagReady})
        job.Done(map[string]any{
            "queryId": queryId,
            "state":   "done",
            "result":  status.Result,
        }, "joern")
    }
}
```

Three outcomes, three ports, declared on the action so the canvas shows them
before anything runs:

```go
p.AddAction(sdkv1.Action{
    Method: "cpg.query.observe",
    // No stops.Middleware here, deliberately — see § 5.
    Middleware:     sdkv1.Use(attachOrStart),
    RequestHandler: observe,
    Outbound: []sdkv1.OutboundPort{
        {Title: "Still running", Tags: []string{tagPending}, Description: "The query is not finished; re-check later"},
        {Title: "Result ready", Tags: []string{tagReady}, Description: "The result is committed to the node's scope"},
        {Title: "Query failed", Tags: []string{tagFailed}, Description: "The query failed upstream"},
    },
})
```

Note what each outcome does and does not do:

- **`pending`** is a **successful** job. The node did its work — it looked —
  and the flow is told, truthfully, that the answer is not in. Failing here
  would be a lie, and would route the error branch for a query that is fine.
- The committed snapshot (`state`, `percent`, `stage`, `observedAt`) lands on
  the node's scope under `joern`, so the *flow* can see progress between runs —
  a dashboard, a contract that escalates after N attempts, a human reading the
  context document.
- **`_exception`** fails the node *and* routes: the reserved tag's edge fires,
  so a handler branch you drew runs while the node is recorded as an error.
- `Done` ends the job in seconds. The process concludes `done`, the proc signal
  says so, the engine writes `doneAt` — and the query keeps running.

### The shape over time

```
run #1  09:00   attachOrStart: no memory → POST /query → q-8f21c47b3d
                observe: 2% → commit {state:running} → route "pending" → Done
                process ends (done).  registry: {jobId: q-8f21c47b3d, doneAt: …}
                                                    ▲
                ──── joern keeps building the CPG ──┼───────────────────────▶
                                                    │
run #2  09:30   attachOrStart: Has(q-8f21c47b3d) → adopt the SAME id
                observe: 38% → commit → "pending" → Done
                                                    │
run #3  15:00   attachOrStart: adopt again          │
                observe: done → commit result → Release → route "ready" → Done
                                                    ▼
                the flow continues down the "ready" branch, six hours later,
                with the result on its context — and no process waited for it
```

Each run costs one HTTP call and one job. Nothing is pinned open in between: no
goroutine, no connection, no engine state — the same economy the platform's
park-and-resume has, reached from the plugin side.

---

## 4. Closing the loop: who re-runs the node

The plugin reports state; something has to come back and look again. That is a
**graph** decision, not a plugin one, and the pattern is indifferent to which of
these the flow author chooses:

| Shape | How it re-enters | Suits |
|---|---|---|
| **Continue After** (park and resume) | the `pending` branch ends in a delay node; the backend schedules a resume over the same context | hours–days; the canonical answer |
| **A scheduled trigger** | an external schedule starts a run over the same context on a tick | fixed cadence, many nodes observed at once |
| **A loop edge + contract** | the `pending` branch leads back to the node, with a contract counting attempts in the context | short waits, bounded retries |
| **An event** | a webhook from the service resumes the parked flow directly | the service can call back |

All four work because the plugin's side is identical in each: read the memory,
adopt the handle, report, route. The flow's re-entry mechanism is described in
the wiki's [Waiting — joins and delays](https://github.com/Inflowenger/inflow-wiki/blob/main/book/02-fusion/waiting.md);
the only requirement this pattern adds is the one that makes a memory a memory:

> **Re-enter over the same context document.** `_registry` is a node's entry in
> that document's header. A resume, a scheduled continuation and a loop edge all
> satisfy this; starting a brand-new run over a new context does not, and the
> plugin will correctly start new upstream work.

A loop also needs a floor: a contract on the `pending` branch that counts
attempts and routes to an escalation after N, or a `reqAt` check in the plugin
that gives up on a query older than its own patience. An observer with no
stopping rule is an infinite flow.

---

## 5. Cancellation: detached work usually must *not* be stopped

This is where the pattern deliberately parts company with
[`jobstop`](jobs-and-commands.md#stopping-a-job-with-its-flow--package-jobstop).

For a streaming job, a stopped flow means the work is waste, so the job dies
with it. For detached work the opposite is true: **outliving the flow is the
feature.** The process ending is the normal case — it ends on *every* run — and
a user stopping the flow does not mean "throw away four hours of CPG
construction", because the next run reattaches to exactly that query.

So an observer action takes **no** `stops.Middleware`, and its plugin sends no
upstream abort on `Conclusion.Canceled()`. The observer job is seconds long;
there is nothing to cancel.

Three cases where you do want a stop, and what each actually asks for:

| Case | Mechanism |
|---|---|
| The *observing* job (one poll) should abort with the flow | `stops.Middleware` on the action — harmless, nearly pointless |
| The *upstream work* should die when a user abandons the flow | an explicit decision: a signal handler that calls `joern.Cancel(ctx, sig.JobId)` on `Canceled()` — see [external-job-identity.md § 4](external-job-identity.md#4-cancellation-making-a-stop-cross-the-boundary). Add it only if an abandoned query costs money or capacity |
| The upstream work should die when *nobody will ever collect it* | not a signal at all: a TTL on the service, or a sweeper that drops queries no flow has polled since `reqAt + N` |

The third is the honest answer for most deployments, because a flow can be
deleted, re-authored or never re-run, and no signal is published for *that*.

---

## 6. Why this needs no new node type

The flow author's view of all of the above is: a node with a port marked *Still
running* and a port marked *Result ready*. That is the entire interface. Behind
it the plugin may be:

- registering and observing an external job (this document);
- doing the work itself in a background goroutine and reporting its own state;
- fronting a queue, where "pending" means "not yet consumed";
- fronting a human process, where "pending" means "nobody has answered".

Every one of those has the same node type, the same protocol, the same two
inputs at the accept stage and the same three ports. The runtime needed no
concept of asynchrony to make it work: it needed a node's memory (`_registry`),
a way to name work that outlives a process (the shared id), and routing from
inside a job (`CmdNextFilter`). All three already existed for other reasons.

That is the coverage argument in miniature. A platform that added an "async
node" would have one more primitive, and still not cover the human-process case
without a second one. **Here the asynchrony is in the plugin, where the
knowledge is** — the plugin author is the only party who knows whether their
service answers in 3ms or 3 days, and that knowledge stays encapsulated with the
code that depends on it.

---

## 7. Limits, stated plainly

**Exactly-once is not promised, and this pattern makes that visible.** A
re-entry may re-execute the action; the registry handle is what keeps it from
starting a second query, and the handle is only as good as the service's memory
of that id. If Joern forgets the query (restart, retention, a cleaner), the
plugin correctly starts a new one — and the old one may still be running
somewhere. Where that is expensive, give the service a client-supplied
idempotency key derived from the input, not the jobId.

**The registry's writable half is not exposed by this SDK.** The protocol has
two more job commands — `registry/update` and `registry/get`
(`models.JobCommand` in fractal-core) — which let a job write its own entry in
the node's memory: a cursor, a phase, an upstream handle that is not the jobId.
The Go SDK's `Job` does not surface them today, so the durable per-node state
available to a plugin is the `jobId` the engine records, plus anything the job
commits into the node's **scope** (`Done(data, key)` / `CmdSetOnPath`). That is
enough for this pattern — the handle *is* the jobId — but a plugin needing
richer memory should commit it to scope deliberately and read it back with
`CmdGetScope("$this.…")` **in the handler**, not in middleware, where context
reads are not yet possible.

**`doneAt` and `conclusion` describe the run, not the work.** An observer run
that said "pending" finishes cleanly: `doneAt` is set and `conclusion` is `done`
even though the real work is unfinished. Do not read them as the state of the
external job — that is what the service is for. `reqAt` is useful, though: it
dates the handle, which is how a plugin gives up on a query that has outlived
its patience.

**Polling has a cost, and it is linear in re-entries.** One HTTP call per run is
cheap; a one-minute loop edge over a six-hour build is 360 runs, 360 jobs and
360 rows in whatever records them. Match the re-entry cadence to the work: a
delay node measured in minutes, not seconds. Sub-minute observation belongs
inside a single streaming job, not in the graph.

**The result must survive the run that collected it.** The `ready` branch is the
only run that ever sees the result, so it must be committed (`Done(data, key)`)
rather than merely reported — a bare `Done` with no `key` commits nothing at a
path, and the next node reads an empty scope.

**A stale handle is the one failure mode to test.** Write the case where the
service has forgotten the id: the plugin must start fresh rather than report
`pending` forever against a query that no longer exists. That is why
`attachOrStart` logs and falls through rather than trusting the memory.

---

## 8. Checklist

- [ ] The action's ports include a **"not yet"** port, declared in `Outbound`.
- [ ] The namer reads `_registry["jobId"]` and adopts it **only if the service
      still holds that work**; otherwise it registers new work.
- [ ] The adopted/registered id passes the identity rules (≥ 10 characters, a
      usable subject token, unique plugin-wide — see
      [external-job-identity.md § 6](external-job-identity.md#6-rules-limits-and-failure-modes)).
- [ ] "Still running" ends as **`Done`**, not an error, and routes the pending
      tag.
- [ ] The state snapshot is **committed** to the node's scope, so the flow can
      see progress between runs.
- [ ] The completing run **commits the result** and consumes/releases the
      upstream work.
- [ ] Upstream failure routes `_exception` **and** reports the failure, with any
      partial state in `DoneWithErrorData`.
- [ ] No `stops.Middleware` on the observer action unless the single poll really
      must abort with the flow.
- [ ] Re-entry happens **over the same context document**, and the loop has a
      floor (attempt count, or a `reqAt` age limit).
- [ ] The stale-handle path is implemented and tested.

## See also

- [external-job-identity.md](external-job-identity.md) — the shared id that
  makes the registry handle meaningful to both systems; the middleware mechanics
  in full.
- [jobs-and-commands.md](jobs-and-commands.md) — `CmdNextFilter`, `Done` /
  `DoneWithErrorData`, committing to scope, the signal port.
- [protocol-inflowv1.md](protocol-inflowv1.md) — the `{_registry, body}`
  envelope and the handshake.
- The wiki's [Long-running work](https://github.com/Inflowenger/inflow-wiki/blob/main/book/03-plugins/long-running-work.md)
  chapter — the same pattern from the platform's side, with the flow shapes it
  composes with.
- [`sdkv1/detached_integration_test.go`](../sdkv1/detached_integration_test.go) —
  the accept-stage adoption, the stale handle and the routed "not yet" as
  executable tests.
- [cookbook.md § Skill 14](../cookbook.md#skill-14--report-and-observe-instead-of-waiting-advanced) — the condensed recipe.
