# Agent Framework Adapters

Status: in progress. Parent design: [long-running-agents.md](../long-running-agents.md).
Direction: the [owner review of #1132](https://github.com/superdurable/dex/pull/1132#pullrequestreview-5724027276)
and its follow-up comments ([turn replay](https://github.com/superdurable/dex/pull/1132#issuecomment-6546651218),
[Step mode](https://github.com/superdurable/dex/pull/1132#issuecomment-6547044614)).

The adapters run in **Step mode**. Every model call is one Dex Step, and every
tool call is its own Step, READ tools included. The tool calls of one model
response run as parallel Steps, and a `ResumeTurn` Step waits for their results
before the next model call. Each call therefore has its own retry policy,
timeout, failure route, history entry, and Dex Web node. A crash repeats at most
the one call that was running.

Dex adds no new primitive for this. There is no Step Journal, and no framework
loop runs several model calls or inline tools inside one Step. Every guarantee
rests on ordinary Steps, Attributes, Channels, and Timers.

## Scope

| Package | Language | Framework |
| --- | --- | --- |
| `agents/python/core` (`dex-agents`, import `dex_agents`) | Python | Shared session Flow |
| `agents/python/openai-agents` (`dex-agents-openai-agents`, import `dex_agents_openai_agents`) | Python | OpenAI Agents SDK |
| `agents/python/pydantic-ai` (`dex-agents-pydantic-ai`, import `dex_agents_pydantic_ai`) | Python | Pydantic AI |
| `agents/python/langgraph` (`dex-agents-langgraph`, import `dex_agents_langgraph`) | Python | LangChain `create_agent` and LangGraph |
| `agents/python/google-adk` (`dex-agents-google-adk`, import `dex_agents_google_adk`) | Python | Google ADK |
| `agents/python/claude-agent-sdk` (`dex-agents-claude-agent-sdk`, import `dex_agents_claude_agent_sdk`) | Python | Claude Agent SDK |
| `agents/typescript/core` (`@superdurable/dex-agents`) | TypeScript | Shared session Flow |
| `agents/typescript/vercel-ai` (`@superdurable/dex-agents-vercel-ai`) | TypeScript | Vercel AI SDK |
| `agents/typescript/openai-agents` (`@superdurable/dex-agents-openai-agents`) | TypeScript | OpenAI Agents SDK |
| `agents/typescript/mastra` (`@superdurable/dex-agents-mastra`) | TypeScript | Mastra |
| `agents/typescript/claude-agent-sdk` (`@superdurable/dex-agents-claude-agent-sdk`) | TypeScript | Claude Agent SDK |

Each package has its own manifest and lockfile, so adapters pin framework
versions independently and never share a lock. An adapter depends on its
language core through a path dependency (`{ path = "../core", editable = true }`
for uv, `"file:../core"` for npm). Every package depends on the released Dex SDK
from the public registry, never on `sdk-python/` or `sdk-typescript/` by path:
Python `dex-python-sdk==1.30.0`, TypeScript the latest released
`@superdurable/dex` on npm.

Source files use the Sustainable Use License header (`new`), the same as
`sdk-python/dex`. `agents` is in `included_prefixes` and `forced_new_prefixes`
in `script/licenseheaders/policy.json`.

## Model

One Flow execution is one agent session. The Flow ID is the session ID. Each
adapter registers the shared session Flow with its own framework runner. An
application can register several agents; each gets its own Flow type.

```python
flow = AgentSessionFlow(PydanticAiRunner(agent), flow_type="TriageAgent")
registry = Registry([flow], allow_async_handlers=True)
```

A turn starts when the session consumes a user message and ends when the model
produces a final answer. Within the turn, the state of the agent loop is the
message list plus the framework's own run state. A runner loads that state,
lets the framework make **exactly one model call**, and stops it before it runs
any tool. If the response asks for tools, the runner returns the framework's
serialized run state and the tool calls. The session stores both, runs each
tool call in its own Step, and calls the runner again with the final tool
results:

```text
RunModel -> tool calls requested -> CallTool, CallTool, AwaitToolApproval -> ... (parallel)
         -> ResumeTurn (waits for every result) -> RunModel -> ... -> final answer
```

A turn with two model calls and one READ tool produces
`RunModel -> CallTool -> ResumeTurn -> RunModel`. Nothing re-runs the loop, so
application code follows no determinism rule. A code change takes effect at the
next Step. The framework keeps its own features, such as handoffs, guardrails,
and structured output, because it still makes each model call itself.

**Turn replay** is a documented fallback for a framework that cannot stop after
one model call or cannot accept external tool results. It re-runs the framework
loop from the start of the turn in every model Step, returns recorded results
for calls already made (keyed `model#<n>`, `tool:<toolCallId>`,
`approval:<toolCallId>`), and parks at the first unrecorded call. No adapter in
this plan needs it, and the cores do not implement it.

## Integration modes

| Framework | Mode | Stop after one model call | Run state | Feed tool results back |
| --- | --- | --- | --- | --- |
| Pydantic AI | Step mode | Deferred tools (`ExternalToolset` or `CallDeferred`, with `DeferredToolRequests` in `output_type`) end the run right after the model call | Message history as `ModelMessagesTypeAdapter` JSON plus the pending requests | `agent.run(..., message_history=..., deferred_tool_results=DeferredToolResults(calls={tool_call_id: value}))` |
| Vercel AI SDK | Step mode | Tools without `execute` return their calls from `generateText`; `stopWhen: isStepCount(1)` (`stepCountIs` before v7) | The plain-JSON `ModelMessage[]` | Append a `tool` message with `tool-result` parts and call `generateText` again |
| LangGraph / LangChain `create_agent` | Step mode | An interrupt for every tool call: `HumanInTheLoopMiddleware`, or `wrap_tool_call` middleware that calls `interrupt()` | Graph checkpoint, from a Dex `BaseCheckpointSaver` kept in `FrameworkRunState` | `Command(resume=...)` whose decision supplies the recorded result |
| Google ADK | Step mode | A confirmation for every tool call: `FunctionTool(require_confirmation=True)` or `tool_context.request_confirmation()` | Session events with `ResumabilityConfig(is_resumable=True)` | The confirmation response, sent with the original `invocation_id`, carries the recorded result; the prototype confirms the details |
| Mastra | Step mode | Client tools: the agent stops with their calls instead of running them | Agent messages | Send the tool results back as client tool results and call the agent again |
| OpenAI Agents SDK | Step mode | A `needs_approval` gate on every function tool; the run stops with `result.interruptions` after one model call | `RunState.to_json()` / `RunState.from_json()`, which also holds the current agent after a handoff | Approve each interruption whose record is final, and the tool wrapper's `on_invoke_tool` returns the Dex result instead of running the tool; reject with the rejection message |
| Claude Agent SDK | Whole-turn harness | No clean stop: the CLI runs the turn's model calls and tools itself | Session transcript through `SessionStore`, resumed by session ID | No injection; tools run inside the harness |

Notes:

- OpenAI Agents SDK handoffs are not function tools. The SDK runs a handoff
  itself and continues with the next agent's model call, so a `RunModel` that
  hands off makes more than one model call. The adapter documents this.
- The whole-turn harness runs one turn inside one `RunModel` Step. Its tools
  run in the harness and are at-least-once: a `RunModel` retry repeats them.
- Each adapter documents every tool kind it cannot defer and treats it as
  at-least-once.

## Effects and policies

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

`ToolPolicy(effect, requires_approval)` overrides the defaults per tool name;
an unset `requires_approval` uses the effect's default. A session-level
`default_tool_effect` (default `unclassified`) applies to tools without a policy
or framework metadata. Adapters read framework metadata where it exists, such as
MCP `readOnlyHint` (`read`) and `idempotentHint` (`idempotent_write`)
annotations. No tool runs inside `RunModel` unless the framework cannot defer
it.

The idempotency key passed to `execute_deferred_tool_call` is
`<flowId>/<turnId>/<toolCallId>`, the same on every attempt. Tool call IDs must
be non-empty, contain no `/`, and be unique within one turn; they may repeat
across turns.

### Known limits

The table promises that a `CallSingleAttemptTool` call runs at most once and
records `outcome_unknown` when it may have run. Two cases break that promise.
Both are documented for operators in the
[deployment guide](../../content/production/agents.mdx#tool-call-limits), and
their fix is deferred until after M2.

- **A single-attempt call can run twice.** Dex starts an attempt when it
  hands the attempt to a Worker, and it never hands the same attempt number to
  a second Worker. A Worker that dies, or loses its stream, after it received
  the tool Step fails the attempt, and the call records `outcome_unknown`. The
  tool Step still heartbeats before it calls the tool. Earlier Dex Servers
  redelivered an attempt at the same attempt number when its Worker's stream
  ended with no output, and the heartbeat narrowed that window. Two narrow
  cases remain. An ASYNC Step can run again at the same attempt number when a
  Dex Server node crashes before the attempt start is durable; the tool Steps
  use SYNC durability, so this case does not apply to them. Cancellation is
  cooperative, so a cancelled run may keep running while the failure route
  starts.
- **A call that never ran records `outcome_unknown`.** When no Worker accepts
  the tool Step within the dispatch wait, because every Worker is at its task
  limit or none is running, the attempt fails without running and still
  consumes an attempt. `CallSingleAttemptTool` has one attempt, so
  `RecordToolOutcomeUnknown` records `outcome_unknown`. `RecordToolFailure`
  does the same for an `idempotent_write` whose last attempt fails this way.
  The failure route cannot tell this apart from a Worker that disconnected
  after the tool started: both reach it with the recovery error type
  `FLOW_ERROR_TYPE_WORKER_API_FAIL`, and only the detail text differs.

## MCP tool source

Parent design: C4, MCP as a tool source. Both cores ship an MCP client as an
optional part: `dex_agents.mcp` behind the `dex-agents[mcp]` extra, and the
`@superdurable/dex-agents/mcp` subpath with `@modelcontextprotocol/sdk` as an
optional peer dependency. Neither core's main entry point imports an MCP SDK.

- `McpToolSource` takes stdio and Streamable HTTP server configurations from
  the Worker, never from Flow input, a user message, or a browser. It lists
  and caches each server's tools on their own as agent tool definitions named
  `<server>__<tool>`; a server that cannot be listed, and a tool whose name
  collides with an earlier one, are left out with a warning.
  `deferred_tool_call` resolves each call's policy with the annotation effect
  as `framework_effect`: `readOnlyHint` is `read`, `idempotentHint` is
  `idempotent_write` whatever `destructiveHint` says, and an explicit
  `ToolPolicy` wins. Annotations are trusted by default.
- `deferred_tool_call` stores the server and the tool's MCP name in the new
  `DeferredToolCall.tool_source_reference`, which the session keeps on the
  `AgentToolCall` record (`tool_source_reference`), so the tool Step calls that
  server without a listing.
- Calls run only in tool Steps, through `execute_deferred_tool_call`. Every
  request opens a new connection, with the handshake bounded at 30 seconds by
  default, so a server restart between listing and calling is harmless.
  `tools/call` carries the idempotency key in `_meta` under
  `io.superdurable.dex/idempotencyKey`. The TypeScript source ends each HTTP
  protocol session with a `DELETE`.
- An `isError` result, a JSON-RPC error other than `-32603`, and a call that
  was never sent raise `ToolExecutionError`. A request sent without a result
  back raises `McpToolCallInterruptedError`, a Step failure, so the effect
  class decides a re-issue: `CallTool` sends a new request for `read` and
  `idempotent_write`, and `CallSingleAttemptTool` records `outcome_unknown`.
  The MCP SDKs also report an HTTP error status, and Python an unreadable
  body, as `-32603` and `-32700`; a gateway can send those after the server
  ran the call, so they count as no result.
- A task handle from `io.modelcontextprotocol/tasks` (Python) or the SDK's
  2025-11-25 tasks (TypeScript) of a Streamable HTTP server is first saved as
  the attempt's heartbeat checkpoint: the new
  `DeferredToolExecution.save_heartbeat_checkpoint` records it, every later
  heartbeat of the attempt repeats it, and a retried attempt reads it as
  `heartbeat_checkpoint` and returns the same task without calling again. Once
  that checkpoint reaches Dex, a Worker kill after `tools/call` creates no
  second task. Dex may drop a heartbeat checkpoint (see Known limits), so an
  attempt that dies before its checkpoint is saved and before the tool Step
  commits can send `tools/call` again on a `CallTool` retry; the idempotency
  key in `_meta` lets the server drop the duplicate. Each source reads only a
  checkpoint it wrote, so another writer's checkpoint reads as none.
- A source called without `DeferredToolExecution.supports_tool_tasks`, for a
  runner without `poll_tool_task`, polls every task inside the tool Step, as
  `call_tool` does, instead of starting one nobody polls.
- A task that any connection can poll becomes `ToolTaskStarted` (see
  `PollToolTask`): a Python Streamable HTTP server's task, and a TypeScript
  one when the server keeps no protocol session. `poll_tool_task` sends one
  `tasks/get` per poll and writes an `mcp.task_status` event only when the
  status changed. The next interval is the server's poll interval,
  rounded up to whole seconds, or, when that is shorter, double the previous
  interval, up to a minute, to bound a long task's Steps. TypeScript requests
  a task TTL of `taskTtlMs`, 24 hours by default, because the poll spans
  Worker restarts; Python lets the server choose. A task the server no longer
  knows, or that three poll attempts cannot reach, goes to the call's failure
  route by effect. `cancel_tool_task` (`cancelToolTask`) sends `tasks/cancel`
  for a task that `cancel_turn` abandoned.
- A task that only its own connection can poll is polled with `tasks/get`
  inside the tool Step, with a heartbeat per poll, for a bounded wait per
  attempt (10 minutes by default): a stdio server's task, which dies with its
  process, and in TypeScript a stateful server's task, which the SDK's task
  store binds to the protocol session that the request ends. TypeScript
  requests its TTL as four times the wait. A retried attempt of a
  session-bound task cannot see it and calls the tool again.
- In Python, a task in `input_required` that asks one form elicitation with a
  single string, `enum`, or boolean field becomes `ToolInputRequested` from
  `poll_tool_task`. Its continuation names the request: its key and a SHA-256
  fingerprint of the request. The first poll after the question is resolved
  sends the resolution with `tasks/update`, only on its first attempt, after a
  heartbeat, and only while the task still lists that same request: an
  `accept` input response for an answer, `decline` for a declined question,
  and `cancel` for a passed deadline. A different later request under the same
  key never gets an old answer. A retried attempt cannot tell whether the
  first one sent it, and the task may since have asked the identical request
  again, so it returns the question again and the user answers anew. After a
  send, the poll returns `ToolTaskPending` with the continuation plus the
  Worker clock's send time while the task still lists the request, and polls
  a second apart read that request as working for 10 seconds after the send.
  A poll that sees the task leave `input_required`, or the request change,
  returns no continuation, so an identical later request, such as one per
  batch, is asked as a new question; so is one still listed after those 10
  seconds. When `tasks/update` fails for a decline, or
  the task is cancelled after one, the source cancels it best-effort and
  fails the poll, so the call is recorded by effect. Any other input request,
  any TypeScript task input request (2025-11-25 input requests must be
  answered on a live connection), and an `input_required` answer to
  `tools/call` itself record `failed` with text naming the requested input,
  and a waiting task is cancelled.
- The Python SDK (`mcp` 2.3) speaks 2026-07-28; the source adds the tasks
  extension, which the SDK does not implement. The TypeScript SDK 1.x speaks
  up to 2025-11-25, so the TypeScript source uses the initialize handshake per
  request and the SDK's 2025-11-25 tasks for `taskSupport: required` tools.

Follow-ups:

1. **Question interrupts outside tasks.** An `input_required` answer to
   `tools/call` raises a session question interrupt per input request instead
   of failing. The answer RPC resumes the call, and the tool Step retries
   `tools/call` with `inputResponses` and the echoed `requestState`.
2. **MCP 2026-07-28 in TypeScript** when an `@modelcontextprotocol/sdk` release
   speaks it, which also lets TypeScript task input requests become questions.

Framework adapters use the source when they accept tools defined by a JSON
Schema: each MCP tool becomes a framework tool that calls `call_tool` with the
idempotency key from the adapter's tool context, and `tool_policies()` feeds
the adapter's `tool_policies`. The Claude Agent SDK harness runs read tools
inline, so only its write calls become Steps.

## Session Flow contract

Names here are wire contracts shared by Python and TypeScript, so a UI or Dex
Web view can read any agent session. JSON field names are snake_case in both
languages.

### Steps

| Step type | Input | Kind | Next |
| --- | --- | --- | --- |
| `StartAgentSession` | `AgentSessionStartInput` | Start Step. Initializes `AgentState` (`next_message_sequence` 1), `AgentUsage`, and an empty `PendingInterrupts`, and stores non-empty `session_settings` in `SessionSettings`; settings it refuses force-fail the session with a reason starting `dex-agents: the start input's session_settings are not valid session settings`. With a non-blank `initial_message`, appends it as the turn's user message. | `RunModel` when `initial_message` is set and not blank, else `AwaitUserMessage` |
| `AwaitUserMessage` | none | WaitFor any of `TurnControlRequests.for_range(at_least=1, at_most=MAX)`, `QueuedUserMessages.for_one()`, and, while `AgentPlan` is `draft` or `active`, `PlanExecutions.for_one(<revision>)`, in that order; Dex selects Channel conditions in argument order, so a steer beats a queued message, which beats a plan execution. Execute locks `TurnCancellationRequest`, so no turn starts while `cancel_turn` decides. A control wake: Execute moves to `ApplyTurnControl`. A message: Execute appends it and starts a turn whose `interaction_mode` is `planning` when plan mode is enabled and the message has `plan_mode` or a `draft` plan exists, else `chat`. An approval: Execute deletes `PendingPlanExecution`; unless the plan still has that revision and is `draft` or `active`, it waits again; otherwise it marks the plan `active`, appends `[Dex] The user approved plan revision <n>. Execute it and keep the task statuses current with write_todos.` through `build_user_message`, and starts an `executing` turn, writing `turn_started` then `plan_execution_started`. | `ApplyTurnControl` on a control wake, else `RunModel`, or itself |
| `RunModel` | none | Async Execute. Before the model call, when the turn is steered or cancelled, it ends the turn in this commit, as `ApplyTurnControl` does, and moves to `ApplyTurnControl` without a model call, which starts the steered turn. `cancel_turn` cancels a running `RunModel`: Python cancels the coroutine, TypeScript aborts `cancellationSignal`, and the model call's result is dropped. Otherwise, with `AgentSessionOptions.compaction`, it asks the runner for `estimate_input_tokens`; at `trigger_ratio` of the window it asks for `select_compaction_cutoff`, and a cutoff after the current `ContextSummary` makes it set status `compacting_context` and move to `CompactContext` with no model call. These are its only decision points, in that order. Otherwise it calls `run_model_step` for one model call. Options: method timeout 15 min, heartbeat timeout 2 min, retry 3 attempts within 30 min, failure route `RecordTurnFailure`. | Compaction: `CompactContext({through_sequence})`. `TurnCompleted`: appends `new_messages`, clears the turn, `AwaitUserMessage`. `ToolCallsRequested`: appends `new_messages`, stores `FrameworkRunState`, one `AgentToolCalls` record per call, and the interrupts, then `go_to_many` with one Step per call plus `ResumeTurn(<call count>)`. A runner that sets `new_messages` without implementing `build_unfinished_turn_messages` fails the turn in this commit, with `last_turn_error` `a runner that sets ToolCallsRequested.new_messages must implement build_unfinished_turn_messages` (TypeScript names `newMessages` and `buildUnfinishedTurnMessages`) and no retry. It records the model call's usage first. An `ask_user` call is recorded `awaiting_answer` with a `question` interrupt and goes to `AwaitQuestionAnswer`; a `durable_wait` call is recorded `waiting_for_timer` with `wait_until`, writes `timer_started`, and goes to `AwaitTimer`; a `write_todos` call replaces `AgentPlan`, writes `plan_updated`, and records `succeeded`; an invalid built-in call records `failed`; in a `planning` turn every call but `write_todos` records `rejected` with `The tool call was not run: a planning turn can only call write_todos.`. A call that becomes final here publishes its result in this commit, and `ResumeTurn` still counts it |
| `CompactContext` | `CompactContextInput` | Async Execute with `RunModel`'s options and failure route. Loads only the messages it summarizes. Writes `compaction_started`, calls `summarize_context` once with the previous summary and the messages after it through `through_sequence`, then in one commit writes `ContextSummary` (generation + 1, `first_sequence` 1, `last_sequence` `through_sequence`), records the call's usage, sets status `running_model`, and writes `compaction_finished`. A crash reruns only the summary call. A blank summary fails the attempt. | `RunModel` |
| `AwaitToolApproval` | tool call ID | WaitFor any of `ToolCallCancellations.for_one(id)`, `InterruptResolutions.for_one(id)`, and, while the interrupt is in `PendingInterrupts` with a deadline, a Timer at that deadline, rounded up to whole seconds because Dex Timers take whole seconds. Execute locks `PendingInterrupts`. `cancel_turn` cancels it, and `FinishTurnCancellation` records its call. | Approved: records `approved`, then `CallTool` or `CallSingleAttemptTool` (id). Rejected or deadline passed: records `rejected`, publishes `ToolCallResults`, ends (dead end). Timer fired after `resolve_interrupt` already removed the interrupt, or before the deadline: itself (id), which waits again. A cancellation wake: see the steering rules below |
| `AwaitQuestionAnswer` | tool call ID | WaitFor any of `ToolCallCancellations.for_one(id)`, `InterruptResolutions.for_one(interrupt_id)`, and, while the interrupt is pending with a deadline, a Timer at that deadline; while a tool's question is neither pending nor answered, a zero Timer instead. Execute locks `PendingInterrupts` and raises such a tool question from the record (`interrupt_raised`), then waits again; in a steered or cancelled turn it records the call cancelled instead and asks nothing, unless the record has `task_id`; in a cancelled turn a record with `task_id` moves to `FinishTurnCancellation` instead. `cancel_turn` cancels a waiting `AwaitQuestionAnswer`, and `FinishTurnCancellation` records its call. When the tool question's ID `<tool_call_id>-input-<n>` is already the ID of a pending interrupt of another call, or of another call's record, it records the asking call `failed` with `the tool's question cannot use interrupt ID <interrupt_id>, which another tool call of this turn uses` and raises nothing. Answered `ask_user`: records `succeeded` with `{"status":"answered","answer":"<text>"}`. Answered tool question: appends a `QuestionAnswer` to `input_answers` and records `approved`. A question that a started task asked (the record has `task_id`) never ends the call here: an answer, a decline, and a passed deadline each append a `QuestionAnswer` with `resolution` `answered`, `declined`, or `deadline_passed` (the answer, the decline text, or `question deadline passed` in `answer`), set `task_answer_pending`, record `waiting_for_task`, and move to `PollToolTask`, which hands the resolution to the task; such a call whose question ID collides is recorded by effect instead of `failed`. Declined `ask_user` or tool question: `rejected` with the message or `The user declined to answer.`. Deadline: `rejected` with `question deadline passed`. | Ends, back to the call's tool Step or `PollToolTask`, or itself after an early Timer |
| `AwaitTimer` | tool call ID | `cancel_turn` cancels it, and `FinishTurnCancellation` records its call. WaitFor any of a Timer for the remaining whole seconds until `wait_until` and `ToolCallCancellations.for_one(id)`; Dex selects Timers before Channels, so a wait that ends as a steer arrives completes normally. Execute records `succeeded` with `{"status":"completed","waited_seconds":<n>,"reason":"<reason>"}` and publishes `ToolCallResults`. The Timer is the authority, so `SkipTimer` ends the wait. | Ends |
| `CallTool` | tool call ID | Execute one `read` or `idempotent_write` call. Options: retry 3 attempts, failure route `RecordToolFailure`. Before `tool_call_started`, when the turn is steered or cancelled, records the call cancelled and publishes its result without running it. Records `succeeded`, or `failed` for `ToolExecutionError`, and publishes `ToolCallResults`. A `ToolInputRequested` result records `awaiting_answer` with `input_continuation`, `input_question`, `input_choices`, and `input_allows_free_text`, and moves to `AwaitQuestionAnswer`, which raises the `question` interrupt `<tool_call_id>-input-<n>` under the `PendingInterrupts` lock. The tool Step itself never writes `PendingInterrupts`, so parallel tool Steps cannot lose an update. A `ToolTaskStarted` result records `waiting_for_task` with `task_id` and `task_poll_interval_seconds`, clears `input_continuation`, writes `tool_task_started`, and moves to `PollToolTask`. When the runner does not implement `poll_tool_task` (`pollToolTask`), or `task_id` is blank or the interval is not a number, it records `failed` for a `read` call and `outcome_unknown` otherwise, with that error, because the task may run. | Ends, `AwaitQuestionAnswer`, or `PollToolTask` |
| `CallSingleAttemptTool` | tool call ID | Execute one `non_idempotent_write` or `unclassified` call. Options: retry 1 attempt, failure route `RecordToolOutcomeUnknown`. Same check before `tool_call_started`, and the same recording. | Ends, `AwaitQuestionAnswer`, or `PollToolTask` |
| `PollToolTask` | tool call ID | WaitFor a Timer for `task_poll_interval_seconds`, or a zero Timer while `task_answer_pending` is set, so a resolved question reaches the task at once. Execute calls `poll_tool_task(ToolTaskPoll)` once, with `pending_answer` set to the last `input_answers` entry while `task_answer_pending` is set, and an `execute_tool` span that records the outcome. `ToolExecutionResult` records `succeeded` and `ToolExecutionError` records `failed`, each publishing `ToolCallResults`; `ToolTaskPending(poll_interval_seconds, continuation)` stores the interval, rounded up to whole seconds from 1 to a day, and `continuation` as `input_continuation`, where `None` clears it; `ToolInputRequested` records `awaiting_answer` as `CallTool` does. The poll's commit clears `task_answer_pending`. The poll with `pending_answer` reads the question's continuation, and each later poll reads what the last `ToolTaskPending` returned, so the runner decides how long it recognizes the request it answered. Options: retry 3 attempts; failure route `RecordToolFailure` for `read` and `idempotent_write` calls and `RecordToolOutcomeUnknown` otherwise. It takes no lock and waits on no cancellation, so steering never abandons a started task. `cancel_turn` cancels it, including a running poll, whose result is dropped. A `PollToolTask` that starts after `cancel_turn` committed waits on a zero Timer and moves to `FinishTurnCancellation` without polling. | Ends, itself, `AwaitQuestionAnswer`, or `FinishTurnCancellation` |
| `RecordToolFailure` | tool call ID | Records `failed` for a `read` call and `outcome_unknown` for an `idempotent_write` call, with the recovery detail in `error`, and publishes `ToolCallResults`. It is reached only through crashes, timeouts, or errors other than `ToolExecutionError` after the attempts of `CallTool` or `PollToolTask` ran out. | Ends |
| `RecordToolOutcomeUnknown` | tool call ID | Records `outcome_unknown` with the recovery detail in `error`, and publishes `ToolCallResults`. | Ends |
| `ResumeTurn` | expected result count | WaitFor any of `ToolCallResults.for_n(count)` and `TurnControlRequests.for_range(at_least=1, at_most=MAX)`. Execute locks `PendingInterrupts`. On a control wake in a steered turn that is not cancelled, it removes each pending interrupt of an `awaiting_approval` record or of an `awaiting_answer` record with no `task_id`, writes its `interrupt_resolved` with `cancellation_reason`, and publishes `ToolCallCancellations[id]` for those records and every `waiting_for_timer` record; then, as on a wake with nothing to apply, it waits again for the same count. With every result in, it checks that every record of the turn is final; if a consumed result belonged to an earlier turn, it waits again for the unfinished count. It never writes a record. | `RunModel`, resumed with the final records; `ApplyTurnControl` in a steered or cancelled turn; itself |
| `ApplyTurnControl` | none | WaitFor all of `SteeredUserMessages.at_most(MAX)` and `TurnControlRequests.at_most(MAX)`, which are non-blocking, so it consumes every steered message and stale wake at once; it is the only consumer of `SteeredUserMessages`. Execute, options SYNC and the recovery retry, locks `TurnCancellationRequest`, so no turn starts while `cancel_turn` decides: with an active turn, every record final, it appends the runner's unfinished-turn messages when the runner implements them and they succeed, otherwise, when the turn has records, the steered or cancelled note. It deletes the turn's records and `FrameworkRunState`, counts the turn, clears it and `last_turn_error`, and writes `turn_steered` or `turn_cancelled`. With consumed steered messages, it starts a turn whose user messages are all of them, in order. | `RunModel` with steered messages, else `AwaitUserMessage` |
| `RecordTurnFailure` | `CompactContextInput` or none, the failing Step's input, which it ignores | Records `last_turn_error`. When the runner implements `build_unfinished_turn_messages`, first appends what it returns; if it raises, or returns anything but messages with a `user`, `assistant`, or `tool` role and a string `framework_item`, it logs a warning and appends nothing for it, so a runner defect never blocks the failure route. When the turn has final `AgentToolCalls` records, then appends one turn-failure note; otherwise appends nothing more. Deletes the turn's records and `FrameworkRunState`, empties `PendingInterrupts`, and clears the turn. `cancel_turn` cancels a pending `RecordTurnFailure`, and the turn ends as cancelled. | `AwaitUserMessage` |
| `FinishTurnCancellation` | turn ID | Execute, options SYNC and the recovery retry, locks `TurnCancellationRequest` and `PendingInterrupts`, which `cancel_turn` holds, and decides from committed state. When the turn already ended or is not cancelled: ends. When the status is not `running_tools`, `cancel_turn` cancelled the model call or compaction: `ApplyTurnControl`. Otherwise `ResumeTurn` still waits, and this Step owns every record whose waiting Step `cancel_turn` cancelled: an `awaiting_approval`, `awaiting_answer`, or `waiting_for_timer` record with no `task_id` records `rejected` with the cancellation result and publishes `ToolCallResults`, removing its pending interrupt with `interrupt_resolved`; a record whose answer committed first goes back to its waiting Step, which applies the answer; a `waiting_for_task` record, or an `awaiting_answer` record with `task_id`, keeps `waiting_for_task` with `cancellation_reason` `turn_cancelled` and moves to `CancelToolTask`. `approved` records belong to their tool Steps, which run or record the cancellation themselves. It never ends the turn while `ResumeTurn` waits. | Ends, `ApplyTurnControl`, `CancelToolTask` per abandoned task, or a waiting Step whose answer committed |
| `CancelToolTask` | tool call ID | Async Execute. Calls the runner's optional `cancel_tool_task(ToolTaskCancellation)` (`cancelToolTask`), then records `outcome_unknown` with `error` `the turn was cancelled while the tool's task was running; the task may have taken effect` and publishes `ToolCallResults`. Options: method timeout 30 s, retry 3 attempts within 2 min, failure route `RecordToolOutcomeUnknown`, which keeps `cancellation_reason` and records the same `error`, so a cancellation that keeps failing still ends the turn. | Ends |

Rules:

- Every Step sets both its WaitFor and its Execute durability to SYNC
  explicitly, so a Flow started with an ASYNC Step durability default never
  replays a Step result.
- A failure-route Step receives the failing Step's input, so each tool call
  Step and its failure route know their tool call from the input. There is no
  shared current tool call, because tool call Steps run in parallel.
- Every tool call publishes exactly one `ToolCallCompleted` message, in the same
  commit that makes its record final. A tool Step whose record is already final
  ends without running or publishing.
- Before a tool Step calls `execute_deferred_tool_call`, it writes
  `tool_call_started` and awaits a heartbeat. Dex hands each attempt number to
  one Worker only, so a Worker that disconnects after it received the task
  fails the attempt instead of running it again elsewhere. The heartbeat
  narrowed the redelivery window of earlier Dex Servers.
- `ToolExecutionError` from `execute_deferred_tool_call` records `failed` with
  the error message and is not retried. Any other error fails the attempt.
- A record with `task_id` belongs to remote work that started. Nothing but the
  task's own outcome, or the call's failure route by effect, makes it final:
  it is never recorded `rejected`, which means the tool never ran. Steering and
  turn cancellation never cancel it: they skip a `waiting_for_task` record and
  an `awaiting_answer` record with `task_id`, whose question keeps waiting for
  its answer, decline, or deadline, and they wait for the task's result.
- A runner either stores no messages while tool calls run, or stores them
  incrementally. `ToolCallsRequested.new_messages` (default empty) is appended
  in the commit that records the tool calls. `TurnCompleted.new_messages`
  carries the turn's items that the session has not stored yet; for a runner
  that never sets the incremental field, that is every item the turn produced
  after its user message, across all of its model calls. Message sequences
  start at 1. `last_turn_error` is cleared when a later turn completes.
- A call to a built-in tool enabled in `AgentSessionOptions.built_in_tools`
  (`ask_user`, `durable_wait`, `write_todos`) is routed by `RunModel` to the
  built-in's Step instead of a tool Step, or, for `write_todos`, recorded in
  `RunModel`'s commit, with no approval and the `read` effect. Arguments
  are validated in `RunModel`'s commit; an invalid call records `failed` and
  publishes its result at once; `null` for an optional `ask_user` argument
  means the argument is absent. An enabled built-in name is reserved; a
  disabled one is an ordinary tool name.
- `write_todos` replaces `AgentPlan` with revision + 1: no tasks is `cleared`,
  a `planning` turn writes `draft`, every task completed is `completed`, and
  otherwise it is `active`. When a response has more than one `write_todos`
  call, each fails with `write_todos was called more than once in one
  response` and the plan is unchanged.
- A turn's mode comes from one decision that every turn start applies to its
  user messages: `planning` when plan mode is enabled and a message has
  `plan_mode` or a `draft` plan exists, else `chat`. A turn that
  `execute_plan` started is `executing`, and an approval that marks a `draft`
  `active` writes `plan_updated`.
- Plan mode needs `AgentSessionOptions.plan_mode_enabled`, which needs
  `write_todos` and a runner whose `supports_plan_mode` is true; the Flow
  constructor refuses either missing. `execute_plan` serializes on the
  `PendingPlanExecution` lock and records the accepted revision there, so a
  second approval is refused until `AwaitUserMessage` consumes the first,
  including while its Execute has not committed. `AwaitUserMessage` is the
  only other writer of `PendingPlanExecution`, and with `RunModel` the only
  writer of `AgentPlan`.
- Compaction changes the model input. A runner with the
  compaction capability receives in `AgentTurnContext.messages` only the
  messages after `ContextSummary.last_sequence`, and the summary in
  `AgentTurnContext.context_summary`. Summarized messages stay stored until
  retention deletes their chunk (see Message storage below). Every movement
  into `RunModel` and `ApplyTurnControl` lists in its load options only the
  `AgentMessageChunks` instances after the summary, and `CompactContext` lists
  the chunks it summarizes; each Step also loads the whole `AgentMessages`
  window. `RunModel` compacts only when the cutoff
  advances past the summary, and no message is appended between
  `CompactContext` and `RunModel`, so the pair cannot loop. `RunModel` runs
  only when every tool call record of the turn is final, so compaction never
  runs while an approval, a question, a durable wait, or a tool task is
  pending; a task's call and its result are stored like any other call's, and
  no cut separates them.
- Session settings change at turn boundaries only. `StartAgentSession`
  stores `AgentSessionStartInput.session_settings` in `SessionSettings`, and
  `update_session_settings` replaces it. The Step that starts a turn copies
  `SessionSettings` into `TurnSessionSettings` in the same commit, and
  `RunModel` and `CompactContext` read only `TurnSessionSettings`, so a running
  turn keeps the settings it started with, retries included. Settings hold no
  secrets, only references the Worker resolves, and their compact JSON text in
  UTF-8 is at most `MAX_SESSION_SETTINGS_BYTES` (16,384) bytes, which Clients
  check; the session's own checks accept `SESSION_SETTINGS_SIZE_TOLERANCE_BYTES`
  (1,024) more, because the cores write some floats and U+2028/U+2029
  differently. Tool Steps do not receive the settings.
- `RunModel` and `CompactContext` both route failures to `RecordTurnFailure`,
  whose input is therefore `CompactContextInput` or JSON `null`. The Python
  core declares `CompactContextInput | None` as the input of all three Steps,
  because its Registry requires a failure route to share its Step's input
  type; `RunModel` always receives `null`, as before.
- Writers in both cores emit times in UTC with milliseconds and `Z`, such as
  `2026-11-01T12:00:00.123Z`. Readers in both cores, 0.1.0 included, accept
  that form and the `+00:00` offset form that Python 0.1.0 wrote.

Steering and cancellation rules (`MAX` is 2,147,483,647):

- Two flags decide every boundary, from committed state only. A turn is
  cancelled while `TurnCancellationRequest.turn_id` equals the active turn ID;
  turn IDs never repeat. It is steered while `SteeredUserMessages` is not
  empty. A turn that is both ends as cancelled, and the steered messages start
  the next turn. The `cancellation_reason` is `turn_cancelled` or `steered`.
- A call abandoned before it starts records `rejected` with
  `cancellation_reason` and the structured `rejection_message` below. Final
  statuses do not change.
- Steering never cancels a model call or a tool call that has started.
  Execute sees the state loaded for its first attempt, and retries reuse it,
  so `RunModel` decides only before its model call, a tool Step only before
  `tool_call_started`, and a tool Step that started runs to the end. A steer
  that commits during a model call applies at the next boundary: the tool
  Steps it requested, `ResumeTurn`'s control wake, or, after a final answer,
  `AwaitUserMessage`.
- A steered turn takes its `interaction_mode` from its messages as a queued
  turn does: `planning` when plan mode is enabled and any steered message has
  `plan_mode` or a `draft` plan exists. A queued message moved by its ID keeps
  `plan_mode`. `execute_plan` returns false while a steered message is
  pending. A `write_todos` call settles in `RunModel`'s own commit, like a
  built-in call with invalid arguments, so one that a model call made while
  the user steered still replaces the plan.
- Steering abandons a waiting call only while it has taken no effect: an
  `awaiting_approval` record, an `awaiting_answer` record with no `task_id`,
  or a `waiting_for_timer` record. One
  predicate in each core decides this for `ResumeTurn`, the woken Step, and an
  unraised tool question, so a call whose remote work has started is left to
  finish.
- `ToolCallCancellations` only wakes a waiting Step; the flags decide. A woken
  Step whose turn is neither steered nor cancelled waits again, so a wake-up
  left from an earlier turn whose call reused the ID changes nothing. A woken
  approval or question whose interrupt is gone and whose answer is pending
  waits again, so an answer that committed first wins. Otherwise the Step
  removes its interrupt when it is still pending, records the call
  cancelled, and publishes its result.
- A Step's waits, locks, and loads come from the Worker that scheduled it. A
  turn that a Worker without steering started or advanced can hold such a wait,
  which a steer never wakes. `_begin_turn` sets `AgentState.accepts_turn_control`,
  every turn end clears it, and an earlier Worker drops it whenever it writes
  `AgentState`; `steer_user_message` and `cancel_turn` refuse a turn without it.
  While no turn is active, a steer is queued instead, because an idle wait that
  an earlier Worker began listens only to `QueuedUserMessages`. A turn that
  started on 0.2.0 during a mixed rollout is not refused, so applications call
  the RPCs only after every turn active during the rollout has ended.
- `ResumeTurn`, `AwaitToolApproval`, and `AwaitQuestionAnswer` serialize on
  the `PendingInterrupts` lock with `resolve_interrupt` and
  `answer_question`. Either the RPC commits first and its record moves on, or
  `ResumeTurn` removes the interrupt first and the RPC returns false. An
  approval that wins moves to its tool Step, which sees the flag and records
  the call cancelled before it starts.

### Turn-failure note and model-visible results

The turn-failure note is the user message that `build_user_message` returns
for this text, with one line per final record in `deferral_order`:

```text
[Dex] The previous turn failed after these tool calls ran:
- <tool_name> <arguments> -> <status>: <model-visible result>
Do not repeat these tool calls unless the user asks again.
```

Model-visible results for final records, used in the note and when resuming:

| Status | Text |
| --- | --- |
| `succeeded` | `output` |
| `failed` | `The tool call failed: <error>` |
| `rejected` | `rejection_message`, or `The user rejected this tool call.` |
| `outcome_unknown` | `The tool call's outcome is unknown: it stopped without returning a result, so it may or may not have taken effect. It will not be retried automatically.` |

Steering and `cancel_turn` record an abandoned call `rejected` with one of
these `rejection_message` values, compact JSON text. A started task that
`cancel_turn` abandons records `outcome_unknown` with `cancellation_reason`
`turn_cancelled` instead, because the task may have run:

```text
{"status":"cancelled","reason":"superseded_by_steered_user_message"}
{"status":"cancelled","reason":"turn_cancelled_by_user"}
```

When a steered or cancelled turn ends and the runner stores no
unfinished-turn messages, `ApplyTurnControl` stores a note in the
turn-failure note's line format and footer, with the first line
`[Dex] The user steered the conversation after these tool calls ran:` or
`[Dex] The user stopped the previous turn after these tool calls ran:`.

An expired approval deadline records `rejected` with `rejection_message`
`approval deadline passed`; an expired question deadline records
`question deadline passed`. A declined question records
`The user declined to answer.` when the user gave no message. Built-in
argument errors record `failed` with `invalid <tool> arguments: <reason>`, and
`invalid todos: <reason>` for `write_todos`. A `write_todos` call that succeeds
records `succeeded` with `{"status":"updated","revision":<n>,"task_count":<k>}`,
or `{"status":"cleared","revision":<n>}` for no tasks. Argument text that is
not strict JSON, `NaN` included, fails as `arguments must be a JSON object` in
both cores, and the first unknown name is the lowest by code point.

A runner that supports plan mode shows the model the plan with
`build_plan_note(plan, interaction_mode)`, as user text before the turn's
messages:

```text
[Dex] The current plan, revision <n>, is <status>:
- [<task status>] <content>
This turn only plans: write or revise the plan with write_todos, and do not do or claim any of its tasks. The user reviews the plan before it runs.
```

The last line appears only in a `planning` turn, where a missing or `cleared`
plan shows as `[Dex] There is no plan yet.`; outside a planning turn, such a
plan shows nothing.

### Message storage

The conversation is a Sequentially Chunked AttributeMap with retention.

- **Window.** The newest messages are `AgentMessages` instances, one per
  message, so storing a message is one blind instance write and never
  rewrites a larger value. Every Step that stores messages loads the whole
  window: `StartAgentSession`, `AwaitUserMessage`, `RunModel`,
  `ApplyTurnControl`, and `RecordTurnFailure`.
- **Rollover.** When a Step leaves more than `message_window_size` messages in
  the window, the same commit moves the oldest ones into `AgentMessageChunks`
  instances keyed by their first sequence, deletes them from the window,
  appends each chunk's range to `AgentMessageIndex.chunks`, and advances
  `window_first_sequence`. A chunk closes at `message_chunk_size` messages or
  before the next message would take its encoded size past
  `message_chunk_max_bytes`. The size is the UTF-8 length of the core's own
  JSON encoding: 15 bytes for `{"messages":[]}`, each message, and a comma
  between messages. One commit
  moves at most `MESSAGE_ROLLOVER_MAX_CHUNKS_PER_COMMIT` (4) chunks and
  `MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT` (8 MiB) unless its first chunk alone
  is larger, so the window can stay above its size until later commits drain
  it. Chunks are never rewritten.
- **Oversized messages.** A message whose single-message chunk would exceed
  `message_chunk_max_bytes` skips the window: the Step that stores it writes
  it straight into its own chunk and adds that range to
  `AgentMessageIndex.chunks`, where it sits among the window's sequences. The
  first store thus writes the largest value the message ever needs. A message
  too large for `values.maxValueBytes` fails that first commit, and the turn
  fails, as a too-large value did in 0.1.0; no later rollover can fail on it.
  Rollover steps over such chunks.
- **Retention.** With compaction and a `retained_message_limit`, the Step that
  stores messages and `CompactContext` delete the oldest chunk while its last
  sequence is at or before `ContextSummary.last_sequence` and at least
  `retained_message_limit` messages are stored after it, then advance
  `first_sequence`. The newest `retained_message_limit` messages and every
  message after the summary always stay.
- **Reads.** A sequence in a chunk range is in that chunk; any other sequence
  from `window_first_sequence` on is in `AgentMessages`. A reader checks that the chunk holds exactly its range.
  `AgentMessagesPage.deleted_before_sequence` is `first_sequence` once
  retention deleted the messages before it.
- **Index writes.** `AgentMessageIndex` is written only when a Step writes or
  deletes a chunk. A session without it stores every message in
  `AgentMessages` from sequence 1. An append with no messages that starts no
  turn writes nothing.
- **Counts.** The Step that starts a turn writes `TurnReplySequence` with the
  turn ID and the first sequence after its user messages. `turn_completed`,
  `turn_steered`, and `turn_cancelled` count the turn's messages as
  `next_message_sequence - sequence`, so no Step reads the turn's older
  messages. When the value names another turn, such as one a 0.1.0 Worker
  started, the count scans back from the newest message, and the next Step
  that stores a message in that turn records its `TurnReplySequence` before
  any rollover.
- **0.1.0 sessions.** A session that dex-agents 0.1.0 stored has every message
  in `AgentMessages` and no `AgentMessageIndex`, which readers treat as a
  large window. The rollover of each Step that stores messages moves the
  older messages into chunks, at most four chunks a commit. Readers handle
  the half-moved session through the index.
- **Upgrade rule.** Stop every 0.1.0 Worker of a Flow type before any 0.2.0
  Worker starts. A 0.1.0 Worker on an upgraded session fails its turns,
  because it cannot read the messages that moved into chunks.
- **Warning.** A runner without the compaction capability on a session whose
  `first_sequence` is past 1 gets a model input without the deleted messages
  and no summary; the core logs a warning on each such model call.

Options, named alike in every core: `message_window_size` (64),
`message_chunk_size` (32), `message_chunk_max_bytes` (1 MiB, at most 8 MiB),
and `retained_message_limit` (1,000; `None` keeps every message). The Go core
has no compaction, so it has no retention.

### Persistence schema

| Name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute | `AgentState` |
| `AgentMessages` | AttributeMap, key = sequence as 12-digit zero-padded decimal | `AgentMessage`; the window only: sequences from `AgentMessageIndex.window_first_sequence` |
| `AgentMessageChunks` | AttributeMap, key = the chunk's first sequence as 12-digit zero-padded decimal | `AgentMessageChunk`; immutable, its range listed in `AgentMessageIndex.chunks`, deleted whole by retention |
| `AgentMessageIndex` | Attribute | `AgentMessageIndex`; unset until a Step first writes a chunk |
| `TurnReplySequence` | Attribute | `TurnReplySequence`; the Step that starts a turn writes it |
| `FrameworkRunState` | Attribute | string, opaque framework JSON; present only while a turn waits for tool calls |
| `AgentToolCalls` | AttributeMap, key = tool call ID | `AgentToolCall`; deleted when the turn ends |
| `PendingInterrupts` | Attribute | list of `AgentInterrupt` |
| `AgentUsage` | Attribute | `AgentUsage` |
| `QueuedUserMessages` | Channel | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap, key = interrupt ID | `AgentInterruptResolution` |
| `ToolCallResults` | Channel, internal | `ToolCallCompleted` |
| `SteeredUserMessages` | Channel | `AgentUserMessage`; `steer_user_message` publishes, `ApplyTurnControl` consumes |
| `TurnControlRequests` | Channel, internal | `TurnControlRequest`; wakes `ResumeTurn` or `AwaitUserMessage` |
| `TurnCancellationRequest` | Attribute | `TurnCancellationRequest`; only `cancel_turn` writes it |
| `ToolCallCancellations` | ChannelMap, internal, key = tool call ID | `ToolCallCancellation`; `ResumeTurn` publishes, the call's waiting Step consumes |
| `AgentTraceContext` | Attribute | string, the W3C `traceparent` (`00-<trace id>-<span id>-<flags>`) of the current activation's root span while it has a valid context, sampled or not; an activation whose root has none deletes it |
| `AgentPlan` | Attribute | `AgentPlan`; unset until `write_todos` first succeeds |
| `PlanExecutions` | ChannelMap, key = revision as decimal | `PlanExecutionRequest` |
| `PendingPlanExecution` | Attribute | `PlanExecutionRequest`; the approval `execute_plan` accepted that `AwaitUserMessage` has not consumed |
| `ContextSummary` | Attribute | `ContextSummary`; set once the session compacts; only `CompactContext` writes it |
| `SessionSettings` | Attribute | JSON object; the settings the next turn starts with; `StartAgentSession` and `update_session_settings` write it; unset when there are none |
| `TurnSessionSettings` | Attribute | `TurnSessionSettings`; the settings a turn started with, tagged with its turn; the turn-start Steps write it; a copy whose `turn_id` is not the active turn reads as `{}` |
| `AgentEvents` | Stream, capacity 10 MiB by default, configurable | `AgentEvent` |

Value types:

```text
AgentSessionStartInput {
  initial_message: str | null
  session_settings: JSON object | null = null   # absent in 0.1.0; {} means none
}
UpdateSessionSettingsRequest { session_settings: JSON object | null = null }   # replaces; {} clears; null is refused
TurnSessionSettings { turn_id: str, session_settings: JSON object }
AgentState {
  status: "waiting_for_user_message" | "running_model" | "running_tools" | "compacting_context"
  turn_count: int            # completed or failed turns
  active_turn_id: str | null # "turn-<n>"
  next_message_sequence: int
  last_turn_error: str | null
  interaction_mode: "chat" | "planning" | "executing" = "chat"  # of the active or last turn
  accepts_turn_control: bool = false  # set when a Worker with steering starts the turn
}
AgentMessage {
  sequence: int
  turn_id: str
  role: "user" | "assistant" | "tool"
  text: str | null           # display text
  tool_call_id: str | null
  tool_name: str | null
  tool_arguments: str | null # JSON text
  tool_output: str | null
  framework_item: str        # framework-native JSON, replayed to the framework unchanged
  created_at: str            # RFC 3339
}
AgentToolCall {
  tool_call_id: str
  turn_id: str
  tool_name: str
  arguments: str             # JSON text
  effect: "read" | "idempotent_write" | "non_idempotent_write" | "unclassified"
  requires_approval: bool
  status: "awaiting_approval" | "approved" | "awaiting_answer" | "waiting_for_timer" | "waiting_for_task"  # non-final
        | "rejected" | "succeeded" | "failed" | "outcome_unknown"                                      # final
  output: str | null
  error: str | null          # ToolExecutionError message, or the recovery detail
  rejection_message: str | null
  deferral_order: int        # zero-based order of the call within its turn
  tool_source_reference: str | null  # opaque text from DeferredToolCall.tool_source_reference
  trace_context: str | null  # traceparent of the activation root its tool spans join; set by RunModel and by an approval
  wait_until: str | null = null              # RFC 3339, durable_wait only
  input_continuation: str | null = null      # from the last ToolInputRequested, or a task's last ToolTaskPending
  input_answers: list of QuestionAnswer = []
  input_question: str | null = null          # the pending ToolInputRequested question
  input_choices: list of str = []            # its choices
  input_allows_free_text: bool = true        # whether it accepts free text
  cancellation_reason: "steered" | "turn_cancelled" | null = null  # on a rejected record that steering abandoned
  task_id: str | null = null                 # from ToolTaskStarted; kept after the task ends
  task_poll_interval_seconds: int | null = null  # whole seconds, at least 1, until the next poll
  task_answer_pending: bool = false          # input_answers[-1] has not reached the task yet
}
QuestionAnswer { interrupt_id: str, question: str, answer: str,
                 resolution: "answered" | "declined" | "deadline_passed" = "answered" }
AgentInterrupt {
  interrupt_id: str          # the tool call ID for approvals and ask_user; "<id>-input-<n>" for tool questions
  kind: "tool_approval" | "question"
  tool_call_id: str
  tool_name: str
  arguments: str
  created_at: str
  deadline: str | null
  question: str | null = null
  choices: list of str = []
  allows_free_text: bool = true
}
AgentInterruptResolution { approved: bool, rejection_message: str | null, answer: str | null = null }
AnswerQuestionRequest { interrupt_id: str, answer: str }
ToolCallCompleted { tool_call_id: str }
PendingUserMessage { message_id: str, message: AgentUserMessage }  # message_id is the Channel message ID
SteerUserMessageRequest { message_id: str | null, text: str | null }  # exactly one set
DeleteQueuedMessageRequest { message_id: str }
CancelTurnRequest { turn_id: str | null }  # null for the active turn
TurnControlRequest { kind: "steer" | "cancel_turn" }  # cancel_turn no longer publishes one
TurnCancellationRequest { turn_id: str, requested_at: str }
ToolCallCancellation { reason: "steered" | "turn_cancelled" }
AgentUsage { model_calls: int, input_tokens: int, output_tokens: int, cached_input_tokens: int, reasoning_tokens: int }
AgentUserMessage { text: str, plan_mode: bool = false }
AgentPlan { revision: int, status: "draft" | "active" | "completed" | "cleared", tasks: list of AgentPlanTask, updated_at: str }
AgentPlanTask { content: str, status: "pending" | "in_progress" | "completed" }
ExecutePlanRequest { revision: int }
PlanExecutionRequest { revision: int }
AgentEvent {
  type: str                  # see Events
  turn_id: str | null
  attempt: int               # one-based Step attempt
  payload: str               # JSON object text
  step_execution_id: str     # Dex Step execution ID, shared by every attempt; "" when absent
}
ResolveInterruptRequest { interrupt_id: str, approved: bool, rejection_message: str | null }
AgentSnapshot {
  state: AgentState | null   # null until StartAgentSession commits
  messages: list of AgentMessage           # the last 200, in sequence order
  pending_interrupts: list of AgentInterrupt
  tool_calls: list of AgentToolCall        # the active turn's records, in deferral_order
  usage: AgentUsage
  queued_message_count: int
  plan: AgentPlan | null = null
  queued_messages: list of PendingUserMessage = []   # oldest first
  steered_messages: list of PendingUserMessage = []  # not applied yet, oldest first
  context_summary: ContextSummary | null = null
  session_settings: JSON object | null = null   # the next turn's settings
}
ContextSummary {
  generation: int            # summaries written, starting at 1
  first_sequence: int        # always 1: each summary folds in the previous one
  last_sequence: int         # the last summarized message
  text: str
  created_at: str            # RFC 3339
}
CompactContextInput { through_sequence: int }
AgentMessageChunk { messages: list of AgentMessage }   # consecutive messages
AgentMessageChunkRange { first_sequence: int, last_sequence: int }
AgentMessageIndex {
  chunks: list of AgentMessageChunkRange  # every kept chunk, oldest first; archived ones consecutive, then single oversized messages
  first_sequence: int                     # oldest stored message
  window_first_sequence: int              # oldest message in AgentMessages
}
TurnReplySequence { turn_id: str, sequence: int }  # first sequence after the turn's user messages
ListAgentMessagesRequest { before_sequence: int | null, limit: int }  # limit defaults to 50
AgentMessagesPage { messages: list of AgentMessage, next_before_sequence: int | null, deleted_before_sequence: int | null }
```

### RPCs

| RPC | Input | Output | Notes |
| --- | --- | --- | --- |
| `send_user_message` | `AgentUserMessage` | bool | Transactional; false for blank text, and for a `plan_mode` message when `plan_mode_enabled` is false |
| `execute_plan` | `ExecutePlanRequest` | bool | Locks `PendingPlanExecution`. False unless the session waits for a user message, `AgentPlan` has this revision and is `draft` or `active`, `PendingPlanExecution` holds no request for it, and no queued or steered message or interrupt is pending. Otherwise sets `PendingPlanExecution` and publishes `PlanExecutions[revision]` in one commit |
| `steer_user_message` | `SteerUserMessageRequest` | bool | Transactional, loads `QueuedUserMessages`, locks nothing. While no turn is active, it queues `text` like `send_user_message`, and a `message_id` still queued stays queued (true). False when the active turn's `accepts_turn_control` is false. With `message_id`, deletes that queued message and publishes it to `SteeredUserMessages`; with `text`, publishes a new steered message. Then publishes `TurnControlRequest(kind="steer")`. False when both or neither field is set, the text is blank, or the message ID is not pending. A message consumed between the read and the commit fails the RPC with CHANNEL_MESSAGE_NOT_FOUND, and nothing commits |
| `delete_queued_message` | `DeleteQueuedMessageRequest` | bool | Transactional, loads `QueuedUserMessages`; deletes the message; false when it is not pending; CHANNEL_MESSAGE_NOT_FOUND as for `steer_user_message` |
| `cancel_turn` | `CancelTurnRequest` | bool | Locks `TurnCancellationRequest` and `PendingInterrupts`; only short Steps lock them, so it never waits for a model call. False when no turn is active, `turn_id` names another turn, or the turn's `accepts_turn_control` is false. True and no change when the turn is already cancelled. Otherwise writes `TurnCancellationRequest(turn_id, requested_at)` and returns canceling Steps `RunModel`, `CompactContext`, `RecordTurnFailure`, `AwaitToolApproval`, `AwaitQuestionAnswer`, `AwaitTimer`, and `PollToolTask`, and next Step `FinishTurnCancellation(turn_id)`. Dex cancels the selected Steps at the RPC's commit, including a running attempt. True means the request is recorded; a model call that committed its final answer before the RPC completes the turn |
| `update_session_settings` | `UpdateSessionSettingsRequest` | bool | Transactional, locks nothing; the last update to commit wins. False before `StartAgentSession` committed, or when the settings are null or missing, are not a JSON object, or exceed `MAX_SESSION_SETTINGS_BYTES` plus `SESSION_SETTINGS_SIZE_TOLERANCE_BYTES` (1,024). Otherwise sets `SessionSettings`, or deletes it for `{}`. Takes effect when the next turn starts; the active turn keeps `TurnSessionSettings` |
| `resolve_interrupt` | `ResolveInterruptRequest` | bool | Locks `PendingInterrupts`; false when the interrupt is not pending or its deadline has passed; removes it and publishes the resolution in one commit, dropping `rejection_message` on approval. On a `question`, `approved=false` declines it and `approved=true` returns false |
| `answer_question` | `AnswerQuestionRequest` | bool | Locks `PendingInterrupts`; false when the interrupt is not a pending `question`, is past its deadline, or the answer is blank or not one of `choices` while free text is not allowed; removes it and publishes `AgentInterruptResolution(approved=true, answer)` in one commit |
| `get_agent_snapshot` | none | `AgentSnapshot` | Loads `AgentMessages`, `AgentMessageChunks`, and `AgentToolCalls`, and the `QueuedUserMessages` and `SteeredUserMessages` messages; returns state, the last 200 stored messages, pending interrupts, the active turn's tool calls, usage, the queued message count, the plan, the queued and steered messages with their IDs, the context summary, and `SessionSettings` |
| `list_agent_messages` | `ListAgentMessagesRequest` | `AgentMessagesPage` | Loads `AgentMessages` and `AgentMessageChunks`. Oldest first. `before_sequence` null starts after the newest message. Limit clamped to 1..200. `next_before_sequence` is the page's first sequence, or null when the page starts at the oldest stored message, `AgentMessageIndex.first_sequence` (1 without an index) |

TypeScript uses the same RPC names.

### Events

`AgentEvents` carries these types. Payloads are JSON objects with snake_case
keys:

| Type | Payload |
| --- | --- |
| `turn_started` | `{"sequence"}`: the turn's user message sequence |
| `turn_completed` | `{"message_count"}`: messages the turn appended |
| `turn_failed` | `{"error"}` |
| `turn_steered` | `{"steered_message_count", "message_count"}`: the steered messages that start the next turn, and the messages the ended turn appended |
| `turn_cancelled` | `{"message_count"}` |
| `text_delta` | `{"text"}` |
| `reasoning_delta` | `{"text"}` |
| `tool_call_started` | `{"tool_call_id", "tool_name"}` |
| `tool_call_finished` | `{"tool_call_id", "tool_name", "status"}`, once per tool call when its record becomes final, rejections included |
| `tool_call_deferred` | `{"tool_call_id", "tool_name", "effect", "requires_approval"}`, once per requested tool call |
| `interrupt_raised` | `{"interrupt_id", "tool_call_id", "tool_name", "deadline", "kind", "question", "choices", "allows_free_text"}` |
| `interrupt_resolved` | `{"interrupt_id", "approved", "rejection_message", "kind", "answered", "cancellation_reason"}`; `cancellation_reason` is `steered` or `turn_cancelled` when steering or `cancel_turn` removed the interrupt, else null |
| `timer_started` | `{"tool_call_id", "wait_until", "reason"}` |
| `plan_updated` | `{"revision", "status", "task_count"}`, when `write_todos` replaces the plan or an approval marks a `draft` plan `active` |
| `plan_execution_started` | `{"revision"}`, after the `turn_started` of an `executing` turn |
| `tool_task_started` | `{"tool_call_id", "tool_name", "task_id"}` |
| `usage_recorded` | `{"recorded", "session"}`: two `AgentUsage` objects, the model call and the session total |
| `compaction_started` | `{"through_sequence"}`, when `CompactContext` starts; a retried attempt writes it again |
| `compaction_finished` | `{"generation", "through_sequence"}`, when `CompactContext` wrote the summary |

Adapter-specific types use an adapter prefix, such as
`openai_agents.handoff`. Every event carries `turn_id`, `step_execution_id`,
and `attempt`. A UI groups live text by `(turn_id, step_execution_id,
attempt)`: within one `step_execution_id` it keeps only the highest `attempt`
seen and drops the partial text of lower attempts. Different
`step_execution_id` values of one turn are separate model call and tool call
Steps, and each keeps its text. After `turn_completed`, `turn_failed`,
`turn_steered`, or `turn_cancelled`, the UI replaces the turn's live text with
the stored messages. Streams are best
effort; clients rebuild from `get_agent_snapshot`. The event writer is safe to
call from any thread.

### Tracing

The cores emit OpenTelemetry GenAI spans through the OpenTelemetry API, an
optional dependency: the Python extra `dex-agents[otel]` (`opentelemetry-api`
only) and the optional npm peer `@opentelemetry/api`. Without it, or with
tracing disabled, a core never loads it and writes no `AgentTraceContext`.
Parent design: C9 in [#1132](https://github.com/superdurable/dex/pull/1132).

- An activation starts when a Step consumes an input from outside the
  session: `StartAgentSession`, `AwaitUserMessage`, or `ApplyTurnControl`
  with a user message, or `AwaitToolApproval` with an interrupt answer or an
  expired deadline. That
  Step's attempt emits the trace root `invoke_agent {agent name}`, linked to
  the previous root. It stores its `traceparent` in `AgentTraceContext` when
  its span context is valid, sampled or not, so the activation's spans share
  its sampling decision, and deletes a stored value when it has none.
- Each `RunModel` attempt emits a `chat` span when it ends, as a child of the
  root in `AgentTraceContext`: `CLIENT` with usage when the runner reports one
  model call, `INTERNAL` without usage otherwise. Each `CompactContext` attempt
  emits the same span for its summary call, named from
  `ContextSummaryResult.model_call_metadata`. `RunModel` copies the root
  onto each tool call record, and an approval replaces it with its own root.
  Each `CallTool`, `CallSingleAttemptTool`, and `PollToolTask` attempt emits
  `execute_tool {tool name}` under the record's root. Every span ends inside
  its Step attempt, so none stays open across a durable wait, and the
  `invoke_agent` root ends with its own Step.
- Tracing never fails or retries a Step: OpenTelemetry errors are logged and
  the span is skipped.
- Every span carries the Flow ID as `gen_ai.conversation.id`, `session.id`, and
  `dex.flow.id`, plus `dex.run.id`, `dex.step.type`, `dex.step.execution_id`,
  `dex.step.attempt`, and `dex.agent.turn.id`.
- Content is by reference by default: message sequence keys and tool call IDs.
  Message content is opt-in.
- The conventions version is pinned, with
  `open-telemetry/semantic-conventions-genai` at commit `dc3571562603552dc555c667dfa327a638d94ab2` (schema
  `1.42.0-dev`) as the default and OpenTelemetry semantic conventions `1.36.0`
  as the compatibility value.

The core READMEs list every attribute. The Dex SDKs emit no spans; SDK-level
tracing is a separate design.

### Adapter interface

```python
class AgentFrameworkRunner(Protocol[ToolOutcomeT]):  # ToolOutcomeT defaults to ToolExecutionResult
    def build_user_message(self, text: str) -> FrameworkMessage: ...
    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome: ...
    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolOutcomeT: ...
```

A bare `AgentFrameworkRunner` returns `ToolExecutionResult`, as in 0.1.0. A
runner whose tools ask the user implements
`AgentFrameworkRunner[DeferredToolOutcome]`, where `DeferredToolOutcome` is
`ToolExecutionResult | ToolInputRequested`. The TypeScript core declares
`AgentFrameworkRunner<ToolOutcome = ToolExecutionResult>` the same way.

`AgentTurnContext` gives the runner the session's stored messages, the
framework run state when continuing the turn, the turn's tool call records (all
final when `RunModel` runs), the tool policy lookup `resolve_tool_policy`, an
event writer, a heartbeat function, the Flow ID, the turn ID, and the attempt.
`AgentTurnOutcome` is either `TurnCompleted(new_messages, usage)` or
`ToolCallsRequested(framework_run_state, tool_calls, usage)`, where each tool
call is a `DeferredToolCall(tool_call_id, tool_name, arguments, policy,
tool_source_reference=None)`. Both take an optional `model_call_metadata`, a
`ModelCallMetadata(request_model, response_model, provider_name, response_id,
finish_reasons)` that only names and annotates the `chat` span; the session
never stores it.
`run_model_step` makes at most one model call and runs no tool it can defer.
`FrameworkMessage` is the `AgentMessage` projection plus `framework_item`.
`execute_deferred_tool_call` runs the real tool once, possibly concurrently
with other calls, and returns `ToolExecutionResult(output)`, or raises
`ToolExecutionError` for a failure the model should see. `DeferredToolExecution`
also carries `heartbeat_checkpoint`, the checkpoint an earlier attempt saved
with `save_heartbeat_checkpoint`. It may instead return
`ToolInputRequested(question, choices, allows_free_text, continuation)` when the
tool needs the user's answer before it takes any effect; the tool Step runs
again with `DeferredToolExecution.input_answers`. It may also return
`ToolTaskStarted(task_id, poll_interval_seconds)` when the tool started remote
work, after saving the handle with `save_heartbeat_checkpoint` so a retried
attempt returns it instead of starting the work again; `DeferredToolOutcome`
is `ToolExecutionResult | ToolInputRequested | ToolTaskStarted`.

Optional additions, all inert for a runner that ignores them:

- `ToolCallsRequested.new_messages` stores items incrementally. A runner that
  sets it implements `UnfinishedTurnMessageBuilder.build_unfinished_turn_messages(turn)`,
  which returns the unstored items, a tool result for every stored tool call
  included, when a turn ends without a final answer: in `RecordTurnFailure`,
  before the turn-failure note, and in `ApplyTurnControl`, instead of the
  steered or cancelled note. A runner that keeps its items in run state may
  implement it too, so a steered turn keeps its exact transcript.
- `AgentTurnContext.enabled_built_in_tools` names the enabled built-ins. A
  runner offers their `BUILT_IN_TOOL_DEFINITIONS` to its model like any tool.
- `AgentTurnContext.session_settings` (TypeScript `sessionSettings`, Go
  `SessionSettings`) holds `TurnSessionSettings`, an empty object when there
  are none, and `ContextSummaryRequest.session_settings` the same. A runner
  chooses the model or instructions from it. Stepwise's
  `AgentDefinition.resolve_model` (`resolveModel`, Go `WithModelResolver`)
  maps it to the `ModelClient` of each model call, and
  `InstructionContext.session_settings` passes it to an instructions function.
- `AgentTurnContext.interaction_mode` and `AgentTurnContext.plan` carry the
  turn's mode and the committed plan. A runner that sets `supports_plan_mode`
  (`PlanModeRunner`; TypeScript `supportsPlanMode`) offers only `write_todos`
  in a `planning` turn and shows `build_plan_note` before the turn's messages.
  Only such a runner can enable plan mode. Stepwise does; the adapters do not
  yet. Stepwise's `AgentDefinition.planning_tool_choice` (`planningToolChoice`)
  is `auto` by default: a planning turn may answer in text, such as with a
  clarifying question, and then ends without a draft. `required` forces
  `write_todos` on the first model call of a planning turn while no draft
  exists, which some models and Anthropic extended thinking refuse; the Python
  core refuses `required` when the model client's
  `supports_forced_tool_choice(settings)` returns false.
- A runner whose tools return `ToolTaskStarted` implements
  `ToolTaskPoller.poll_tool_task(poll: ToolTaskPoll) -> ToolExecutionResult |
  ToolInputRequested | ToolTaskPending` (`ToolTaskPollOutcome`), where
  `ToolTaskPoll` is `{flow_id, tool_call, idempotency_key, attempt,
  input_answers, events, heartbeat, pending_answer}` and `ToolTaskPending` is
  `{poll_interval_seconds, continuation = null}`. A poll only reads the task,
  and delivers `pending_answer`, the resolution of the task's last question,
  when it is set: the answer, a decline, or a cancelled request, by its
  `resolution`. It delivers only to the request that
  `tool_call.input_continuation` names, and idempotently, because a crashed
  poll runs again; when a retried attempt cannot tell whether an earlier one
  delivered, it returns the question again instead. A pending poll returns
  the continuation the next poll needs, and drops an answered question's
  continuation once the task took the answer, so an identical later question
  reads as new. A runner that cannot
  deliver a decline cancels the task best-effort and raises an error other
  than `ToolExecutionError`, so the call is recorded by effect.
  `DeferredToolExecution.supports_tool_tasks` tells a tool whether the runner
  polls tasks; without it, the tool runs its work to the end inside the call.
- A runner that polls tasks may implement
  `ToolTaskCanceller.cancel_tool_task(cancellation: ToolTaskCancellation)`
  (`cancelToolTask`), where `ToolTaskCancellation` is `{flow_id, tool_call,
  idempotency_key, attempt, heartbeat}` (TypeScript adds
  `cancellationSignal`). `CancelToolTask` calls it best-effort for a task
  that `cancel_turn` abandoned, and it must be idempotent by `task_id`. A
  Stepwise tool source may define the same optional method; `McpToolSource`
  sends `tasks/cancel` and treats any answer from the server as done.
- Model calls honor cancellation: `cancel_turn` cancels a running `RunModel`
  or `CompactContext`. In Python the coroutine is cancelled, which stops
  the LiteLLM call and closes its response stream; in TypeScript
  `AgentTurnContext.cancellationSignal` aborts, and the AI SDK client passes
  it as `abortSignal` through `ModelRequest.cancellationSignal`. The
  framework adapters pass it on: TypeScript adapters give the signal to
  their framework, and Python adapters run on asyncio, so cancellation
  reaches the framework's awaits.
- `ContextCompactionRunner`, required by `AgentSessionOptions.compaction`
  (`CompactionPolicy(context_window_tokens, trigger_ratio=0.85,
  retained_ratio=0.3)`); the Flow constructor raises for a runner without it:
  `estimate_input_tokens(turn) -> int`, the next model input's size;
  `select_compaction_cutoff(turn, retained_token_budget) -> int | None`, the
  last sequence to summarize, which never separates a tool call from its
  result; and `summarize_context(ContextSummaryRequest(flow_id, turn_id,
  attempt, previous_summary, messages, through_sequence, events, heartbeat,
  input_token_budget)) -> ContextSummaryResult(text, usage,
  model_call_metadata=None)`, one model call. `input_token_budget` is the
  policy's `context_window_tokens`. TypeScript makes them
  optional `AgentFrameworkRunner` methods, `estimateInputTokens`,
  `selectCompactionCutoff`, and `summarizeContext`; the first two may return a
  Promise, and the request also carries `stepExecutionId`,
  `cancellationSignal`, and `deadline`. Stepwise implements all three: its
  model input starts with `[Dex] Summary of the earlier conversation:` and the
  summary text, and it cuts only between model responses whose tool calls all
  have stored results. When the summary request plus the configured
  `max_output_tokens` would exceed `input_token_budget`, it leaves out the
  oldest messages up to such a cut and
  says so with `[Dex] <n> earlier messages did not fit in this request and are
  left out.` (`[Dex] 1 earlier message did not fit in this request and is left
  out.` for one).

The TypeScript core mirrors these names in camelCase for code and keeps the
snake_case wire format. Its runner-facing `AgentToolCallStatus` keeps the six
statuses a runner sees; stored records use `AgentToolCallRecord`, whose status
also allows the three waiting statuses, and which `ToolTaskPoll.toolCall`
carries. `AgentTurnContext.toolPolicies` and
`defaultToolEffect` mirror `tool_policies` and `default_tool_effect`. Options
that only one core has, because of each language's conventions: TypeScript
takes `interruptDeadlineMs` and `maxDurableWaitMs` in milliseconds where
Python takes `interrupt_deadline` and `max_durable_wait` as `timedelta`, and
TypeScript exposes `cancellationSignal` and `deadline` on its contexts,
`AgentToolCallRecord`, `ModelToolChoice`, and `aiSdkModelClientApi`.

## Tests

Each package runs integration tests against a real Dex Server, using `dexcli
dev --engine zd` from the released dexcli bundle, with a scripted model so no
model key is needed. Every adapter covers this matrix:

1. A turn with two model calls and a READ tool produces
   `RunModel -> CallTool -> ResumeTurn -> RunModel`, and the history replays
   on the next turn.
2. Parallel tool calls of one model response run as parallel Steps.
3. A write tool that needs approval waits in parallel with an unapproved READ
   tool, survives a Worker restart while waiting, runs once after approval, and
   its result reaches the model.
4. A rejected write never runs, and the model sees the rejection message.
5. A Worker killed with SIGKILL during `CallSingleAttemptTool` records
   `outcome_unknown`, the tool is not run again, and the model sees the unknown
   outcome.
6. A Worker killed during `RunModel` reruns only that one model call.
7. `resolve_interrupt` called twice returns false the second time, and an
   approval deadline rejects the call.
8. A turn that fails after a write ran leaves the turn-failure note, and the
   write is not repeated.
9. A session started with an ASYNC Step durability default never reruns a
   single-attempt write.
10. A message sent during a turn is queued and handled after the turn.
11. `get_agent_snapshot` returns the conversation after the session completes a
    turn.

Kill tests run the Worker as a child process in its own process group, as
`examples/python/tests/integ/ai_agent_processes.py` does. Tests assert the Step
shape of a turn from the Flow's history (`dexcli flow history`).

## CI

`.github/workflows/agents-ci.yml` (workflow name `Agents CI`) discovers every
package that has an executable `agents/<language>/<package>/ci.sh` and runs
one matrix job per package, so adding a package never edits the workflow.
Discovery fails when a `ci.sh` is not executable or when it finds no package.
Each job downloads the pinned dexcli release bundle and verifies its checksum,
then runs `ci.sh` with `DEXCLI` set to the `dexcli` binary; `dex-server-zd` sits
in the same directory. `ci.sh` installs the package's locked dependencies, runs
its type checks and lint, and runs its integration tests. Tests start private
`dexcli dev --engine zd` instances on free ports. The Check Gate maps
`agents/**` and the workflow file to `Agents CI`.

## Documentation

- Each package has a README with installation, a complete example, the effect
  table, its integration mode, and its limits.
- Product docs in English and zh-Hans follow in a separate change, with
  snippets copied from runnable examples.
- Skills follow the first release that contains the packages.

## UI/UX

N/A: the packages add no Dex Web UI. The Dex Web agent view in the parent
design reads the contract above, and Step mode gives every framework the same
Step shapes.

### UI stream adapters

The cores also carry the protocol adapters of parent design C6, for UIs that
applications build. They read `AgentEvents` through the Client with a resume
token and need no Server change and no other store.

- `@superdurable/dex-agents/ui-stream` translates one turn into the AI SDK UI
  message stream (`x-vercel-ai-ui-message-stream: v1`). A `RunModel` Step
  execution is one AI SDK step, recognized by its `StepType-Number` Step
  execution ID; a new attempt of it emits `reset-step`. Tool calls are dynamic
  tool parts whose arguments and results come from `AgentToolCalls`, or from
  the stored messages once the turn ended. An interrupt is a
  `tool-approval-request` whose `approvalId` is the `interrupt_id`, and a
  `tool-approval-response` calls `resolve_interrupt` for an interrupt pending
  in the active turn. The stream finishes with reason `tool-calls` when the
  turn waits only for approvals whose interrupts are still in
  `PendingInterrupts`, so `useChat` can send the answers.
- The UI stream resume token encodes the Dex resume token plus the parts the
  browser holds open, with one token after every part. A reconnect replays the
  token's turn by default, so any browser state rebuilds; an opt-in exact
  continuation serves a browser that kept its message. A Dex position the
  Server rejects restarts the turn from its listed start.
- Turn ends wait for the end's commit, and committed state ends a stream whose
  events the Stream lost, including with the Stream Store disabled.
- Both cores serve plain Server-Sent Events whose `id` is the Dex resume token,
  so an `EventSource` resumes through `Last-Event-ID`, and a snapshot with the
  token to open the events from. The snapshot JSON leaves out the fields only
  the session's Steps use: `framework_item`, `tool_source_reference`,
  `trace_context`, `input_continuation`, `input_answers`, and
  `task_answer_pending`. A rejected token
  gets a 400 before the stream starts.
- Stop is pending: a durable turn cancel is not in the contract yet, so
  closing the browser response leaves the turn running.
