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

## Session Flow contract

Names here are wire contracts shared by Python and TypeScript, so a UI or Dex
Web view can read any agent session. JSON field names are snake_case in both
languages.

### Steps

| Step type | Input | Kind | Next |
| --- | --- | --- | --- |
| `StartAgentSession` | `AgentSessionStartInput` | Start Step. Initializes `AgentState` (`next_message_sequence` 1), `AgentUsage`, and an empty `PendingInterrupts`. With a non-blank `initial_message`, appends it as the turn's user message. | `RunModel` when `initial_message` is set and not blank, else `AwaitUserMessage` |
| `AwaitUserMessage` | none | WaitFor `QueuedUserMessages.for_one()`. Execute appends the user message and starts a turn. | `RunModel` |
| `RunModel` | none | Async Execute. Calls `run_model_step` for one model call. Options: method timeout 15 min, heartbeat timeout 2 min, retry 3 attempts within 30 min, failure route `RecordTurnFailure`. | `TurnCompleted`: appends `new_messages`, clears the turn, `AwaitUserMessage`. `ToolCallsRequested`: stores `FrameworkRunState`, one `AgentToolCalls` record per call, and the interrupts, then `go_to_many` with one Step per call plus `ResumeTurn(<call count>)` |
| `AwaitToolApproval` | tool call ID | WaitFor any of `InterruptResolutions.for_one(id)` and, while the interrupt is in `PendingInterrupts` with a deadline, a Timer at that deadline. Execute locks `PendingInterrupts`. | Approved: records `approved`, then `CallTool` or `CallSingleAttemptTool` (id). Rejected or deadline passed: records `rejected`, publishes `ToolCallResults`, ends (dead end). Timer fired after `resolve_interrupt` already removed the interrupt: itself (id), which consumes that answer |
| `CallTool` | tool call ID | Execute one `read` or `idempotent_write` call. Options: retry 3 attempts, failure route `RecordToolFailure`. Records `succeeded`, or `failed` for `ToolExecutionError`, and publishes `ToolCallResults`. | Ends |
| `CallSingleAttemptTool` | tool call ID | Execute one `non_idempotent_write` or `unclassified` call. Options: retry 1 attempt, failure route `RecordToolOutcomeUnknown`. Same recording. | Ends |
| `RecordToolFailure` | tool call ID | Records `failed` for a `read` call and `outcome_unknown` for an `idempotent_write` call, with the recovery detail in `error`, and publishes `ToolCallResults`. It is reached only through crashes, timeouts, or errors other than `ToolExecutionError` after the attempts ran out. | Ends |
| `RecordToolOutcomeUnknown` | tool call ID | Records `outcome_unknown` with the recovery detail in `error`, and publishes `ToolCallResults`. | Ends |
| `ResumeTurn` | expected result count | WaitFor `ToolCallResults.for_n(count)`. Execute checks that every record of the turn is final; if a consumed result belonged to an earlier turn, it waits again for the unfinished count. | `RunModel`, resumed with the final records |
| `RecordTurnFailure` | none | Records `last_turn_error`. When the turn has final `AgentToolCalls` records, first appends one turn-failure note; otherwise appends nothing. Deletes the turn's records and `FrameworkRunState`, empties `PendingInterrupts`, and clears the turn. | `AwaitUserMessage` |

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
  `tool_call_started` and awaits a heartbeat. Dex can redeliver a task to
  another Worker at the same attempt when the first Worker disconnects before
  sending any output; that output closes the window before the tool runs. A
  frame that left the Worker but never reached Dex remains a small residual
  window.
- `ToolExecutionError` from `execute_deferred_tool_call` records `failed` with
  the error message and is not retried. Any other error fails the attempt.
- A turn stores no messages while tool calls run. `TurnCompleted.new_messages`
  carries every item the turn produced after its user message, across all of
  its model calls, and the session appends them in one commit. Message
  sequences start at 1. `last_turn_error` is cleared when a later turn
  completes.

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

An expired approval deadline records `rejected` with `rejection_message`
`approval deadline passed`.

### Persistence schema

| Name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute | `AgentState` |
| `AgentMessages` | AttributeMap, key = sequence as 12-digit zero-padded decimal | `AgentMessage` |
| `FrameworkRunState` | Attribute | string, opaque framework JSON; present only while a turn waits for tool calls |
| `AgentToolCalls` | AttributeMap, key = tool call ID | `AgentToolCall`; deleted when the turn ends |
| `PendingInterrupts` | Attribute | list of `AgentInterrupt` |
| `AgentUsage` | Attribute | `AgentUsage` |
| `QueuedUserMessages` | Channel | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap, key = interrupt ID | `AgentInterruptResolution` |
| `ToolCallResults` | Channel, internal | `ToolCallCompleted` |
| `AgentEvents` | Stream, capacity 10 MiB by default, configurable | `AgentEvent` |

Value types:

