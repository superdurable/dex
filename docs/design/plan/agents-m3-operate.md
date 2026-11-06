# Agents M3: Connector Tools, the Operator View, and Session Lifetime

Status: proposed, design only. Nothing here is approved or built.
Built so far: [long-running-agents.md](../long-running-agents.md), the as-built design.
Current contract: [agent-framework-adapters.md](agent-framework-adapters.md) and
[agents-m2-stepwise.md](agents-m2-stepwise.md), as released in `agents/v0.2.0`.
Direction: the [owner review of #1132](https://github.com/superdurable/dex/pull/1132#pullrequestreview-5724027276),
the [Step mode comment](https://github.com/superdurable/dex/pull/1132#issuecomment-6547044614), and the
[review of the M2 decisions](https://github.com/superdurable/dex/pull/1178#issuecomment-6592361866).

M2 gave sessions a built-in loop, interrupts, steering, compaction, and bounded
state. M3 makes those sessions usable in production. An agent should reach real
systems through Connectors, an operator should see what needs a person, and a
long session should not end because its state filled up.

This plan goes deep on two items, connector tools (O1) and the operator view
(O2). The other items state scope and open questions, and each needs its own
detail before it is built.

## Principles

These come from the owner reviews and apply to every item.

1. **No new primitive.** "Dex will not add a new primitive for durable operations
   inside a Step." Every addition is ordinary Steps, Attributes, Channels, and
   RPCs.
2. **Start from the state machine.** Steps read and write Attributes. Nothing
   replays a loop, and no determinism rule exists.
3. **A fix lives in the layer that owns the problem.** The attempt-start work
   ([#1253](https://github.com/superdurable/dex/pull/1253)) replaced an
   application-called mark API
   ([#1235](https://github.com/superdurable/dex/pull/1235)) and an agents-only
   marker. A bridge between two layers translates. It does not carry correctness
   of its own.
4. **Reuse existing states.** [#1265](https://github.com/superdurable/dex/pull/1265)
   told "never ran" from "may have run" and added no final status.
5. **Decide from committed state.** An RPC reads a snapshot, and its effects
   resolve at commit. Each design below names its races and tests them.
6. **Settle persisted layouts before a release.** A later change means reading
   both layouts for every open session.
7. **Inert until enabled.** New fields have defaults, and sessions started on
   0.2.0 continue.
8. **No secrets in session state.** Only references, such as a connection name.
   The Worker resolves them.
9. **Python and TypeScript first.** They are the first tier for agents, and every
   item ships there first. Go and other languages are the second tier. A feature
   that exists only in Go is a gap to close, not a baseline.

## Preconditions

M3 implementation starts after these M2 items close. Design can proceed now.

| Item | State on 2026-11-06 |
| --- | --- |
| npm packages at 0.2.0 | `latest` is 0.1.0. The publish failed twice; #1317 fixed the first failure, and the second ended in `E404` |
| Go core tag | Untagged. The tag scheme needs the owner (`agents-go-java-rust.md`) |
| Published crash matrix | Not published. Worker-kill tests exist per core. [#1282](https://github.com/superdurable/dex/issues/1282) is open, and no agent-level test restarts or fails over the server |
| Cost of the SYNC start commit | Not measured. [#1266](https://github.com/superdurable/dex/issues/1266) is open |

## Relation to earlier proposals

[#1132](https://github.com/superdurable/dex/pull/1132) began as a proposal with an M3
and an M4. It merged as the as-built design, so this plan is now the only home for
unbuilt work. No item here waits for another row below.

| Earlier M3 row | Here |
| --- | --- |
| Engine fix for single-attempt exactly-once | Done (#1253, #1265) |
| Enforce `limits.maxFlowStateBytes` | Done (#1234) |
| Dex Web agent view | O2 |
| Budgets, cost estimates, and budget interrupts | O4 |
| Exit: load test of 10,000 sessions | O5, separated from concurrency groups, limits, and the view |
| Connector host SubFlows | Dropped: Python and TypeScript Connector packages exist |
| All other rows | [Later](#later) |

New here: connector tools as a tool source (O1), adapter session lifetime (O3), and
the harness segment limit (O6).

## Scope

| Item | What | Depth here |
| --- | --- | --- |
| O1 | Connector operations as agent tools | Design |
| O2 | Operator view and inbox in Dex Web | Design |
| O3 | Session lifetime and failure handling | Measured, urgent |
| O4 | Spend limits | Scope |
| O5 | Session scale test | Scope |
| O6 | Harness segment limit | Scope |
| O7 | Stream durability | Closed, no requirement |

## O1. Connector tools

### The gap

A Connector works in a Flow today. A developer maps input, wires each branch to
the next Step, and the Flow type is fixed at build time. An agent tool is chosen
by the model at run time. The two differ in five ways.

| | Connector Step | Agent tool |
| --- | --- | --- |
| Chosen by | The developer, at build time | The model, at run time |
| Input | `MapToOperationInput` from a typed value | JSON arguments against a JSON Schema |
| Runs in | Its own Step type | `CallTool` or `CallSingleAttemptTool` |
| Result | A named branch and a Receipt | A result for the model, `failed`, or `outcome_unknown` |
| Identity | Call ID: Flow ID, Step execution ID, Connector ID, Operation ID | `<flow>/<turn>/<tool_call>` |

An application can write a function tool that calls a provider SDK directly.
That loses the idempotency key, the `uncertain` branch, and credential isolation.
These are the reasons to use a Connector.

### Options

**A. Connector operations run inside the existing tool Steps.** A
`ConnectorToolSource` lists tools and runs one operation per tool Step.

**B. Each tool is its own connector Step.** The session Flow registers generated
Step factories, and `RunModel` moves to them.

| | A: existing tool Steps | B: one Step per tool |
| --- | --- | --- |
| Flow definition | Unchanged by adding a tool | Changes with every tool |
| Tools per session | Chosen at run time | A registered superset, filtered |
| Graph | Shows `CallTool` | Shows each operation by name; grows with the tool count |
| Connector contract | Reached through `run_query` and `run_mutation` | Reused whole |
| Session logic | `_run_tool_call` reused: turn control, events, spans, heartbeats, record updates | Needs hooks in every factory, in three languages |
| Tool call identity | Available | Lost: "Connector Steps do not forward their input", and a single Result Attribute collides under parallel calls |
| A tool removed while a session runs | The stored call fails by its effect class | The Step type stays queued until the Flow times out ([#1259](https://github.com/superdurable/dex/pull/1259)) |

**Decision: A.** The tool call identity problem alone rules out B without a
Connector contract change. A keeps the session Flow fixed, which also keeps one
operator view for every agent. The price is a bridge, kept thin by reusing the
Connector SDK's own entry points.

### Design

**`ConnectorToolSource`** implements the existing `ToolSource` protocol, as
`McpToolSource` does. An application names the operations an agent may use. No
Connector is exposed by default.

```python
slack = slack_connection("support-slack")
stripe = stripe_connection("billing-stripe")

agent = AgentDefinition(
    name="support",
    tool_sources=[
        ConnectorToolSource(
            source_name="connectors",
            tools=[
                connector_tool(slack.post_thread_reply()),
                connector_tool(stripe.create_refund(), requires_approval=True),
            ],
        ),
    ],
)
```

The call shape is illustrative. Names are settled in the PR that builds it.

**Execution.** A tool Step already runs one call per Step execution, which is what
the Connector contract expects: "One Step execution makes one Connector operation
invocation."

1. The source builds a `Call` with `derive_call_id(flow_id, step_execution_id, operation)`.
2. It decodes the model's arguments into the operation's input type. A decode
   failure is a `failed` call the model can correct.
3. It calls `run_query` or `run_mutation`.
4. It maps the result as below.

**`DeferredToolExecution` needs one addition.** `Call.context` must be the live
`AsyncContext`, because the single-dispatch fence reads and writes heartbeat checkpoints
through it. Today the source does not receive it. Add an optional field that exposes the
Step's context to a tool source. It is additive and absent from persisted values. The same
context gives the Step execution ID for the Call ID. The bridge also translates `RetryError`
into a Dex retry, because the helper that does this in connector Steps is private and
`Retry-After` is lost otherwise.

**Identity.** The Connector key is derived from the Call ID, so a `CallTool` retry
keeps it. A new Step execution gets a new key. Connector tools never ask a
question mid-call, so a call does not re-execute under a new Step execution. The
agent `idempotency_key` stays for other tools. To be confirmed: a call resumed
after an approval runs in its first tool Step execution, so its key is stable.

**Result mapping.** No final status is added.

| Connector outcome | Tool call |
| --- | --- |
| Success branch (`sent`, `read`, `found`) | Completed, with the Result value for the model |
| Declared business branch (`notFound`, `providerRejected`) | Completed with an error result, so the model can react. The session does not fail |
| `uncertain` | `outcome_unknown` |
| `defect` | `failed` |
| Retry | Raises through the Step, so Execute retry applies. Exhausted retries reach `RecordToolFailure` |

An unwired optional branch fails a Flow in a normal connector Step. That rule must
not apply here. One provider rejection must not end a session.

**Effect class and retry.** The effect class answers one question: can this call
safely run again?

| Operation | Effect | Runs in |
| --- | --- | --- |
| Query | `READ` | `CallTool` |
| Mutation with provider idempotency | `IDEMPOTENT_WRITE` | `CallTool` |
| Mutation with `dispatchFence` | `IDEMPOTENT_WRITE` (proposed) | `CallTool` |
| Mutation with neither | `NON_IDEMPOTENT_WRITE` | `CallSingleAttemptTool` |

The third row differs from the first mapping in #1132, which put any Mutation with
`uncertain` in `NON_IDEMPOTENT_WRITE`. A fenced Mutation is safe to retry, because
the fence refuses a second send. The clear gain is a write proven unsent, such as a 429:
`CallTool` sends it on retry, while `CallSingleAttemptTool` ends `outcome_unknown` with
nothing sent. Read-back helps only as far as the provider allows. With Slack, a kill while
the provider holds the response still ends `outcome_unknown`.

**Approval is a separate question from retry.** Approval asks whether the call
should happen. Idempotency does not answer that: a refund with an idempotency key
is still a real refund. By default `ToolPolicy` derives approval from the effect class,
so `IDEMPOTENT_WRITE` needs no approval, and `requires_approval` overrides it. That is right for most writes, because
approving every message would make an always-on agent unusable. It is wrong for a
few operations.

| Operation | Why it should stop for a person |
| --- | --- |
| Stripe `createRefund` | Money leaves. The request may come from text the agent read |
| DocuSign `createEnvelopeFromTemplate` | A legal document goes out |
| DocuSign `voidEnvelope` | Ends a document others may be signing |
| PostgreSQL `execute` | One wrong statement changes many rows |

Proposed:

- Mutations run without approval by default.
- A new optional manifest field marks operations a Connector author considers high
  risk, for example `agent: {approval: recommended}`. `ConnectorToolSource` turns
  that into `requires_approval=True`.
- The application can override either way per tool.
- The field is plain metadata. The core's rule does not change.

**Which operations are offered.** Only operations reviewed for agent use are
exposed, starting with a first set. An unreviewed Mutation is not offered, so no
operation has an unknown approval default.

**Tool schema.** The manifest names `inputType` and has no JSON Schema.
`connectorctl generate` should write a JSON Schema per operation into
`dex-connector.json`. That file's schema lives in dexcli and rejects unknown fields, so a
dexcli change and release come first. The schema is generated from the same models, plus an optional
model-facing description. Input field comments can seed it. Generating in each
agent core at run time would give three implementations of one mapping.

**Step options.** Tool Steps are SYNC with fixed options. Manifest `execution` may
say `durability: async` and a 30-second timeout. Keep SYNC, because ASYNC reopens
the double-run residual in the deploy guide. Carry `executeMethodTimeout` and retry
as movement-scoped options: `StepMovement.of` accepts options in Python and
TypeScript. An earlier Go agent implementation used one tool Step with movement-scoped
options and per-tool policy successfully.

**Write ordering.** Tool calls of a turn run in parallel today. A model that refunds and
then notifies has an order in mind. For connector tools, writes run one at a time in the
model's order, and reads stay parallel. If a write fails or ends `outcome_unknown`, later
writes of that turn do not run and record that they were skipped. `RunModel` schedules the
waves. Other tools keep today's behavior. Locks do not give this order: a Step waits for
its lock, but a lock cannot hold a write behind an earlier write that is waiting for approval.

**Connections.** The application binds a logical connection at Worker start, as
Connector Steps do. A per-session choice is a connection name in `session_settings`.
Acting as a specific user is M4.

**Adapters and languages.** `ToolSource` is Stepwise only, and Go has function tools
only. O1 ships for Stepwise in Python and TypeScript first. Adapters then need a
helper that exposes the same tools in each framework's format. Go follows once its
scope is decided. Connector packages exist for all three languages, so Connector
host SubFlows are not needed for Python and TypeScript.

**Result size.** Query output goes to the model and into Flow state. Most Connectors
already cap provider responses with `maxResponseBytes`, but that is far above what a model
should read. Bound the result per tool, and truncate with a note. This needs a default.

### Exactly-once

Results from a spike on released 0.2.0 in Python, with the real Fence, a fake provider, and
about 775 SIGKILLs over 913 runs. No run produced a second write. Five runs per case will not
show rare races. TypeScript, real providers, and server crashes are not covered.

| Failure | Fenced Mutation in `CallTool`, with the bridge fix |
| --- | --- |
| Worker dies before the claim | `outcome_unknown`, 0 writes. No marker means the retry reads back and never sends |
| Worker dies after the claim, before the provider receives it | `outcome_unknown`, 0 writes |
| Worker dies after the provider accepted, read-back finds it | Settled as sent, 1 write |
| Worker dies while the provider holds the response, read-back cannot find it | `outcome_unknown`, 1 write |
| Provider returns 429 | Retried and sent, 1 write |
| Tool Step never reaches a Worker | Waits in the durable queue |
| `cancel_turn` during a started call | The Step runs to its end, as for any synchronous tool |

The claim may not be durable when the send happens: it was missing in all 30 runs of one case.
Only the Fence's "no marker means read back" rule prevents a duplicate, so that rule must stay.

**Two conflicts with the core, measured.**

1. `_ToolStepHeartbeats` reads the last heartbeat value as a string. After a Marker heartbeat,
   every later attempt raises before the tool runs, so on released code a retry never reads back
   and an ordinary 429 ends `outcome_unknown`.
2. A bare background heartbeat clears the marker. This cannot cause a second write, because a
   missing marker means read back. It loses the provider ID or `canResend`. It needs a write
   window longer than the first background heartbeat, about 34 seconds of budget, so most
   30-second operations are not exposed.

**Fix.** `ConnectorToolSource` gives the Fence a small context proxy that stores the Marker as a
text checkpoint through `save_heartbeat_checkpoint`. It needs no Fence change and no core change
beyond the `context` field. Its weakness is that it depends on which context methods the Fence
calls, so a conformance test pins that. Rejected: changing the Fence, which each language would
repeat; and suspending background heartbeats, which caused heartbeat timeouts.

**One more core addition.** A tool cannot record `outcome_unknown` today. Add
`ToolOutcomeUnknownError`, so `uncertain` maps to it directly.

### Tests

| # | Test |
| --- | --- |
| O1-1 | Query tool completes; result reaches the model |
| O1-2 | Each branch kind maps as the table says, and a provider rejection does not fail the session |
| O1-3 | Kill the Worker in a fenced write before and after the claim; the provider receives one write |
| O1-4 | Fence and core heartbeats together, across retries |
| O1-5 | Parallel reads and ordered writes in one turn |
| O1-6 | Approval marker honoured, and an application override wins |
| O1-7 | Tool removed while a call is stored: the call ends by its effect class |
| O1-8 | Credentials never appear in the tool record, Attributes, Stream, or history |
| O1-9 | Conformance: the same operation gives the same record in Python and TypeScript |

## O2. Operator view and inbox

### Three surfaces

| Surface | Reader | Question |
| --- | --- | --- |
| The application's chat UI | End user | Talk to the agent |
| Dex Web agent view | Operator | What did this agent do, and what is waiting for me? |
| Dex Web Flow pages | Engineer | Why did this Step fail? |

The agent view does not replace the application's UI. It adds no agent logic, and
it reads standard Attributes and RPCs: "One Dex Web agent view (C9) and one approval
contract (C5) can therefore serve every framework."

### What is already there

Dex Web has run search, the Flow canvas, the timeline, state, Streams, Time Travel, Actions,
and a Work Queue. `actionableSteps.ts` derives Steps that wait for a person from the Flow
Definition Graph. Actions reach Dex Web through that graph, and `POST /api/v2/actions` invokes
them. It is the only route that invokes a chosen RPC. It is not authorization: the permission
it checks is a string the browser sends, and the trusted boundary in front of Dex Web
authenticates users.

### Session view

A tab on the run page, shown for Flows with the Dex Agents schema.

- **Conversation:** messages in order, linked to Step executions.
- **Tool calls of the running turn:** arguments, effect class, approval, and state, from
  `AgentToolCalls`.
- **Past turns:** the session deletes tool records when a turn ends, and only message text
  stays. Effect class, approval decision, and `outcome_unknown` of a past call are available
  from Flow history inside its window, or from the message text. The view says so. Keeping
  them for longer needs a persisted field, which is a decision for O2's first PR.
- **State of a call:** queued for a Worker comes from `StepMethodPendingReason`. Since #1259
  a tool Step can wait a long time, and a Flow timeout or stop skips the failure route.
- **Controls:** approve, reject, answer, steer, stop, through the Action route below.
- **Usage:** tokens from `AgentUsage`.

**Read path.** `get_agent_snapshot` is an RPC and needs a Worker. An operator most needs this
page when Workers are down. The view reads Attributes directly and treats the snapshot as
optional. This keeps schema knowledge in Dex Web, so the schema needs a stated compatibility
rule.

### Inbox

The inbox answers one question across sessions: what needs a person?

**Discovery.** `ActiveStepTypes` is a built-in search field, so `ActiveStepTypes =
"AwaitToolApproval"` finds sessions waiting for approval, across agents. It needs no indexed
Attribute and uses no tenant index key. Session Flows set `ActiveStepSearchMode` to
`WITH_WAIT_FOR`, which `start_session` options can do today. **UpdateFlowConfig** turns it on
for a running session. Local tests on the builtin backend showed a Flow appear while waiting,
leave when it moved on, and appear within seconds of the update. SQL backends and write cost
are not tested.

**Dex Web changes.** The v2 list has no `ActiveStepTypes` filter and searches one Flow type at
a time. The inbox needs both.

**Not covered by this search.**

- `outcome_unknown`: no Step waits on it. It stays in the session view.
- A draft plan waiting for `execute_plan`: no Step waits on it.
- Sorting by deadline: the deadline lives in `PendingInterrupts`, so a row reads it after
  search. Sorting works within a page only. If this is not enough, an indexed Attribute comes
  back as a question.

**Invoking session RPCs.** A session can hold several pending approvals, and the view draws
one card per call. Each card calls the existing Action route, which already accepts per-call
input. Two things stop it today:

1. The route answers 502 when an RPC returns a value, after the RPC has run. `resolve_interrupt`
   returns a bool. The route should return the output.
2. An Action needs a scalar condition Attribute, and the session has none. The route should
   accept an Action without a condition, because `resolve_interrupt` checks current state itself.

**Graph.** Dex Web reads Actions from the Flow Definition Graph. `dexcli visualize` on
`session_flow.py` reports invalid, and the Flow type is chosen per application. So Dex Agents
needs a generator for its session graph, with the Flow type filled in. Python and TypeScript
sessions then show their Actions with no SDK change.

**Python and TypeScript Actions.** Applications in those languages should also declare their
own Actions, as Go does. That needs a registration API, the Worker sending
`ActionPermissionMappings`, and `dexcli visualize` reading them. Server and protocol already
support it. It is worth doing, and the agent inbox does not wait for it.

**Permissions.** "Working as" filters on permissions that Workers project. Agent sessions
without mappings have nothing to match, so the first inbox does not filter by permission.

**Races.** Two operators approve the same call. `resolve_interrupt` locks `PendingInterrupts`,
and an RPC that cannot get a lock fails. The UI treats that as "already handled", then reloads.
A deadline can reject a call while an operator decides, so the result is read back from
committed state.

### Conditional approval

Approval is per call, and a tool source decides the policy per call from the arguments. So
`ConnectorToolSource` can offer an argument predicate, such as a refund under a limit, with no
core change. "Allow for this session" can read `session_settings`, which applies at the next
turn. Function tools keep a fixed policy. Editing arguments before approval needs a new field
on `ResolveInterruptRequest`, and stays out of scope.

## O3. Adapter session lifetime

Measured at default limits with scripted models, in Python. Real models, TypeScript, and the
Vercel and Claude adapters are not covered.

| Framework, profile | Reaches 200k tokens | Reaches 1M tokens | Crosses 64 MiB |
| --- | --- | --- | --- |
| Pydantic AI, light | Turn 37 | Turn 182 | Turn 732 |
| Pydantic AI, heavy | Turn 8 | Turn 38 | About turn 200 |
| LangGraph, light | Turn 37 | Never | About turn 62 |
| LangGraph, heavy | Turn 8 | Never | About turn 28 |
| OpenAI Agents, light | Turn 36 | Never | About turn 72 |
| OpenAI Agents, heavy | Turn 8 | Never | Turn 32 |

Light is 2 KB in, 4 KB out, and two 8 KB tool results per turn. Heavy uses 50 KB tool results.

**What ends a session.** On a 200k model the context window comes first, and every later turn
fails with the provider's error. On a 1M model, LangGraph and OpenAI Agents reach the state limit
first, because `FrameworkRunState` holds the whole conversation and is rewritten each tool round.
Superseded values are 90 to 95 percent of their budget.

**What the operator sees at the state limit.**

- Most sessions close `FAILED` (23 of 27). The rest stay alive and unusable.
- The Dex Web summary gives no reason. It is only in the last history event.
- The snapshot can stay at `RUNNING_MODEL`.
- Queued messages never run.
- A model call can be paid for and its answer discarded, for up to 125 turns.
- No API reports Flow state size, so nothing warns first.
- Nothing tried recovered a session: not raising the limit, and not Time Travel.

**A separate limit.** Python SDK gRPC channels use the default 4 MiB receive limit, and
`ClientOptions` has no setting for it. `get_agent_snapshot` fails on heavy sessions from about turn
20. A Worker with an empty blob cache loads every miss in one call, so it fails every turn of a
long Pydantic AI session. Other SDKs are not measured.

**Compaction alone does not fix this.** Stepwise with compaction reached the same limit at defaults.
It stayed flat only with `history.maxRetainedSupersededBytes` lowered to 16 MiB.

Order of work:

1. Failure handling for every runner: a recorded reason, a warning before the limit, and a Flow
   state size that an operator can read.
2. The 4 MiB limit: chunk blob loads, and make the receive limit configurable.
3. Stop rewriting the whole conversation in `FrameworkRunState`, or stop retaining its superseded
   values. This is the main cause on LangGraph and OpenAI Agents.
4. Compaction at turn boundaries for context. Turn input comes from `turn.messages`, so this looks
   easy for most adapters. Claude transcripts are the exception.
5. For long record keeping, archive old messages to a hard-delete AttributeMap, which leaves the
   budget (tested). It has no time travel and needs a blob store. Retention without a summary is
   the alternative, and it changes the rule from the M2 review.

## O4. Spend limits

"Run budget" already means the state limit in the docs, so this item is named spend
limits. Limits on tokens per turn and per session, checked at a Step boundary, where
`RunModel` already decides compaction.

Open: tokens only or cost; check before or after a call; a new interrupt kind or a
turn failure; coarse limits for the harness case; adapters that report zero; raising
a limit mid-turn.

## O5. Session scale test

Run 10,000 sessions and publish the result. The earlier proposal tied this to concurrency
groups, spend limits, and the view. This plan separates them, so the test can run
first.

Known limits it will meet: `maxInFlightRpcsPerNode`, `maxConcurrentTasksCap`, and
AgentEvents capacity, which defaults to 10 MiB shared by all sessions of a Flow type, or
about 1 KiB per session at this scale. It can be raised to 64 MiB.

Open: the mix of parked and active sessions, scripted model and which core, pass
criteria, and recovery time after a takeover. Run after #1266.

## O6. Harness segment limit

Deferred tools and the stored transcript are built for the Claude Agent SDK adapter.
What remains is the fixed `RunModel` options. A segment must finish within 15
minutes. Make the limit configurable, and move the attempts and total duration with
it. A failed long segment reruns whole.

Open: whether to record CLI built-in tools as observed entries, with no effect
guarantee.

## O7. Stream durability

Closed. The Stream primitive says it "gives up durability for speed", and
[#1275](https://github.com/superdurable/dex/pull/1275) kept memory and best effort. After a
takeover, a client loses partial text of the running attempt and the event tail. The final
message survives in Attributes, and clients recover from a snapshot. No requirement needs more.

## Prior art

Products with a similar problem. Each row says what to take and what differs.

| Product | Relevant to | What it does | Take | Differs |
| --- | --- | --- | --- | --- |
| [LangChain human-in-the-loop](https://docs.langchain.com/oss/python/langchain/human-in-the-loop) | O1, O2 | Per-tool `interrupt_on`; a `when` predicate on the call; decisions approve, edit, reject, respond | Argument predicate; allowed decisions per tool; its warning that `respond` reads as a success | Needs a checkpointer; a tool that ran can rerun |
| [LangChain Agent Inbox](https://github.com/langchain-ai/agent-inbox) | O2 | Accept, edit, respond, ignore per interrupt | Action set driven by each interrupt's config | Archived 2026-09. A separate client that kept its configuration in browser storage |
| [LangSmith Fleet](https://www.langchain.com/blog/introducing-langsmith-fleet) | O2 | One inbox across an operator's agents; only editors can review | The inbox belongs in the operator product | Hosted and tied to its own agents |
| [Claude Code permissions](https://code.claude.com/docs/en/permissions) | O2 | Allow, ask, and deny rules with argument patterns; "don't ask again" | Deny wins; rules match arguments; scope of a standing answer | One developer at a terminal |
| [n8n human review for tools](https://docs.n8n.io/advanced-ai/human-in-the-loop-tools) | O1, O2 | A reviewer gates chosen tools; approval goes out through Slack, email, or chat | Approval can reach people outside the view | Configured per workflow |
| [Composio](https://docs.composio.dev) | O1 | Many tools behind session meta tools, and schema and execution modifiers | Avoid listing every tool; let an application adjust a schema | Hosted execution, not verified from official docs here |
| [Arcade](https://docs.arcade.dev/en/guides/agent-frameworks/langchain/user-auth-interrupts.md) | O1 | Authorization as an interrupt during a tool call | A model for acting as a user in M4 | Separate from approval |

## Order and PRs

1. O3 failure handling and the 4 MiB limit. These affect sessions today, and need no O1 work.
2. Connector library: tool schema and the `agent` manifest block.
3. Cores: the `DeferredToolExecution` field, `ConnectorToolSource`, write ordering.
4. O3 state growth, once the owner answers decisions 9 and 10.
5. O2 session view, then active Step search on session Flows, then the inbox.
6. O5 after #1266. O4 and O6 as demand shows.

## Documentation

- O1: a new section in the agent deployment guide on connector tools, effect classes, and approval. Connector manifest field in `manifest-authoring.md` of the connectors library. Per-API detail in SDK doc comments.
- O2: the Dex Web reference. A new page section on the agent view, with screenshots.
- O3: the adapter READMEs and the deployment guide state the session lifetime until compaction ships.
- O5: results in `benchmark/results/` and `benchmark/README.md`.
- Pages outside the Developer reference ship in English and Simplified Chinese. A stable concept change updates docs and skills together.

## UI/UX

O2 is the UI work of this plan.

- Session view: a tab on the run page, present only for Flows with the Dex Agents schema. Each tool call shows its state, and "queued for a Worker" reads differently from "running" and "failed".
- Inbox: an agent mode of the Work Queue, one row per session that needs a person, oldest deadline first.
- Approval card: tool, arguments, effect class, deadline, approve and reject. A failed approval because another operator acted reads as "already handled", and the card reloads.
- An error while reading a session shows what is missing. It does not show an empty conversation.

Native Dex Web units are the standard. The view works with Workers down, except for actions that need an RPC, which are disabled with a reason.

## Decisions

**Decided, with reasons.**

1. **A fenced Mutation runs in `CallTool` with retries, through a context proxy in the bridge.**
   The spike showed no second write in 913 runs, and a proven-unsent write now retries.
2. **The single-dispatch fence stays.** #1253 stops the same attempt being delivered again. The
   fence stops a later attempt from sending again.
3. **High-risk operations carry an optional manifest marker.** Mutations run without approval by
   default, and only reviewed operations are offered to agents.
4. **Connector writes in one turn run in order.** A failed or unknown write stops later writes.
5. **The inbox uses `ActiveStepTypes`.** No new indexed Attribute.
6. **Python and TypeScript gain Actions.** The agent inbox does not wait for this.
7. **O7 closes.** A new requirement reopens it.

**For the owner.**

8. **Two changes to the Action route.** Return an RPC's output instead of 502, and accept an
   Action without a condition Attribute. Session RPCs check current state themselves, and the
   route is not authorization. Is that acceptable for `POST /api/v2/actions`?
9. **Superseded values in session state.** O3 shows they are most of the budget. May session
   Attributes that are rewritten each round, such as `FrameworkRunState`, opt out of keeping
   superseded values, or should the default history setting change?
10. **Long record keeping.** Archive to a hard-delete AttributeMap keeps the current retention
    rule. Retention without a summary changes it. Which does the owner prefer?

## Non-goals

A new primitive or engine feature. Connector host SubFlows for Python and TypeScript.
Acting as a user, sandbox connectors, and A2A, which stay in M4. A resolved status for
`outcome_unknown`. Multi-node server work.

## Later

Proposals from #1132, none scheduled and none reviewed by the owner. The full text is in that PR's history.

### Earlier M3 rows

- **Tool calling in the `llm` connector (G4).** Add tool definitions, tool choice, tool calls in the result, and tool-result messages for every provider in the catalog. Also reasoning items for stateless replay, reasoning summaries on a separate Stream, prompt-cache controls, and image and file inputs behind the request-feature checks. Branch table, Receipt, and retry rules stay. It could add a Go model client later.
- **Asynchronous model calls.** A Step submits a background or batch request and stores its ID in an Attribute. It moves to a Step waiting on a Channel fed by the provider webhook through the existing webhook Trigger, with a Timer poll as fallback. No Step holds a long HTTP connection, and batch pricing becomes available. Tool tasks (A8) already have the Timer half.
- **Dex MCP server (G7).** Expose session Flows and selected RPCs as MCP tools. A session maps to a task whose ID is the Flow ID: `tools/call` starts the Flow, `tasks/get` reads the snapshot, `tasks/update` publishes to the session Channel, and `tasks/cancel` calls `cancel_turn`. A pending interrupt appears as `input_required`.
- **AG-UI adapter (G6).** Snapshots from Attributes on connect, then live events from the resume token. Test: a client receives snapshots on connect.
- **Archived AttributeMap instances (G9).** A hard-delete AttributeMap already leaves the budget; see O3. A server feature moves an instance to blob storage, keeps it readable through an explicit load and the history page RPC, and stops counting it toward `limits.maxFlowStateBytes`. Retention then archives a chunk instead of deleting it. Tests: archived bytes leave the budget, explicit load reads them, history pages return them.
- **Step concurrency groups (C10).** See C10.
- **ForkFlow, counterfactual replay, trajectory export (C11).** See C11.
- **Sub-agents as tools and handoffs (C8).** See C8.
- **Step attempt spans (G10).** One span per Step attempt, with OTLP export, for every Flow, in the SDKs. It needs its own design. Agent spans become children of Step attempt spans.
- **Adapter test that restores the previous framework pin's state (C3, C14).** Each adapter restores a `FrameworkRunState` written by its previously pinned framework version. Motive: a framework release can change state or operation order under in-flight turns.
- **Usage extras (C9).** Estimated cost per model, an indexed Attribute for **SearchFlows**, the `llm` connector Receipt as a usage source, a sortable usage column in the Flow list, and a link from an activation to the tracing backend.
- **Deadline escalations and reminders (C5).** Action on an approaching deadline, beyond rejection.
- **Approvals from outside the UI (C5).** A Slack approval is a connector Trigger calling the same RPC. An MCP client answers an `input_required` task. An A2A client sends the task its input.
- **Agent memory (C7).** An `AgentMemory` interface with a session-local and a cross-session implementation through the Attribute Store and **SearchFlows**. External memory services plug in as tools.
- **Crash matrix gaps.** A deploy of a changed system prompt between two `RunModel` Steps of one turn and during an approval wait (M2 plan S-2). A cold resume after a Timer of more than a day, simulated with **SkipTimer**, with cache usage recorded. Published matrix and server-kill cells are preconditions in the M3 plan.

### Earlier M4 rows

- **Google ADK adapter for TypeScript and Go.** Same three adapter questions.
- **More framework adapters, by demand.** Microsoft Agent Framework, Strands, CrewAI, LlamaIndex Workflows. Likely state hooks: **CheckpointStorage**, **SessionRepository**, **FlowPersistence**, context snapshots. Mode decided by the three questions.
- **Go contract features (A2 to A8, MCP, spans), by demand.** Also adapters opting in to unfinished-turn messages and built-ins (M2 plan, PR 17).
- **Java and Rust cores, by demand.** Ports through the conformance suite.
- **Agent definition versions (C14).** One Flow type per version works today by convention. A version a session can pin at start, with Workers that register several versions. See question 12.
- **Cross-language Step routing for connector Steps (C4).** Routing by Step type exists; a supported contract does not. Engine change. A Flow declares a connector Step that a different Worker serves, turning per-Worker Step registration into a supported contract with registration checks, so the Step runs in the Flow's own history without a SubFlow.
- **Turn replay fallback.** For a framework that can neither stop after one model call nor take external results. `RunTurn` reruns the loop from the turn start with provider and tools replaced. A call whose key (`model#<n>`, `tool:<toolCallId>`) and request fingerprint match a recorded result returns it; the first unrecorded calls park the loop and become Steps. It needs same-order calls within a turn, and every side effect through a replaced call. Build only for a concrete framework with a reproduction.
- **A2A, sandboxes, acting for a user.** See C8, C12, C13.

### C8. Multi-agent

- **Sub-agent as a tool.** `spawn_subagent` and `subagent(definition)` start a child session Flow as a SubFlow. The parent waits in **WaitFor** on the SubFlow condition, so a child runs for days without holding a Worker. Several calls start several SubFlows. The SubFlow ID derives from parent and tool call ID, so a retried dispatch starts no second child.
- **Handoff.** Changes the active agent definition inside the same session Flow, recorded as a message. Adapters keep their framework's handoffs inside `RunModel`.
- **Interrupts across levels.** A child's interrupt is visible on the parent's snapshot and resolved by the same RPC on the child, so one UI serves the tree.
- **Peer agents.** Long-lived agents talk through RPCs and Channels on each other's Flows. Fan-out beyond `maxConcurrentStepsPerFlow` (1,000) uses the partitioning SubFlow pattern.
- **A2A.** A server adapter exposes a session Flow as an A2A agent, with the Flow ID as task ID. A client tool calls remote agents, with a polling Step for long tasks.

### C10. Budgets and model concurrency

- **Budgets (mechanics beyond O4).** Limits per turn and per session on the agent definition. Reserve the call's estimated cost against `AgentUsage` before a model call, and settle after it. An exhausted budget raises a budget interrupt, resolved by `extend_budget(interrupt_id, amount)`, instead of failing.
- **Prompt cache and long waits.** Provider caches last minutes to a day, so a resumed session pays a full cache write. `AgentUsage` records a cold resume, and an optional pre-warm call before a Timer fires trades cost for latency.
- **Step concurrency groups.** A new server feature. A Step type declares a group key and limit, such as `model:<provider>/<model>` with 50 concurrent executions across all Flows. Matching dispatches past the limit only when a slot frees, and the wait consumes no retry. The key can come from the turn's model. Tests: limit holds across Flows, waiting uses no retry, a freed slot dispatches the oldest waiter.
- Correction: the removed text said `RunModel` retries honour `Retry-After`. Source disagrees: no agent core reads it.

### C11. Fork and evals

- **ForkFlow.** A server API starts a new Flow from a history point of an existing run, with an optional override such as a model or prompt version. It copies committed state at that point and starts the chosen Step. Session settings are a natural home for the override. Test: the source run is unchanged.
- **Counterfactual replay.** A forked session in replay mode. A tool call matching a recorded name and arguments returns the recorded result. Other READ tools run live. Other writes are denied with a result saying this is a replay.
- **Trajectory export.** Export messages, tool calls, interrupts, and usage as JSONL for eval tools. It runs before chunks are deleted, or after archiving ships. It doubles as the audit export.

### C12. Sandboxes and hosted sessions

- **Sandbox as a session resource.** Providers cap continuous runtime at about 24 hours and expire snapshots after 7 to 30 days, so a session cannot own one sandbox for life. A connector interface: get or create by name, exec, read and write files, pause, resume, snapshot, delete.
- **Lifecycle.** The handle lives in an Attribute: provider, name, snapshot ID, lease expiry. The name derives from the Flow ID, so creation is idempotent. Pause on a long wait, resume on wake, and a Timer renews a snapshot. A finalizer deletes the sandbox and adds its cost to `AgentUsage`. Outputs that matter are Step results or blobs, never only sandbox files.
- **Hosted agent sessions.** A session Flow drives a vendor session as a tool or a whole turn: start or resume idempotently, store its ID in an Attribute, wait for its webhook on a Channel. Dex owns schedules, long approvals, budgets across sessions, and audit history.

### C13. Identity and policy

- **Acting for a user.** A session records the end user and tenant. Tools fetch that user's token at call time from a connection or external vault. A failed refresh or a scope request raises a re-authorization interrupt.
- **Policy hook.** Before each tool call, a function receives tool, arguments, effect class, principal, and session state. It returns run, ask, or deny, stored with the call. O2's "Conditional approval" lists related candidates but does not design this.
- **Audit.** Flow history and stored messages record what the agent saw and did, within retention. Trajectory export is the audit export.

### Unresolved open questions

- **11. Serverless Workers (G16).** Scaling Workers to zero on queue metrics is documented; RPCs still fail with no Worker. Pull Workers are the only mode since #1023. Builders on Vercel, Cloudflare, and Lambda expect push or scale-to-zero. Needs a separate decision.
- **12. Version pinning default.** Following the latest definition is the Dex default; pinning protects a session from a prompt or tool schema change. Recommendation: follow latest, with pinning per session through session settings.
- **15. Budget ownership with a gateway.** When a gateway such as LiteLLM also enforces budgets, which one is authoritative by default. One owns enforcement and the other reports.
- Questions 10 and 14: covered by O3 and O2 (see top).
- Question 13: resolved (#1275).
