# Agent Framework Adapters

Status: in progress. Parent design: [long-running-agents.md](../long-running-agents.md).

This plan builds the framework adapters before the Step Journal. Every guarantee
here rests on Dex Step boundaries that exist today. The Step Journal, if it is
adopted later, only removes repeated model calls after a crash and lets writes
run inside the framework loop. No adapter API in this plan depends on it.

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
`sdk-python/dex`. `agents` is added to `included_prefixes` and
`forced_new_prefixes` in `script/licenseheaders/policy.json`.

## Model

One Flow execution is one agent session. The Flow ID is the session ID.
Each adapter registers the shared session Flow with its own framework runner.
An application can register several agents; each gets its own Flow type.

```python
registry.add_flow(openai_agents_session_flow(triage_agent, flow_type="TriageAgent"))
```

### Turn segments and deferred writes

A turn starts when the session consumes a user message and ends when the
framework produces a final answer. `RunAgentTurn` runs the framework's own loop.
Model calls and READ tools run inside it. Writes never do.

When the model asks for a tool whose effect is not READ, or a tool whose policy
requires approval, the adapter makes the framework defer the call with the
framework's own mechanism. The Step commits the framework's serialized run
state and the deferred calls, then moves to `AwaitInterruptResolution` or an
execute Step. The tool runs in its own Dex Step. `RunAgentTurn` then restores
the framework and resumes it with the recorded result. The framework's tool
wrapper returns the recorded result instead of calling the tool again.

A crash in `RunAgentTurn` reruns the turn segment from the last committed state.
Model calls and READ tools in that segment run again. A crash in a single-attempt
tool Step records an unknown outcome and reports it to the model. No write runs
twice silently.

### Effects and policies

