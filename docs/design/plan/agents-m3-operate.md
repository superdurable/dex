# Agents M3: Connector Tools, the Operator View, and Session Lifetime

Status: proposed, design only. Nothing here is approved or built.
Parent design: `long-running-agents.md` on [#1132](https://github.com/superdurable/dex/pull/1132),
which is open and not on main. Its M3 and M4 milestones are proposals.
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

## Preconditions

M3 implementation starts after these M2 items close. Design can proceed now.

| Item | State on 2026-11-06 |
| --- | --- |
| npm packages at 0.2.0 | `latest` is 0.1.0. The publish failed twice; #1317 fixed the first failure, and the second ended in `E404` |
| Go core tag | Untagged. The tag scheme needs the owner (`agents-go-java-rust.md`) |
| Published crash matrix | Not published. Worker-kill tests exist per core. [#1282](https://github.com/superdurable/dex/issues/1282) is open, and no agent-level test restarts or fails over the server |
| Cost of the SYNC start commit | Not measured. [#1266](https://github.com/superdurable/dex/issues/1266) is open |

## Relation to the parent's M3

This plan is not built on the parent's M3, and no item here waits for another M3
row. It takes a subset of that milestone, adds items the parent lacks, and leaves
the rest proposed.

| Parent M3 row | Here |
| --- | --- |
| Engine fix for single-attempt exactly-once | Done (#1253, #1265) |
| Enforce `limits.maxFlowStateBytes` | Done (#1234) |
| Dex Web agent view | O2 |
| Budgets, cost estimates, and budget interrupts | O4 |
| Exit: load test of 10,000 sessions | O5, separated from concurrency groups, limits, and the view |
| Connector host SubFlows | Dropped: Python and TypeScript Connector packages exist |
| Tool calling in the `llm` connector | Out of scope. Stepwise calls models in-process, so O1 does not need it |
| Step concurrency groups, archived AttributeMap instances, ForkFlow, sub-agents, Dex MCP server, AG-UI, asynchronous model calls, trajectory export, Step attempt spans, adapter pin test | Out of scope, still proposed in the parent |

Not in the parent's M3: connector tools as a tool source (O1), adapter session
lifetime (O3), and the harness segment limit (O6).

## Scope

| Item | What | Depth here |
| --- | --- | --- |
| O1 | Connector operations as agent tools | Design |
| O2 | Operator view and inbox in Dex Web | Design |
| O3 | Adapter session lifetime | Scope, urgent |
| O4 | Spend limits | Scope |
| O5 | Session scale test | Scope |
| O6 | Harness segment limit | Scope |
| O7 | Stream durability | Requirement first, needs an owner decision |

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
`AsyncContext`, because the single-dispatch fence reads and writes heartbeat
checkpoints through it, and `Call.id` needs the Step execution ID. Today the
source receives neither. Add an optional field that exposes the Step's context to
a tool source. It is additive and absent from persisted values.

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

The third row differs from the parent design's C4, which maps any Mutation with
`uncertain` to `NON_IDEMPOTENT_WRITE`. A fenced Mutation is safe to retry, because
the fence refuses a second send and reads back instead. If it ran in
`CallSingleAttemptTool`, a Worker death would record `outcome_unknown` where a
retry and read-back could have settled the outcome.

**Approval is a separate question from retry.** Approval asks whether the call
should happen. Idempotency does not answer that: a refund with an idempotency key
is still a real refund. Today `ToolPolicy` derives approval from the effect class,
so `IDEMPOTENT_WRITE` needs no approval. That is right for most writes, because
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
`dex-connector.json`, generated from the same models, plus an optional
model-facing description. Input field comments can seed it. Generating in each
agent core at run time would give three implementations of one mapping.

**Step options.** Tool Steps are SYNC with fixed options. Manifest `execution` may
say `durability: async` and a 30-second timeout. Keep SYNC, because ASYNC reopens
the double-run residual in the deploy guide. Carry `executeMethodTimeout` and retry
as movement-scoped options: `StepMovement.of` accepts options in Python and
TypeScript. An earlier Go agent implementation used one tool Step with movement-scoped
options and per-tool policy successfully.

**Write ordering.** Tool calls of a turn run in parallel today. An agent that posts
and then refunds in one turn has an order the model intended. Proposed: writes run
one at a time in the model's order, and reads stay parallel. `RunModel` schedules
the waves. Locks are not the way: "Locks do not queue", and a parked Step has a
per-second cost ([#1185](https://github.com/superdurable/dex/issues/1185)).

**Connections.** The application binds a logical connection at Worker start, as
Connector Steps do. A per-session choice is a connection name in `session_settings`.
Acting as a specific user is M4.

**Adapters and languages.** `ToolSource` is Stepwise only, and Go has function tools
only. O1 ships for Stepwise in Python and TypeScript first. Adapters then need a
helper that exposes the same tools in each framework's format. Go follows once its
scope is decided. Connector packages exist for all three languages, so the parent
design's Connector host SubFlows are not needed for Python and TypeScript.

**Result size.** Query output goes to the model and into Flow state. Bound it per
tool, and truncate with a note. This needs a default.

### Exactly-once

| Failure | What happens |
| --- | --- |
| Worker dies before the provider call, query or idempotent write | Attempt fails `NOT_COMPLETED`, Execute retries, same key |
| Worker dies during a fenced write | Next attempt sees the claim, reads back, and either settles or records `outcome_unknown` |
| Worker dies during a single-attempt write | `outcome_unknown` |
| Tool Step never reaches a Worker | Waits in the durable queue |
| `cancel_turn` during a started call | The Step runs to its end, as for any synchronous tool |

**Risk to test first.** The core records heartbeats before the tool and in the
background. The fence reads the last heartbeat value and fails closed on a value it
cannot read. A background heartbeat must not clear or replace the fence's marker,
and a core heartbeat must not read as a foreign marker. If they conflict, the fence
marker goes through `save_heartbeat_checkpoint`, so background heartbeats carry it.

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

Dex Web has run search, the Flow canvas, the timeline, state, Streams, Time Travel,
Actions, and a Work Queue. `actionableSteps.ts` derives Steps that wait for a person
from the Flow Definition Graph. The session view therefore needs little new data.
The inbox needs real work.

### Session view

A tab on the run page, shown for Flows with the Dex Agents schema.

- **Conversation:** messages and tool calls in order, each linked to its Step
  execution. Each call shows arguments, result, effect class, and approval decision.
- **State of each call:** queued for a Worker, waiting for approval, running,
  finished, or unknown. Since #1259 a tool Step can wait a long time for a Worker,
  and a Flow timeout or stop skips the failure route. Without this state, a queued
  call looks stuck.
- **Controls:** approve, reject, answer, steer, stop, through the session's RPCs.
- **Usage:** tokens from `AgentUsage`.

**Read path.** `get_agent_snapshot` is an RPC and needs a Worker. An operator most
needs this page when Workers are down. The view should read Attributes directly and
treat the snapshot as optional. This keeps schema knowledge in Dex Web, so the schema
needs a stated compatibility rule.

### Inbox

The inbox answers one question across sessions: what needs a person? Nothing does
this today, for three reasons.

1. **Nothing to query.** Search runs one Flow type at a time over declared indexed
   Attributes, and agent persistence declares none. Add one small indexed Attribute,
   such as `AgentAttention`, with the current need: `none`, `approval`, `question`,
   or `outcome_unknown`. Scanning `AgentToolCalls` is not an option, because reading
   the size or keys of an AttributeMap loads the whole map.
2. **Actions are Go only.** An Action is a typed Go registration. Most agents are
   Python or TypeScript, and `resolve_interrupt` is not an Action. Either the SDKs
   gain Action registration, or Dex Web calls known agent RPCs under a stated
   permission.
3. **The Work Queue needs a graph.** It reads a Flow Definition Graph, and no graph
   exists for the session Flow. `dexcli visualize` on a library-built Flow is
   untested.

Proposed: extend the Work Queue with an agent mode rather than add a surface.

**Rules for `AgentAttention`.** It is a new persisted, indexed Attribute, so it needs
the rollout rules. Each Step that raises or clears an interrupt writes it in the same
commit. Index keys come from a tenant-wide budget: the PostgreSQL backend allows 10
KEYWORD keys per tenant, so one key must serve all agents.

**Races.** Two operators approve the same call. `resolve_interrupt` locks
`PendingInterrupts`, and locks do not queue, so one can fail. The UI treats that as
"already handled", then reloads. A deadline can reject a call while an operator
decides, so the result must be read back from committed state.

**`outcome_unknown` in the inbox.** Today an operator checks the target system and
tells the session with `send_user_message`. The inbox can offer that as a prompt.
Adding a resolved status conflicts with Principle 4, so it stays out of scope unless
partners show the need.

### Conditional approval

Approval today is per call, and policy is fixed at build time: always or never. For
an always-on agent, this leaves no middle ground. Other products have one (see
Prior art): rules that match arguments, "allow for this session", and editing the
arguments before approving.

This plan does not design it. It is listed because the decision affects `ToolPolicy`
and the interrupt shape, both of which are hard to change later.

| Candidate | Change |
| --- | --- |
| Predicate on arguments, such as a refund under a limit | `ToolPolicy` gains a Worker-side function. No persisted change |
| "Allow for this session" | Session state read by `RunModel` |
| Edit arguments before approval | `ResolveInterruptRequest` gains replacement arguments |

## O3. Adapter session lifetime

Compaction exists for Stepwise only. Retention deletes a chunk only once its messages
are summarized, and adapters never summarize. So retention never runs on adapter
sessions, and their stored messages grow until `limits.maxFlowStateBytes`, which is
enforced since [#1234](https://github.com/superdurable/dex/pull/1234). This is an
inference from those facts, not a test result. First, measure it: run each adapter
to the limit, and record `FrameworkRunState` growth.

Open:

1. Implement `ContextCompactionRunner` on the main adapters, or state a lifetime.
2. How a summary enters each framework's input. Message-list frameworks look easy;
   `RunState` and graph checkpoints do not.
3. Framework-native compaction against a Dex summary: which owns the record?
4. Retention without a summary for adapters changes an owner rule.

## O4. Spend limits

"Run budget" already means the state limit in the docs, so this item is named spend
limits. Limits on tokens per turn and per session, checked at a Step boundary, where
`RunModel` already decides compaction.

Open: tokens only or cost; check before or after a call; a new interrupt kind or a
turn failure; coarse limits for the harness case; adapters that report zero; raising
a limit mid-turn.

## O5. Session scale test

Run 10,000 sessions and publish the result. The parent design ties this to concurrency
groups, spend limits, and the view. This plan separates them, so the test can run
first.

Known limits it will meet: `maxInFlightRpcsPerNode`, `maxConcurrentTasksCap`, and
AgentEvents capacity, which is 10 MiB shared by all sessions of a Flow type, or about
1 KiB per session at this scale.

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

No design yet. The Stream primitive says it "gives up durability for speed", and
[#1275](https://github.com/superdurable/dex/pull/1275) kept memory and best effort.
Clients recover from a snapshot. After a takeover, a client loses partial text of the
running attempt and the event tail, and the final message survives in Attributes.

Needs: a stated requirement that this does not meet. Without one, this item closes.

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

1. Spike O1-3 and O1-4 against the fence. This decides the effect mapping.
2. Connector library: tool schema and the `agent` manifest block.
3. Cores: the `DeferredToolExecution` field, `ConnectorToolSource`, write ordering.
4. O3 measurement, in parallel.
5. O2 session view, then `AgentAttention`, then the inbox.
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

## Decisions for the owner

1. **Fence and effect class.** May a fenced Mutation run in `CallTool` with retries?
2. **Fence after #1253.** The engine now refuses to rerun a started attempt under the
   same number. Should the fence stay as it is?
3. **Approval marker.** Add an optional manifest field for high-risk operations,
   leaving the core's rule unchanged?
4. **Write ordering.** Run writes of one turn in order, instead of in parallel?
5. **Actions.** Port Action registration to Python and TypeScript, or let Dex Web call
   known agent RPCs?
6. **Indexed Attribute.** Add `AgentAttention` to the persisted schema?
7. **Retention.** May adapters have retention without a summary?
8. **Streams.** Is there a requirement for O7, or does it close?
9. **Parent design.** #1132 still has changes requested. Should this plan wait for it?

## Non-goals

A new primitive or engine feature. Connector host SubFlows for Python and TypeScript.
Acting as a user, sandbox connectors, and A2A, which stay in M4. A resolved status for
`outcome_unknown`. Multi-node server work.