```text
AgentSessionStartInput { initial_message: str | null }
AgentState {
  status: "waiting_for_user_message" | "running_model" | "running_tools"
  turn_count: int            # completed or failed turns
  active_turn_id: str | null # "turn-<n>"
  next_message_sequence: int
  last_turn_error: str | null
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
  status: "awaiting_approval" | "approved" | "rejected" | "succeeded" | "failed" | "outcome_unknown"
  output: str | null
  error: str | null          # ToolExecutionError message, or the recovery detail
  rejection_message: str | null
  deferral_order: int        # zero-based order of the call within its turn
}
AgentInterrupt {
  interrupt_id: str          # equals the tool call ID for tool approvals
  kind: "tool_approval"
  tool_call_id: str
  tool_name: str
  arguments: str
  created_at: str
  deadline: str | null
}
AgentInterruptResolution { approved: bool, rejection_message: str | null }  # null when approved
ToolCallCompleted { tool_call_id: str }
AgentUsage { model_calls: int, input_tokens: int, output_tokens: int, cached_input_tokens: int, reasoning_tokens: int }
AgentUserMessage { text: str }
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
}
ListAgentMessagesRequest { before_sequence: int | null, limit: int }  # limit defaults to 50
AgentMessagesPage { messages: list of AgentMessage, next_before_sequence: int | null }
```

### RPCs

| RPC | Input | Output | Notes |
| --- | --- | --- | --- |
| `send_user_message` | `AgentUserMessage` | bool | Transactional; false for blank text |
| `resolve_interrupt` | `ResolveInterruptRequest` | bool | Locks `PendingInterrupts`; false when the interrupt is not pending; removes it and publishes the resolution in one commit, dropping `rejection_message` on approval |
| `get_agent_snapshot` | none | `AgentSnapshot` | Loads `AgentMessages` and `AgentToolCalls`; returns state, the last 200 messages, pending interrupts, the active turn's tool calls, usage, and the queued message count |
| `list_agent_messages` | `ListAgentMessagesRequest` | `AgentMessagesPage` | Oldest first. `before_sequence` null starts after the newest message. Limit clamped to 1..200. `next_before_sequence` is the page's first sequence, or null when the page starts at 1 |

TypeScript uses the same RPC names.

### Events

`AgentEvents` carries these types. Payloads are JSON objects with snake_case
keys:

| Type | Payload |
| --- | --- |
| `turn_started` | `{"sequence"}`: the turn's user message sequence |
| `turn_completed` | `{"message_count"}`: messages the turn appended |
| `turn_failed` | `{"error"}` |
| `text_delta` | `{"text"}` |
| `reasoning_delta` | `{"text"}` |
| `tool_call_started` | `{"tool_call_id", "tool_name"}` |
| `tool_call_finished` | `{"tool_call_id", "tool_name", "status"}`, once per tool call when its record becomes final, rejections included |
| `tool_call_deferred` | `{"tool_call_id", "tool_name", "effect", "requires_approval"}`, once per requested tool call |
| `interrupt_raised` | `{"interrupt_id", "tool_call_id", "tool_name", "deadline"}` |
| `interrupt_resolved` | `{"interrupt_id", "approved", "rejection_message"}` |
| `usage_recorded` | `{"recorded", "session"}`: two `AgentUsage` objects, the model call and the session total |

Adapter-specific types use an adapter prefix, such as
`openai_agents.handoff`. Every event carries `turn_id`, `step_execution_id`,
and `attempt`. A UI groups live text by `(turn_id, step_execution_id,
attempt)`: within one `step_execution_id` it keeps only the highest `attempt`
seen and drops the partial text of lower attempts. Different
`step_execution_id` values of one turn are separate model call and tool call
Steps, and each keeps its text. After `turn_completed` or `turn_failed`, the UI
replaces the turn's live text with the stored messages. Streams are best
effort; clients rebuild from `get_agent_snapshot`. The event writer is safe to
call from any thread.

### Adapter interface

```python
class AgentFrameworkRunner(Protocol):
    def build_user_message(self, text: str) -> FrameworkMessage: ...
    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome: ...
    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult: ...
```

`AgentTurnContext` gives the runner the session's stored messages, the
framework run state when continuing the turn, the turn's tool call records (all
final when `RunModel` runs), the tool policy lookup `resolve_tool_policy`, an
event writer, a heartbeat function, the Flow ID, the turn ID, and the attempt.
`AgentTurnOutcome` is either `TurnCompleted(new_messages, usage)` or
`ToolCallsRequested(framework_run_state, tool_calls, usage)`, where each tool
call is a `DeferredToolCall(tool_call_id, tool_name, arguments, policy)`.
`run_model_step` makes at most one model call and runs no tool it can defer.
`FrameworkMessage` is the `AgentMessage` projection plus `framework_item`.
`execute_deferred_tool_call` runs the real tool once, possibly concurrently
with other calls, and returns `ToolExecutionResult(output)`, or raises
`ToolExecutionError` for a failure the model should see.

The TypeScript core mirrors these names in camelCase for code and keeps the
snake_case wire format.

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