| Effect | Where it runs | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | Inside `RunAgentTurn` | With the turn | Runs again | No |
| `idempotent_write` | `ExecuteToolCall` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>` | Runs again with the same key | No |
| `non_idempotent_write` | `ExecuteSingleAttemptToolCall` | One attempt | Outcome unknown | Yes |
| `unclassified` | `ExecuteSingleAttemptToolCall` | One attempt | Outcome unknown | Yes |

`ToolPolicy(effect, requires_approval)` overrides the defaults per tool name.
A session-level `default_tool_effect` (default `unclassified`) applies to
tools without a policy. Adapters read framework metadata where it exists, such
as MCP `readOnlyHint` and `idempotentHint` annotations. A `read` tool whose
policy requires approval is deferred; after approval it runs in
`ExecuteToolCall`, so a resumed turn sees only final records.

The idempotency key passed to `execute_deferred_tool_call` is
`<flowId>/<turnId>/<toolCallId>`, the same on every attempt. Tool call IDs must
be non-empty, contain no `/`, and be unique within one turn; they may repeat
across turns.

## Session Flow contract

Names here are wire contracts shared by Python and TypeScript, so a UI or Dex
Web view can read any agent session. JSON field names are snake_case in both
languages.

### Steps

| Step type | Kind | Next |
| --- | --- | --- |
| `StartAgentSession` | Start Step. Input `AgentSessionStartInput`. Initializes `AgentState` (`next_message_sequence` 1), `AgentUsage`, and an empty `PendingInterrupts`. | `RunAgentTurn` when `initial_message` is set and not blank, else `AwaitUserMessage` |
| `AwaitUserMessage` | WaitFor `QueuedUserMessages.for_one()`. Execute appends the user message, starts a turn. | `RunAgentTurn` |
| `RunAgentTurn` | Async Execute. Runs or resumes the framework. Options: method timeout 15 min, heartbeat timeout 2 min, retry 3 attempts within 30 min, failure route `RecordTurnFailure`. | `AwaitUserMessage`, `AwaitInterruptResolution`, `ExecuteToolCall`, or `ExecuteSingleAttemptToolCall` |
| `AwaitInterruptResolution` | WaitFor any of `InterruptResolutions.for_one(id)` for every `AgentToolCalls` record in `awaiting_approval`, plus a Timer at the earliest deadline among interrupts still in `PendingInterrupts`. Execute locks `PendingInterrupts`. It applies the one consumed resolution, or, when the Timer fired, removes every pending interrupt whose deadline passed and rejects its call with `rejection_message` "approval deadline passed". | Itself while any record awaits approval, then the next approved tool Step, then `RunAgentTurn` |
| `ExecuteToolCall` | Execute one `idempotent_write` or approved `read` call. Options: retry 3 attempts, failure route `RecordToolFailure`. | Next approved tool Step or `RunAgentTurn` |
| `ExecuteSingleAttemptToolCall` | Execute one `non_idempotent_write` or `unclassified` call. Options: retry 1 attempt, failure route `RecordToolOutcomeUnknown`. | Next approved tool Step or `RunAgentTurn` |
| `RecordToolFailure` | Records `outcome_unknown` with the recovery detail in `error`. It is reached only through crashes, timeouts, or errors other than `ToolExecutionError` after the attempts ran out, so the write may have taken effect. | Same routing |
| `RecordToolOutcomeUnknown` | Records `outcome_unknown` with the recovery detail in `error`. | Same routing |
| `RecordTurnFailure` | Records `last_turn_error`. When the turn has final `AgentToolCalls` records, first appends one turn-failure note (below); otherwise appends nothing. Deletes the turn's records and `FrameworkRunState`, empties `PendingInterrupts`, and clears the turn. | `AwaitUserMessage` |

Every Step sets both its WaitFor and its Execute durability to SYNC
explicitly, so a Flow started with an ASYNC Step durability default never
replays a Step result. `RunAgentTurn` and the tool Steps load `AgentMessages`
or `AgentToolCalls` as they need them.

The tool Steps read the call to run from `AgentState.executing_tool_call_id`,
so failure-route Steps know which call failed. Steps are idempotent with
respect to their own retries: a tool Step whose record is already final routes
on without running. Approved calls run one at a time in `deferral_order`.

Before a tool Step calls `execute_deferred_tool_call`, it writes
`tool_call_started` and awaits a heartbeat. Dex can redeliver a task to another
Worker at the same attempt when the first Worker disconnects before sending any
output; that output closes the window before the tool runs. A frame that left
the Worker but never reached Dex remains a small residual window.

`ToolExecutionError` from `execute_deferred_tool_call` records `failed` with
the error message and is not retried. Any other error fails the attempt.

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

A turn stores nothing while it is suspended. `TurnCompleted.new_messages`
carries every item the turn produced after its user message, across all
segments, and the session appends them in one commit. Message sequences start
at 1. `last_turn_error` is cleared when a later turn completes.

### Persistence schema

| Name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute | `AgentState` |
| `AgentMessages` | AttributeMap, key = sequence as 12-digit zero-padded decimal | `AgentMessage` |
| `FrameworkRunState` | Attribute | string, opaque framework JSON; present only while a turn is suspended |
| `AgentToolCalls` | AttributeMap, key = tool call ID | `AgentToolCall` |
| `PendingInterrupts` | Attribute | list of `AgentInterrupt` |
| `AgentUsage` | Attribute | `AgentUsage` |
| `QueuedUserMessages` | Channel | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap, key = interrupt ID | `AgentInterruptResolution` |
| `AgentEvents` | Stream, capacity 10 MiB by default, configurable | `AgentEvent` |

Value types:

```text
AgentSessionStartInput { initial_message: str | null }
AgentState {
  status: "waiting_for_user_message" | "running_turn" | "waiting_for_interrupt" | "executing_tool"
  turn_count: int            # completed or failed turns
  active_turn_id: str | null # "turn-<n>"
  next_message_sequence: int
  executing_tool_call_id: str | null
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
  error: str | null          # ToolExecutionError message, or the recovery detail for outcome_unknown
  rejection_message: str | null
  deferral_order: int        # zero-based order in which the turn deferred the call
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

`AgentToolCalls` instances for a turn are deleted when the turn completes;
their results are already messages.

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
| `tool_call_finished` | `{"tool_call_id", "tool_name", "status"}` |
| `tool_call_deferred` | `{"tool_call_id", "tool_name", "effect", "requires_approval"}` |
| `interrupt_raised` | `{"interrupt_id", "tool_call_id", "tool_name", "deadline"}` |
| `interrupt_resolved` | `{"interrupt_id", "approved", "rejection_message"}` |
| `usage_recorded` | `{"recorded", "session"}`: two `AgentUsage` objects, the segment and the session total |

Adapter-specific types use an adapter prefix, such as
`openai_agents.handoff`. Every event carries `turn_id`, `step_execution_id`,
and `attempt`. A UI groups live text by `(turn_id, step_execution_id,
attempt)`: within one `step_execution_id` it keeps only the highest `attempt`
seen and drops the partial text of lower attempts. Different
`step_execution_id` values of one turn are consecutive segments and Steps, and
each keeps its text. After `turn_completed` or `turn_failed`, the UI replaces
the turn's live text with the stored messages. Streams are best effort;
clients rebuild from `get_agent_snapshot`. The event writer is safe to call
from any thread.

### Adapter interface

```python
class AgentFrameworkRunner(Protocol):
    def build_user_message(self, text: str) -> FrameworkMessage: ...
    async def run_turn(self, turn: AgentTurnContext) -> AgentTurnOutcome: ...
    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult: ...
```

`AgentTurnContext` gives the runner the session's stored messages, the
framework run state when resuming, the current turn's tool call records, the
tool policy lookup, an event writer, a heartbeat function, the Flow ID, the turn
ID, and the attempt. `AgentTurnOutcome` is either `TurnCompleted(new_messages,
usage)` or `TurnSuspended(framework_run_state, deferred_tool_calls, usage)`.
`FrameworkMessage` is the `AgentMessage` projection plus `framework_item`.
`execute_deferred_tool_call` runs the real tool once and returns its output
text, or raises `ToolExecutionError` for a failure the model should see.

The TypeScript core mirrors these names in camelCase for code and keeps the
snake_case wire format.

## Per-adapter deferral

| Framework | History | Defer | Resume |
| --- | --- | --- | --- |
| OpenAI Agents SDK | `result.to_input_list()` items | Wrap non-READ tools with `needs_approval=True`; `result.interruptions` | `RunState.from_json`; `approve` when a record is final, the wrapper returns the recorded output; `reject` with the rejection message |
| Pydantic AI | `ModelMessagesTypeAdapter` JSON | Deferred tools (`requires_approval` or `CallDeferred`) produce `DeferredToolRequests` | `DeferredToolResults` with recorded outputs or denials |
| LangChain `create_agent` / LangGraph | Message dicts from graph state | `wrap_tool_call` middleware calls `interrupt()` for non-READ tools | `Command(resume=...)` with the recorded result; checkpoint in `FrameworkRunState` |
| Google ADK | Session events | `FunctionTool(require_confirmation=...)` or `request_confirmation` | Confirmation response with recorded output |
| Claude Agent SDK | Session transcript entries | `PreToolUse` hook returns `defer` | Resume the session; the hook allows the call and the tool returns the recorded output |
| Vercel AI SDK | `ModelMessage` JSON | Non-READ tools have no `execute`, so the loop stops with tool calls | Append tool result messages and call again |
| Mastra | Agent messages | `requireToolApproval` and suspend | `resume` with the recorded output |

Each adapter documents which tool kinds it cannot defer, for example tools that
run inside a harness subprocess, and treats them as at-least-once.

## Tests

Each package runs integration tests against a real Dex Server, using `dexcli
dev --engine zd` from the released dexcli bundle, with a scripted model so no
model key is needed. Every adapter covers this matrix:

1. A turn with a READ tool completes, and the history replays on the next turn.
2. A write tool that needs approval waits, survives a Worker restart while
   waiting, runs once after approval, and its result reaches the model.
3. A rejected write never runs, and the model sees the rejection message.
4. A Worker killed with SIGKILL during a single-attempt write records
   `outcome_unknown`, the tool is not run again, and the model sees the unknown
   outcome.
5. A Worker killed during `RunAgentTurn` reruns the segment and completes.
6. `resolve_interrupt` called twice returns false the second time.
7. `get_agent_snapshot` returns the conversation after the session completes a
   turn.

Kill tests run the Worker as a child process in its own process group, as
`examples/python/tests/integ/ai_agent_processes.py` does.

## CI

`.github/workflows/agents-ci.yml` (workflow name `Agents CI`) discovers every
package that has an executable `agents/<language>/<package>/ci.sh` and runs
one matrix job per package, so adding a package never edits the workflow. Each
job downloads the pinned dexcli release bundle and verifies its checksum, then
runs `ci.sh` with `DEXCLI` set to the `dexcli` binary; `dex-server-zd` sits in
the same directory. `ci.sh` installs the package's locked dependencies, runs
its type checks and lint, and runs its integration tests. Tests start private
`dexcli dev --engine zd` instances on free ports. The Check Gate maps
`agents/**` and the workflow file to `Agents CI`.

## Documentation

- Each package has a README with installation, a complete example, the effect
  table, and its limits.
- Product docs in English and zh-Hans follow in a separate change, with
  snippets copied from runnable examples.
- Skills follow the first release that contains the packages.

## UI/UX

N/A: the packages add no Dex Web UI. The Dex Web agent view in the parent
design reads the contract above.
