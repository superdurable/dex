# dex-agents

`dex-agents` (import `dex_agents`) is the Python core of the Dex agent
framework adapters. It provides one durable agent session Flow,
`AgentSessionFlow`, and the `AgentFrameworkRunner` contract that each
framework adapter implements. The plan is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

One Flow execution is one agent session; the Flow ID is the session ID. The
session runs in **Step mode**: every model call is one `RunModel` Step, and
every tool call, READ included, is its own Step. The tool call Steps of one
model response run in parallel, and `ResumeTurn` waits for their results
before the next model call. Each call therefore has its own retry policy,
timeout, failure route, history entry, and Dex Web node, and a crash repeats
at most the one call that was running.

## Installation

Applications usually install an adapter, which brings in this package. To
build a runner of your own, install the core directly. It needs Python 3.11 or
later and requires the released `dex-python-sdk==1.30.0`:

```bash
pip install dex-agents
```

`dex_agents.testing` ships in the same wheel, so an adapter's integration
tests can start a private engine and Worker processes. So does
`dex_agents.ui_stream`, the [browser streams](#browser-streams).
`dex_agents.mcp` offers the tools of MCP servers as agent tools; install it
with `pip install "dex-agents[mcp]"`, which adds the MCP SDK (`mcp>=2.3,<3`).
See [MCP tools](#mcp-tools-dex_agentsmcp).

For OpenTelemetry spans, install the `otel` extra, which adds only
`opentelemetry-api`; see [Tracing](#tracing).

The package has these modules. Optional dependencies are extras, so no
module needs a new package name:

| Module | Install | Contents |
| --- | --- | --- |
| `dex_agents` | `dex-agents` | The session Flow, the runner contract, built-in tools, `AgentSessionClient`, and tracing options |
| `dex_agents.stepwise` | `dex-agents` | Stepwise mode: `AgentDefinition`, function tools, the `ModelClient` protocol, and the runner |
| `dex_agents.litellm` | `dex-agents[litellm]` | `LiteLLMModelClient`, a `ModelClient` on LiteLLM |
| `dex_agents.mcp` | `dex-agents[mcp]` | `McpToolSource`, MCP servers' tools as agent tools |
| `dex_agents.ui_stream` | `dex-agents` | Server-Sent Event frames and snapshot JSON for UIs |
| `dex_agents.testing` | `dex-agents` | Private engines, Worker processes, gates, Step history, and `ScriptedModelClient` |

Spans need the `otel` extra. Importing `dex_agents.litellm` without its extra
raises `ImportError` that names `dex-agents[litellm]`.

Develop the package inside this repository with uv 0.12.2. The adapters in
this repository resolve it from `../core` by path through
`[tool.uv.sources]`; their published wheels require it from PyPI instead:

```bash
cd agents/python/core
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

An adapter requires `dex-agents==<same version>`. All agents packages share
one version line; see the
[compatibility table](https://github.com/superdurable/dex/blob/main/agents/README.md#versions-and-compatibility).

## Running an agent

```python
from dex import AsyncClient, AsyncWorker, BlobCacheConfig, ClientOptions, Registry
from dex import WorkerOptions, open_blob_cache
from dex_agents import AgentSessionClient, AgentSessionFlow, AgentSessionOptions
from dex_agents import ToolEffect, ToolPolicy

flow = AgentSessionFlow(
    MyFrameworkRunner(agent),
    flow_type="TriageAgent",
    options=AgentSessionOptions(
        tool_policies={"search_tickets": ToolPolicy(ToolEffect.READ)},
    ),
)
registry = Registry([flow], allow_async_handlers=True)
cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024 * 1024))
worker = AsyncWorker(registry, cache, WorkerOptions(server_address="localhost:8801"))
client = AsyncClient(registry, cache, ClientOptions(server_address="localhost:8801"))

sessions = AgentSessionClient(client, flow)
await sessions.start_session("session-42", "Find my open tickets")
snapshot = await sessions.get_agent_snapshot("session-42")
```

Register one `AgentSessionFlow` per agent, each with its own `flow_type`. All
Steps are `async`, so the Registry needs `allow_async_handlers=True` and an
`AsyncWorker` serves it. Tool calls run as parallel Steps, so give the Worker
enough task slots (`PullWorkerOptions.max_concurrent_tasks`) for the largest
batch of tool calls a model makes. A session never completes by itself; it
waits for the next user message after every turn. Stop it with
`AsyncClient.stop_flow`.

## Stepwise mode

Stepwise mode is the built-in agent loop, for agents defined in Dex terms
without a framework. `StepwiseAgentRunner` implements the runner contract
below, so the session Flow, its Steps, approvals, and events are the ones
every adapter uses. Each `RunModel` makes exactly one model call through a
`ModelClient`, and every tool call is its own Step.

```python
from dex import Registry
from dex_agents import BuiltInTool, ToolEffect
from dex_agents.litellm import LiteLLMModelClient
from dex_agents.stepwise import AgentDefinition, ToolCallContext, function_tool
from dex_agents.stepwise import stepwise_agent_session_flow


@function_tool(effect=ToolEffect.READ)
async def lookup_order(order_id: str) -> dict[str, str]:
    """Look up an order by its ID."""
    return await orders.get(order_id)


@function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
async def refund_order(order_id: str, amount_cents: int, context: ToolCallContext) -> str:
    """Refund part of an order."""
    return await payments.refund(order_id, amount_cents, idempotency_key=context.idempotency_key)


support_agent = AgentDefinition(
    name="support",
    instructions="You help customers with their orders.",
    model=LiteLLMModelClient("openai/gpt-5-mini"),
    tools=[lookup_order, refund_order],
    built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.DURABLE_WAIT}),
)
flow = stepwise_agent_session_flow(support_agent, flow_type="SupportAgent")
registry = Registry([flow], allow_async_handlers=True)
```

`AgentDefinition` fields: `name`, `instructions` (a string, or a function of
`InstructionContext` evaluated in every `RunModel`), `model`, `model_settings`,
`tools`, `tool_policies`, `default_tool_effect`, `built_in_tools`,
`max_model_calls_per_turn` (100; after that many calls in one turn the next
call offers no tools, and a tool call the model makes anyway gets the result
`The tool call was not run: this turn reached its limit of <n> model calls.`
while the turn ends), `interrupt_deadline`, and `event_stream_capacity_bytes`.
A function tool may not share a name with an enabled built-in tool:
`AgentDefinition` refuses it, and so does `run_model_step`, before any model
request, when only the session options enable that built-in.
`stepwise_agent_session_flow` derives the `AgentSessionOptions` from it.

`@function_tool(effect=..., requires_approval=None, name=None,
description=None, parameters_schema=None)` turns a plain sync or async
function into a tool. The name defaults to the function name, the description
to the docstring, and the JSON Schema is derived from the type hints with the
standard library: `str`, `int`, `float`, `bool`, `None`, `list`, `dict`,
`Literal`, `Enum`, unions, dataclasses, `TypedDict`, and
`Annotated[T, "description"]`. A parameter typed `ToolCallContext` receives the
idempotency key, attempt, Flow ID, turn ID, tool call ID, event writer,
heartbeat, and the answers to the tool's questions. A sync function runs in a
worker thread. Arguments that break the schema, and an unknown tool name,
raise `ToolExecutionError`, which the model sees: arguments that are not JSON
give `invalid arguments for <tool>: arguments are not valid JSON`. A `str` result is the
output; any other result is serialized as JSON.

In `RunModel`, the runner builds the model input from committed state only:
the instructions, every stored item, and the results of the previous model
response from the final records. Every stored tool call is paired with a
result; a missing one becomes the outcome-unknown text, so an orphaned call
never reaches a provider. It stores items as they appear
(`ToolCallsRequested.new_messages`), so `FrameworkRunState` holds only the
turn's model call count.

### Model clients

```python
class ModelClient(Protocol):
    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse: ...
    def estimate_input_tokens(self, request: ModelRequest) -> int: ...
```

Every client makes one request per call with no retries of its own, sends the
whole input every time with no provider-side conversation state, and returns
each output item provider-exact. `ModelOutputItem.item` is the provider's JSON
text: opaque fields such as encrypted reasoning content and thinking
signatures are kept byte for byte and replayed unchanged to the same provider
and API. An item from another provider or API replays as its text and tool
calls, and its reasoning is dropped. User text and tool results are stored in
a provider-neutral form.

`LiteLLMModelClient(model, api=None, api_base=None, api_key_provider=None,
request_options=None)` uses the Responses API for OpenAI models, with
`store=False` and `include=["reasoning.encrypted_content"]`, and streaming
Chat Completions for every other provider, keeping Anthropic `thinking_blocks`
with their `signature`. It streams text and reasoning summaries to
`text_delta` and `reasoning_delta` events and reports usage. Credentials come
from the Worker's environment, as LiteLLM reads them, or from
`api_key_provider`; never from Flow input or Attributes.

`dex_agents.testing.ScriptedModelClient(respond)` answers each call from a
function of the `ModelRequest`, so tests need no model key.

## Built-in tools: questions and durable waits

Built-in tools are session features, so they work under every runner. Enable
them in `AgentSessionOptions.built_in_tools`. The runner offers the enabled
definitions from `BUILT_IN_TOOL_DEFINITIONS` (filter by
`turn.enabled_built_in_tools`) to its model, and `RunModel` routes their calls
to their own Steps with no approval and the `read` effect. An enabled name is
reserved; a disabled one is an ordinary tool name. Invalid arguments record
`failed` with `invalid <tool> arguments: <reason>` at once. `null` for an
optional argument means the argument is absent, because providers in
strict-schema mode send it.

- `ask_user` `{question, choices?, allows_free_text?}` raises an interrupt of
  kind `question` with the tool call ID as its interrupt ID and waits in
  `AwaitQuestionAnswer`. `answer_question(interrupt_id, answer)` answers it;
  the model sees `{"status":"answered","answer":"<text>"}` as the tool result
  and continues the same turn. A second answer, a blank answer, or an answer
  outside `choices` when free text is not allowed returns `False`.
  `resolve_interrupt(..., approved=False)` declines it (`The user declined to
  answer.`), and `interrupt_deadline` applies (`question deadline passed`).
- `durable_wait` `{duration_seconds, reason}` records `waiting_for_timer` with
  `wait_until` and waits in `AwaitTimer` on a Timer. No model call, coroutine,
  or Worker holds the wait. The model then sees
  `{"status":"completed","waited_seconds":<n>,"reason":"<reason>"}`. Durations
  are whole seconds from 1 to `max_durable_wait` (365 days by default).

A tool can ask a question too: `execute_deferred_tool_call` returns
`ToolInputRequested(question, choices, allows_free_text, continuation)` instead
of a result, only when the call has taken no effect yet. The tool Step stores
the question on its record and moves to `AwaitQuestionAnswer`, whose Execute
raises the `question` interrupt `<tool_call_id>-input-<n>` under the
`PendingInterrupts` lock, so parallel tool calls can ask at once. If another
call of the turn already uses that ID, for its record or a pending interrupt,
the asking call records `failed` instead. After the
answer, the session runs the same tool Step type again with `DeferredToolExecution.input_answers` and
`tool_call.input_continuation`. A function tool returns it from the function
and reads the answers from `ToolCallContext`.

## The runner contract

An adapter implements three methods. The session calls them from Steps, so
each call may run again after a crash or retry. Keep the runner stateless
between calls and derive everything from the arguments.

```python
class AgentFrameworkRunner(Protocol):
    def build_user_message(self, text: str) -> FrameworkMessage: ...
    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome: ...
    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome: ...

class UnfinishedTurnMessageBuilder(Protocol):  # optional
    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> Sequence[FrameworkMessage]: ...
```

`DeferredToolOutcome` is `ToolExecutionResult | ToolInputRequested`.
`AgentFrameworkRunner` is generic in what `execute_deferred_tool_call` returns,
and a bare `AgentFrameworkRunner` means `AgentFrameworkRunner[ToolExecutionResult]`,
as in 0.1.0. A runner whose tools ask the user implements
`AgentFrameworkRunner[DeferredToolOutcome]`.

`AgentTurnOutcome` is `TurnCompleted | ToolCallsRequested`.

### `build_user_message(text)`

Return `FrameworkMessage(role=MessageRole.USER, framework_item=..., text=text)`.
The session stores it as the turn's first message before the first
`RunModel`. `RecordTurnFailure` also calls it for the turn-failure note. Any
other role fails the Step.

### `run_model_step(turn)`

The framework makes **at most one model call** per `run_model_step`, and
stops before it runs any tool it can defer. `AgentTurnContext` carries
committed state:

| Field | Meaning |
| --- | --- |
| `flow_id`, `turn_id`, `attempt` | Session ID, `"turn-<n>"`, and the one-based `RunModel` attempt. |
| `messages` | Every stored `AgentMessage` in sequence order. The last user message belongs to this turn. |
| `framework_run_state` | `None` for the turn's first model call; otherwise the value from this turn's last `ToolCallsRequested`. `is_resuming` tests it. |
| `tool_calls` | Every `AgentToolCall` record of this turn by tool call ID. When `RunModel` runs, every record is final. `recorded_tool_call(id)` reads one. |
| `resolve_tool_policy(name, framework_effect=None)` | The policy for a tool: explicit policy, else framework metadata, else `default_tool_effect`. |
| `events` | `AgentEventWriter` bound to the turn, Step execution, and attempt: `write_text_delta`, `write_reasoning_delta`, `write_tool_call_started`, `write_tool_call_finished`, `write_event`. Safe to call from any thread; writes after the call returns are dropped. |
| `heartbeat()` | Records liveness. The session already heartbeats every 20 seconds in the background. |
| `enabled_built_in_tools` | The built-in tools to offer to the model, from `AgentSessionOptions.built_in_tools`. |

A runner must:

1. Without run state, rebuild framework input from each message's
   `framework_item`. With run state, restore the framework from it and give
   every tool call the result from its final record instead of running the
   tool. `describe_tool_call_outcome(record)` renders the model-visible text.
   A framework with a native rejection API may pass `record.rejection_message`
   there instead.
2. Let the framework make one model call.
3. If the response asks for tools, return `ToolCallsRequested(framework_run_state,
   tool_calls, usage)` with one `DeferredToolCall(tool_call_id, tool_name,
   arguments, policy)` per call, in model order. Get each `policy` from
   `turn.resolve_tool_policy(name, framework_effect)`; pass `framework_effect`
   when the framework exposes metadata, such as MCP `readOnlyHint` (`READ`) or
   `idempotentHint` (`IDEMPOTENT_WRITE`). `framework_run_state` must be
   non-empty and must continue the turn after the tools. Tool call IDs must be
   non-empty, contain no `/`, and be unique within one turn; IDs may repeat in
   later turns, because the idempotency key includes the turn ID.
4. If the response is the final answer, return `TurnCompleted(new_messages,
   usage)`. `new_messages` holds the turn's items the session has not stored
   yet, in order: model output, tool requests, tool results, and the final
   answer. The session appends them in one commit. A runner may instead store
   items as they appear with `ToolCallsRequested.new_messages`, which the
   session appends in the commit that records the tool calls; such a runner
   implements `build_unfinished_turn_messages`, which `RecordTurnFailure`
   calls to store the results of the turn's tool calls before its note. If it
   raises or returns anything but `FrameworkMessage` values, the session logs a
   warning and stores only the note. A runner that sets `new_messages` without
   the method fails its turn after that one model call, with no retry.
5. Report `usage` for this model call only. The session adds it to the session
   total. Optionally return `model_call_metadata=ModelCallMetadata(
   request_model, response_model, provider_name, response_id, finish_reasons)`
   in either outcome. The session never stores it; it names the `chat` span
   (see [Tracing](#tracing)). Leave a field `None` when the framework does not
   expose it.
6. Never run a tool inside `run_model_step`, unless the framework cannot defer
   it. Document such tools as at-least-once: a `RunModel` retry runs them
   again.

Any exception fails the attempt. `RunModel` retries up to three attempts
within 30 minutes, rerunning only this one model call, then routes to
`RecordTurnFailure`. Each attempt has a 15 minute timeout.

When a failed turn already has final tool call records, `RecordTurnFailure`
stores one note before it deletes them, built with `build_user_message`, so
the next turn's model does not repeat a write. `build_turn_failure_note`
renders it, one line per record in `deferral_order`:

```text
[Dex] The previous turn failed after these tool calls ran:
- <tool_name> <arguments> -> <status>: <describe_tool_call_outcome(record)>
Do not repeat these tool calls unless the user asks again.
```

### `execute_deferred_tool_call(call)`

`DeferredToolExecution` carries `flow_id`, the approved `tool_call` record,
`idempotency_key` (`"<flow_id>/<turn_id>/<tool_call_id>"`, the same on every
attempt), `attempt`, `events`, `heartbeat`, `heartbeat_checkpoint`,
`save_heartbeat_checkpoint`, and `input_answers`. Run the real tool once and
return `ToolExecutionResult(output)`, or `ToolInputRequested` when the tool
needs an answer before it takes any effect. Tool calls of one model response run in
parallel Steps, so this method may run concurrently with itself.

`save_heartbeat_checkpoint(text)` records a heartbeat that carries `text`, and
every later heartbeat of the attempt, the session's background heartbeats
included, carries it too. A retried attempt reads it as `heartbeat_checkpoint`
and can resume work the earlier attempt started, such as polling a job,
instead of starting it again. A `DeferredToolCall` may also carry
`tool_source_reference`, opaque text stored on the call's `AgentToolCall`
record (wire field `tool_source_reference`), so the tool's source can resolve
the tool in the tool Step without listing it again.

Raise `ToolExecutionError(message)` for a failure the model should see; the
session records `failed` with that message and does not retry. Any other
exception is a Step failure. `read` and `idempotent_write` calls retry up to
three attempts, and then `RecordToolFailure` records `failed` for a read and
`outcome_unknown` for a write, because the write may have taken effect.
Single-attempt calls do not retry; `RecordToolOutcomeUnknown` records
`outcome_unknown`.

Before calling the runner, the tool Step writes `tool_call_started` and awaits
a heartbeat. Dex can redeliver a task to another Worker at the same attempt
when the first Worker disconnects before Dex receives any output, so this
output narrows that window before the tool runs. Awaiting the heartbeat only
queues the frame on the Worker; it does not wait for Dex to receive it.

## Effects and policies

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

`ToolPolicy(effect, requires_approval=None)` overrides the defaults per tool
name; `None` uses the effect's default approval, which `is_approval_required`
reports. `AgentSessionOptions` also sets `default_tool_effect` (default
`unclassified`), the `AgentEvents` Stream capacity (default 10 MiB), and an
optional `interrupt_deadline`, `built_in_tools`, and `max_durable_wait`. A tool
whose policy requires approval runs only after `AwaitToolApproval` approves it.

## Flow shape

```text
StartAgentSession -> AwaitUserMessage -> RunModel
RunModel -> TurnCompleted -> AwaitUserMessage
RunModel -> ToolCallsRequested -> go_to_many(
              AwaitToolApproval(id) -> CallTool(id) or CallSingleAttemptTool(id)   per call needing approval
              CallTool(id) or CallSingleAttemptTool(id)                            per other call
              AwaitQuestionAnswer(id)                                              per ask_user call
              AwaitTimer(id)                                                       per durable_wait call
              ResumeTurn(expected_result_count))
CallTool(id) -> ToolInputRequested -> AwaitQuestionAnswer(id) -> CallTool(id)
ResumeTurn -> RunModel
```

| Step (input) | What it does | Next |
| --- | --- | --- |
| `StartAgentSession` (`AgentSessionStartInput`) | Start Step. Initializes state, usage, and interrupts. | `RunModel` with a non-blank `initial_message`, else `AwaitUserMessage` |
| `AwaitUserMessage` (none) | Waits for `QueuedUserMessages.for_one()`, stores the user message, starts the turn. | `RunModel` |
| `RunModel` (none) | Calls `run_model_step`. 15 min timeout, 2 min heartbeat timeout, 3 attempts in 30 min, failure route `RecordTurnFailure`. On `ToolCallsRequested`, stores the run state, one `AgentToolCalls` record per call, and the interrupts. | `AwaitUserMessage`, or `go_to_many` with one Step per tool call plus `ResumeTurn` |
| `AwaitToolApproval` (tool call ID) | Waits for `InterruptResolutions.for_one(id)`, plus a Timer at the deadline, rounded up to whole seconds, while the interrupt is pending. Approved: marks the record `approved`. Rejected or expired: records `rejected` and publishes `ToolCallResults`. A Timer that fires before the deadline waits again. | `CallTool` or `CallSingleAttemptTool`, or ends |
| `AwaitQuestionAnswer` (tool call ID) | Waits for `InterruptResolutions.for_one(interrupt_id)`, plus a deadline Timer while the question is pending, or a zero Timer while a tool's question is not raised yet. Locks `PendingInterrupts` in Execute, where it raises a tool's question. Records the answer, decline, or deadline and publishes `ToolCallResults`, or returns a tool question's call to its tool Step. | Ends, the tool Step, or itself |
| `AwaitTimer` (tool call ID) | Waits on a Timer for the whole seconds until `wait_until`. Records `succeeded` and publishes `ToolCallResults`. | Ends |
| `CallTool` (tool call ID) | Runs one `read` or `idempotent_write` call. 3 attempts, failure route `RecordToolFailure`. Records `succeeded`, or `failed` for `ToolExecutionError`, and publishes `ToolCallResults`. A `ToolInputRequested` result raises a question instead. | Ends, or `AwaitQuestionAnswer` |
| `CallSingleAttemptTool` (tool call ID) | Runs one `non_idempotent_write` or `unclassified` call. 1 attempt, failure route `RecordToolOutcomeUnknown`. | Ends |
| `RecordToolFailure` (tool call ID) | Records `failed` for a read and `outcome_unknown` for a write, with the recovery detail in `error`, and publishes `ToolCallResults`. | Ends |
| `RecordToolOutcomeUnknown` (tool call ID) | Records `outcome_unknown` with the recovery detail in `error`, and publishes `ToolCallResults`. | Ends |
| `ResumeTurn` (expected result count) | Waits for `ToolCallResults.for_n(count)`. | `RunModel` |
| `RecordTurnFailure` (none) | Records `last_turn_error`. Appends the runner's unfinished-turn messages when it implements `build_unfinished_turn_messages`. When the turn has final tool call records, appends one turn-failure note. Clears the turn's records and run state. | `AwaitUserMessage` |

Every Step sets `wait_for` and `execute` durability to SYNC explicitly, so a
`FlowConfig(step_durability=ASYNC)` never replays a Step result. Each tool
call Step and its failure route receive the tool call ID as input, so parallel
Steps never share state. Every tool call publishes exactly one `ToolCallResults`
message, when its record becomes final, and a tool Step whose record is already
final ends without running or publishing. `ResumeTurn` goes back to waiting if
a consumed result left a record of the turn unfinished. `AwaitToolApproval`
and the tool Steps load only their own `AgentToolCalls` instance, through
per-movement `StepOptions`.

Persistence, shared by every agent Flow type and exported from `dex_agents`:

| Name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute `AGENT_STATE` | `AgentState` |
| `AgentMessages` | AttributeMap `AGENT_MESSAGES`, key `message_sequence_key(n)` (12-digit zero-padded) | `AgentMessage` |
| `FrameworkRunState` | Attribute `FRAMEWORK_RUN_STATE` | string, set only while a turn waits for tool calls |
| `AgentToolCalls` | AttributeMap `AGENT_TOOL_CALLS`, key = tool call ID | `AgentToolCall`, deleted when the turn ends |
| `PendingInterrupts` | Attribute `PENDING_INTERRUPTS` | `list[AgentInterrupt]` |
| `AgentUsage` | Attribute `AGENT_USAGE` | `AgentUsage` |
| `QueuedUserMessages` | Channel `QUEUED_USER_MESSAGES` | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap `INTERRUPT_RESOLUTIONS`, key = interrupt ID | `AgentInterruptResolution` |
| `ToolCallResults` | Channel `TOOL_CALL_RESULTS`, internal | `ToolCallCompleted` |
| `AgentTraceContext` | Attribute `AGENT_TRACE_CONTEXT` | string, the W3C `traceparent` of the current activation's root span when its context is valid, sampled or not; deleted otherwise |
| `AgentEvents` | Stream `flow.agent_events`, one per Flow type | `AgentEvent` |

`AgentState.status` is `waiting_for_user_message`, `running_model`, or
`running_tools`. An `AgentToolCall` record is `awaiting_approval`, `approved`,
`awaiting_answer`, or `waiting_for_timer` while it waits, and `rejected`,
`succeeded`, `failed`, or `outcome_unknown` when final; runners only see final
records.

RPCs:

| RPC | Input | Output | Notes |
| --- | --- | --- | --- |
| `send_user_message` | `AgentUserMessage` | `bool` | Transactional; `False` for blank text. |
| `resolve_interrupt` | `ResolveInterruptRequest` | `bool` | Locks `PendingInterrupts`; `False` when not pending or past its deadline; removes the interrupt and publishes the answer in one commit. On a question, `approved=False` declines it and `approved=True` returns `False`. A concurrent Step can raise `RpcLockConflictError`; `AgentSessionClient` retries it for 5 seconds. |
| `answer_question` | `AnswerQuestionRequest` | `bool` | Locks `PendingInterrupts`; `False` when the interrupt is not a pending question, is past its deadline, or the answer is blank or not an allowed choice; removes it and publishes the answer in one commit. `AgentSessionClient.answer_question` retries lock conflicts like `resolve_interrupt`. |
| `get_agent_snapshot` | none | `AgentSnapshot` | State, last 200 messages, pending interrupts, the active turn's tool calls, usage, queued message count. Serves retained closed sessions. |
| `list_agent_messages` | `ListAgentMessagesRequest` | `AgentMessagesPage` | Oldest first; limit clamped to 1..200. |

Events on `AgentEvents` (`AgentEventType`): `turn_started` `{"sequence"}`,
`turn_completed` `{"message_count"}`, `turn_failed` `{"error"}`, `text_delta`
and `reasoning_delta` `{"text"}`, `tool_call_started` `{"tool_call_id",
"tool_name"}`, `tool_call_finished` `{"tool_call_id", "tool_name", "status"}`
(once per tool call, including rejections), `tool_call_deferred`
`{"tool_call_id", "tool_name", "effect", "requires_approval"}`,
`interrupt_raised` `{"interrupt_id", "tool_call_id", "tool_name", "deadline",
"kind", "question", "choices", "allows_free_text"}`, `interrupt_resolved`
`{"interrupt_id", "approved", "rejection_message", "kind", "answered",
"cancellation_reason"}`, `timer_started` `{"tool_call_id", "wait_until",
"reason"}`, and `usage_recorded` `{"recorded", "session"}`.

Every event carries `turn_id`, `step_execution_id` (shared by all attempts of
one Step execution), and the Step `attempt`. A UI groups live text by
`(turn_id, step_execution_id, attempt)`: within one `step_execution_id` it
keeps only the highest `attempt` seen and drops the partial text of lower
attempts. Different `step_execution_id` values of a turn are separate model
call and tool call Steps, and each keeps its text. After `turn_completed` or
`turn_failed`, replace the turn's live text with the stored messages. Streams
are best effort; rebuild from `get_agent_snapshot`.

## MCP tools (`dex_agents.mcp`)

`dex_agents.mcp` turns the tools of MCP servers into tools of an agent
session. Every MCP tool call runs in its own tool Step, with an effect class
from the tool's annotations, so a crash never repeats a write silently.
Install the MCP SDK with the `mcp` extra; the core itself never imports it:

```bash
pip install "dex-agents[mcp]"
```

Configure the servers on the Worker, never from Flow input, a user message, or
a browser: a stdio server's command runs with the Worker's privileges.

```python
from dex_agents.mcp import McpStdioServerConfig, McpStreamableHttpServerConfig, McpToolSource

tools = McpToolSource([
    McpStdioServerConfig(name="files", command="mcp-server-files", args=["/srv/reports"]),
    McpStreamableHttpServerConfig(
        name="tickets",
        url="https://tickets.example.com/mcp",
        headers={"Authorization": f"Bearer {os.environ['TICKETS_TOKEN']}"},
    ),
])
```

A runner offers the tools and defers every call; the tool Step runs it:

```python
class MyRunner:
    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        offered = await tools.list_tools()  # McpTool: name, description, input_schema
        response = await call_my_model(turn.messages, offered)
        if not response.tool_calls:
            return TurnCompleted(new_messages=..., usage=...)
        return ToolCallsRequested(
            framework_run_state=serialize(response),
            tool_calls=[
                tools.deferred_tool_call(
                    turn, tool_call_id=call.id, tool_name=call.name, arguments=call.arguments
                )
                for call in response.tool_calls
            ],
            usage=...,
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        handlers = {**self._local_handlers, **await tools.deferred_tool_handlers()}
        return await handlers[call.tool_call.tool_name](call)
```

`list_tools` lists each server on first use and caches its tools on their
own; `list_tools(refresh=True)` lists every server again. A server that cannot
be listed is left out with a warning log and tried again on the next call, so
one bad server never hides the others' tools. `deferred_tool_call` stores the
server and the tool's MCP name on the call's record, so the tool Step calls
that server directly, without a listing; `call_tool` finds the server through
the listing. Tool names are `<server>__<tool>`, with characters other than
letters, digits, `_`, and `-` replaced by `_` and names over 64 characters
shortened with a hash. When two tools get the same name, the first in server
order is offered and the other is left out with a warning log. Key
`ToolPolicy` entries by that name.

### Effects

| MCP annotations | Effect | Tool Step | Approval by default |
| --- | --- | --- | --- |
| `readOnlyHint: true` | `read` | `CallTool` | No |
| `idempotentHint: true` | `idempotent_write` | `CallTool` | No |
| anything else, or none | `default_tool_effect`, `unclassified` by default | `CallSingleAttemptTool` | Yes |

`idempotentHint` makes a tool `idempotent_write` whatever `destructiveHint`
says, in both cores. A `ToolPolicy` in `AgentSessionOptions.tool_policies` wins
over the annotations. `trusts_tool_annotations` defaults to `True`, so a server
with a wrong `readOnlyHint` on a write gets that write run without approval and
retried up to three times. Set `trusts_tool_annotations=False` on a server
whose hints you do not trust; its tools then keep the session default unless a
policy names them.

### What a call does

Every request opens a new connection: a new process for a stdio server, a new
HTTP connection for a Streamable HTTP server. Connecting and each handshake
request are bounded by `connection_timeout` (30 seconds by default), and a
`tools/call` by `request_timeout` (10 minutes). The source speaks MCP 2026-07-28,
which has no protocol session, and falls back to the `initialize` handshake for
older servers, so a server restart between listing and calling is harmless.
Every `tools/call` carries the session's idempotency key,
`<flowId>/<turnId>/<toolCallId>`, in `_meta` under
`IDEMPOTENCY_KEY_META_KEY` (`io.superdurable.dex/idempotencyKey`). The MCP SDK
adds `traceparent` to `_meta` when OpenTelemetry has an active trace.

| What happens | Recorded | Re-issued? |
| --- | --- | --- |
| A result | `succeeded`: text blocks joined with newlines, other content as JSON | - |
| An `isError` result, or a JSON-RPC error response other than `-32603` | `failed` with the text (`ToolExecutionError`) | No |
| The call was never sent: its server is not configured, or 3 connection attempts failed | `failed` | No |
| The stream broke, the server exited, or the request timed out after it was sent; an internal error (`-32603`), an HTTP error status, or a body that is not JSON-RPC (`-32700`), which a gateway can return after the server ran the call; a server that keeps asking to repeat the call | `McpToolCallInterruptedError`, a Step failure | `read` and `idempotent_write`: `CallTool` sends a new request, same idempotency key, up to 3 attempts. Other effects: `outcome_unknown`, never sent again |
| The Worker dies during the call | as for any tool Step | Same as above |

MCP 2026-07-28 removed stream resumption, so a broken stream is never resumed;
the effect class alone decides whether the call is sent again.

`notifications/progress` become `mcp.tool_progress` events on `AgentEvents`
(`{"tool_call_id", "tool_name", "progress", "total", "message"}`).

### Long-running MCP tasks

The source declares the tasks extension, `io.modelcontextprotocol/tasks`, on
every request. A server may then answer `tools/call` with a task handle. The
tool Step polls `tasks/get` at the server's `pollIntervalMs`, writes an
`mcp.task_status` event per observed status, and heartbeats between polls,
for at most `task_wait_timeout` (10 minutes by default) per attempt. A
completed task is the tool's result; a failed or cancelled task records
`failed`. MCP 2026-07-28 has no protocol session and lets the server choose
the task TTL, so any connection can poll the task. For a Streamable HTTP
server, the tool Step saves the task ID as its
heartbeat checkpoint before the first poll. When the wait passes, or the Worker
dies while polling, the task keeps running: `CallTool` retries a `read` or
`idempotent_write` call, and the retry polls the same task instead of calling
the tool again; if the server no longer knows the task, the retry sends the
call again. Other effects record `outcome_unknown`. A stdio task lives in its
server process, so it is polled on the call's own connection and a retry calls
the tool again.

Polling inside the tool Step holds a Worker slot for the whole wait. The
follow-up design keeps no connection open: the tool Step stores the task ID in
the call's record and moves to a `PollMcpTask` Step that waits on a Timer at
`pollIntervalMs`, polls once per execution, and records the result, so a task
can run for days and a Worker restart only delays the next poll.

### Input requests

An `input_required` answer, or a task in `input_required`, records `failed`
with `the MCP server asked for input that this agent session cannot provide
yet (...)`, naming each request, such as `an answer to 'Which account?'`; a
waiting task is cancelled. The follow-up maps each input request to a session
question interrupt: the tool Step raises it, the answer RPC resumes the call,
and the call is retried with `inputResponses` and the echoed `requestState`.
A state-only `input_required` answer, which only asks for the same request
again, is retried at once.

### With a framework adapter

The runner above is the core's runner-agnostic path. A framework adapter uses
the source when it accepts tools defined by a JSON Schema: wrap each `McpTool`
as a framework tool whose function calls `tools.call_tool(name, arguments,
idempotency_key=...)` with the key from the adapter's tool context, and pass
`{**tools.tool_policies(), **your_policies}` as the adapter's `tool_policies`.
No adapter package changes for this.

| Adapter | Schema-only tool | Key inside the tool |
| --- | --- | --- |
| `dex-agents-openai-agents` | `FunctionTool(params_json_schema=..., on_invoke_tool=...)` | `get_current_deferred_tool_execution()`, the whole call |
| `dex-agents-pydantic-ai` | `Tool.from_schema(...)` | `deferred_tool_idempotency_key(ctx)` |
| `dex-agents-langgraph` | `StructuredTool` with a JSON Schema `args_schema` | `get_tool_call_idempotency_key(config)` |
| `dex-agents-google-adk` | a `BaseTool` with its own `FunctionDeclaration` | `idempotency_key_for_tool_call(tool_context)` |
| `dex-agents-claude-agent-sdk` | `claude_agent_sdk.tool(name, description, schema)` | `current_deferred_tool_execution()`; write calls only, reads run inside the harness |

Pydantic AI, LangGraph, and Google ADK also defer their own MCP client tools
into tool Steps; use the source there for the shared idempotency key and
annotation handling. The OpenAI Agents SDK runs its own MCP servers inline in
`RunModel`, so a wrapper is its only path to tool Steps.

## Browser streams

`dex_agents.ui_stream` lets a browser render a live session from its
`AgentEvents` Stream and recover after a reconnect. It needs no store besides
Dex and no web framework. An application mounts two endpoints:

- **Snapshot.** `read_agent_event_stream_snapshot(client, flow, session_id)`
  returns the `get_agent_snapshot` result plus the Dex resume token to open the
  event stream from. Serve its `to_json()`: `{"snapshot": ..., "resume_token":
  ...}` in the wire format. While a turn is active, the token replays that
  turn's events, so the browser can rebuild its live text.
- **Events.** `await stream_agent_event_frames(client, flow, session_id,
  resume_token=...)` checks the token with one short read, then returns a
  generator of Server-Sent Event frames. Each frame's `id` is the event's Dex
  resume token and its `data` is the `AgentEvent` wire JSON, with `payload` as
  JSON text. A reconnecting `EventSource` sends the last `id` as
  `Last-Event-ID`; pass it as `resume_token` and the stream continues with the
  next event, with no repeated or missing event while the Stream retains them.
  After each empty long poll it yields `KEEP_ALIVE_FRAME`, an SSE comment. A
  token that Dex rejects, such as one of another session, raises
  `InvalidAgentEventResumeTokenError` before the response starts; answer 400,
  which stops the `EventSource`, and let the browser load the snapshot again.

The `_sync` variants, `read_agent_event_stream_snapshot_sync` and
`stream_agent_event_frames_sync`, take a synchronous `Client` for WSGI
servers. Send `AGENT_EVENT_STREAM_HEADERS` with the event response. The JSON
matches the TypeScript core's `@superdurable/dex-agents/ui-stream`, which also
offers an AI SDK UI message stream for `useChat`.

**Authorize every route.** The session ID and the resume token come from the
browser, so each route must check that the caller may read the session it
names before it calls a helper.

FastAPI or Starlette, with an `AsyncClient`:

```python
from typing import Any

from fastapi import FastAPI, Request, Response
from fastapi.responses import StreamingResponse

from dex_agents.ui_stream import (
    AGENT_EVENT_STREAM_HEADERS,
    InvalidAgentEventResumeTokenError,
    read_agent_event_stream_snapshot,
    stream_agent_event_frames,
)

app = FastAPI()


@app.get("/sessions/{session_id}/snapshot")
async def get_session_snapshot(session_id: str, request: Request) -> dict[str, Any]:
    await authorize_session(request, session_id)
    return (await read_agent_event_stream_snapshot(client, flow, session_id)).to_json()


@app.get("/sessions/{session_id}/events")
async def get_session_events(session_id: str, request: Request) -> Response:
    await authorize_session(request, session_id)
    resume_token = request.headers.get("last-event-id") or request.query_params.get(
        "resume_token", ""
    )
    try:
        frames = await stream_agent_event_frames(
            client, flow, session_id, resume_token=resume_token
        )
    except InvalidAgentEventResumeTokenError:
        return Response(status_code=400)
    return StreamingResponse(frames, headers=dict(AGENT_EVENT_STREAM_HEADERS))
```

Starlette without FastAPI uses the same calls in `Route` handlers, returning
`JSONResponse(value.to_json())` and `StreamingResponse(frames, headers=...)`.

Quart, with an `AsyncClient`. Turn off the response timeout, because the
stream stays open:

```python
from quart import Quart, make_response, request

app = Quart(__name__)


@app.get("/sessions/<session_id>/snapshot")
async def get_session_snapshot(session_id: str):
    await authorize_session(request, session_id)
    return (await read_agent_event_stream_snapshot(client, flow, session_id)).to_json()


@app.get("/sessions/<session_id>/events")
async def get_session_events(session_id: str):
    await authorize_session(request, session_id)
    resume_token = request.headers.get("Last-Event-ID") or request.args.get("resume_token", "")
    try:
        frames = await stream_agent_event_frames(
            client, flow, session_id, resume_token=resume_token
        )
    except InvalidAgentEventResumeTokenError:
        return "", 400
    response = await make_response(frames, dict(AGENT_EVENT_STREAM_HEADERS))
    response.timeout = None
    return response
```

Flask, with a synchronous `Client`. Each open stream holds one worker thread,
so serve it from a threaded or greenlet server:

```python
from flask import Flask, Response, request

from dex_agents.ui_stream import (
    AGENT_EVENT_STREAM_HEADERS,
    InvalidAgentEventResumeTokenError,
    read_agent_event_stream_snapshot_sync,
    stream_agent_event_frames_sync,
)

app = Flask(__name__)


@app.get("/sessions/<session_id>/snapshot")
def get_session_snapshot(session_id: str):
    authorize_session(request, session_id)
    return read_agent_event_stream_snapshot_sync(sync_client, flow, session_id).to_json()


@app.get("/sessions/<session_id>/events")
def get_session_events(session_id: str) -> Response:
    authorize_session(request, session_id)
    resume_token = request.headers.get("Last-Event-ID") or request.args.get("resume_token", "")
    try:
        frames = stream_agent_event_frames_sync(
            sync_client, flow, session_id, resume_token=resume_token
        )
    except InvalidAgentEventResumeTokenError:
        return Response(status=400)
    return Response(frames, headers=dict(AGENT_EVENT_STREAM_HEADERS))
```

In the browser, load the snapshot, then open the event stream from its token:

```js
const { snapshot, resume_token } = await (await fetch(`/sessions/${sessionId}/snapshot`)).json();
render(snapshot);
const events = new EventSource(
  `/sessions/${sessionId}/events?resume_token=${encodeURIComponent(resume_token)}`,
);
events.onmessage = (message) => applyAgentEvent(JSON.parse(message.data));
```

On a reconnect the browser repeats the URL and adds `Last-Event-ID`, which the
routes above prefer to the query parameter. The browser applies the event
rules in [Flow shape](#flow-shape): within one `step_execution_id` it keeps
only the highest `attempt`, so a `RunModel` retried after a crash shows only
its new attempt's text. After `turn_completed` or `turn_failed`, and after any
long gap, it reloads the snapshot. Approve or reject a pending interrupt from
the snapshot with `AgentSessionClient.resolve_interrupt`.

Limits:

- A production Dex Server drops Stream writes until a Stream backend is
  enabled (`streamStore.backend`), and trimming can remove old events. With the
  Stream Store disabled, the snapshot still works, with an empty token, and the
  event stream yields only keep-alive comments; poll the snapshot instead.
- The event stream never ends by itself; the web framework closes it when the
  browser disconnects. An async generator stops when the framework cancels
  it; a synchronous one first finishes its current long poll, up to
  `poll_timeout`.
- Stopping in the browser only closes the response; a durable turn cancel is
  not in the session contract yet.

## Tracing

The session emits OpenTelemetry GenAI spans through the OpenTelemetry API. The
`otel` extra installs only `opentelemetry-api`; the application brings the SDK
and an exporter:

```bash
pip install "dex-agents[otel]" opentelemetry-sdk opentelemetry-exporter-otlp
```

Tracing is on by default and uses the global tracer provider, so an
application that configures the SDK gets spans without further setup. Without
the API, the session never imports OpenTelemetry. With the API but no SDK, the
spans are no-ops and the session stores nothing extra. `AgentTracingOptions`,
passed as `AgentSessionOptions.tracing`, changes the defaults:

```python
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from dex_agents import AgentSessionFlow, AgentSessionOptions, AgentTracingOptions

provider = TracerProvider()
provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
flow = AgentSessionFlow(
    MyFrameworkRunner(agent),
    flow_type="TriageAgent",
    options=AgentSessionOptions(
        tracing=AgentTracingOptions(tracer_provider=provider, agent_name="Triage"),
    ),
)
```

| Option | Default | Meaning |
| --- | --- | --- |
| `is_enabled` | `True` | `False` emits nothing and never imports OpenTelemetry. |
| `tracer_provider` | `None` | The provider to emit with; `None` uses the global provider. |
| `agent_name` | the Flow type | `gen_ai.agent.name` and the `invoke_agent` span name. |
| `should_capture_message_content` | `False` | Put message text, tool arguments, and tool results on spans. |
| `semantic_conventions_version` | `V1_42_0_DEV` | The GenAI semantic conventions the spans follow. |

### One trace per activation

An activation is a burst of Steps between durable waits. It starts when a Step
consumes an input from outside the session: `StartAgentSession` or
`AwaitUserMessage` with a user message, or `AwaitToolApproval` with an
interrupt answer or an expired deadline. It ends when every Step it started has
ended or waits again. Each activation is one trace:

| Span | Kind | Emitted by | Parent |
| --- | --- | --- | --- |
| `invoke_agent {agent name}` | `INTERNAL` | Each attempt of the Step that starts the activation | None. It is the trace root, with a span link to the previous activation's root. |
| `chat`, or `chat {model}` when the runner returns `ModelCallMetadata` | `CLIENT`; `INTERNAL` when the runner reports `model_calls` other than 1 | Each `RunModel` attempt, when it ends | The activation's root |
| `execute_tool {tool name}` | `INTERNAL` | Each `CallTool` and `CallSingleAttemptTool` attempt | The root of the activation that deferred or approved the call |

A turn without approvals is one trace. Each approval answer starts another,
linked to the one before, and so does each new turn. Every span ends inside its
Step attempt, so no span stays open across a durable wait, and every backend
sees a closed root.

The `invoke_agent` root ends with the Step that starts the activation, so its
duration is near zero, and the activation's later spans start after it ended.
That is on purpose. A span must end in the process that started it, and the
OpenTelemetry API cannot give a span an ID before it starts, so a root that
covered the whole activation would have to stay open across Steps, Workers,
and possibly days. Tracing backends drop or hide such traces. A closed root
with later children is a valid trace that every backend shows.

While a tool call runs, its `execute_tool` span is the current span. While a
model call runs, the activation's root is current, because the `chat` span
starts when the attempt ends: its kind depends on the `model_calls` the runner
reports. Framework and HTTP client instrumentation therefore nests under the
activation.

The Steps of one activation can run in different Worker processes. The root
therefore stores its W3C `traceparent`, about 55 bytes, in the
`AgentTraceContext` Attribute in the same commit as its Step. `RunModel` reads
it to parent its `chat` span and copies it onto each tool call record it
creates, as `AgentToolCall.trace_context`. An approval replaces the record's
copy with its own root. Each tool Step parents its spans from its record, so
overlapping approvals never swap tool spans between traces. The next
activation's root reads the Attribute for its link.

The Attribute is written whenever the root has a valid span context, sampled
or not. A sampled-out root stores its unsampled context, so under a
parent-based sampler the activation's other spans are dropped with it instead
of starting traces of their own. When a root has no valid context, because
its Worker has tracing disabled or has no OpenTelemetry API or SDK, that Step
deletes the stored value. The activation's later spans then start traces of
their own, instead of joining the previous activation's trace. The schema
always registers `AgentTraceContext`, so Workers with and without tracing can
serve one Flow type.

Every attempt is its own span with `dex.step.attempt`, and all attempts of one
Step execution share `dex.step.execution_id`, so a retry shows as a second span
of the same Step. A failed attempt has status `ERROR`, `error.type` set to the
exception class name, and the exception as an event. A `ToolExecutionError`
sets `error.type` to `ToolExecutionError`. An attempt whose Worker died exports
no span. `ResumeTurn`, `RecordToolFailure`, `RecordToolOutcomeUnknown`, and
`RecordTurnFailure` emit no span: the failed attempts before them already have
error spans.

### Attributes

| Span | Attribute | Value |
| --- | --- | --- |
| All | `gen_ai.conversation.id`, `session.id`, `dex.flow.id` | The Flow ID, which is the session ID |
| All | `dex.run.id` | The Flow run ID |
| All | `dex.step.type`, `dex.step.execution_id`, `dex.step.attempt` | The Step that emitted the span |
| All | `dex.agent.turn.id` | `turn-<n>` |
| All | `gen_ai.operation.name` | `invoke_agent`, `chat`, or `execute_tool` |
| `invoke_agent`, `execute_tool` | `gen_ai.agent.name` | `agent_name` |
| `invoke_agent` | `dex.agent.activation.trigger` | `user_message`, `interrupt_resolution`, or `interrupt_deadline` |
| `invoke_agent` | `dex.agent.user_message.key` | `AgentMessages` key of the user message |
| `invoke_agent` | `dex.agent.interrupt.id`, `dex.agent.interrupt.approved` | The interrupt and its decision |
| `chat` | `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.provider.name`, `gen_ai.response.id`, `gen_ai.response.finish_reasons` | From `ModelCallMetadata`, when the runner returns it |
| `chat` | `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` | From `usage`, when `model_calls` is 1 and the input or output count is not zero |
| `chat` | `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.reasoning.output_tokens` | `cached_input_tokens` and `reasoning_tokens`, under the same condition, when not zero |
| `chat` | `dex.agent.input_messages.last_key` | `AgentMessages` key of the newest stored message the call received |
| `chat` | `dex.agent.output_messages.keys` | `AgentMessages` keys of what this call produced: the `TurnCompleted` messages after the turn's last tool result |
| `chat` | `dex.agent.requested_tool_call.ids` | `AgentToolCalls` keys that a `ToolCallsRequested` created |
| `execute_tool` | `gen_ai.tool.name`, `gen_ai.tool.call.id` | The tool and its `AgentToolCalls` key |
| `execute_tool` | `dex.agent.tool.effect`, `dex.agent.tool_call.status` | The effect and the final status |

The `chat` span is named `chat {request_model}` when the runner returns
`ModelCallMetadata.request_model`, and `chat` otherwise. The adapters in this
repository do not return `ModelCallMetadata` yet, so their `chat` spans are
named `chat` and carry no model or provider until each adapter reports them.

A runner that reports `model_calls` other than 1, such as a whole-turn harness
that makes several model calls in one `RunModel`, gets an `INTERNAL` `chat`
span without usage attributes. Instrumentation of the provider client
typically traces each of those calls with its own usage, so a summed count on
the session's span would count the tokens twice. Usage still reaches the
`AgentUsage` Attribute.

A single model call can still be counted twice. When instrumentation of the
provider client, such as an OpenAI or Anthropic instrumentation library, also
traces the call, both its span and the session's `chat` span carry usage. Sum
usage from one instrumentation scope in the tracing backend, or remove the
session's usage attributes, for example with an OpenTelemetry Collector
transform processor:

```yaml
processors:
  transform/drop_dex_agents_usage:
    trace_statements:
      - context: span
        statements:
          - delete_matching_keys(attributes, "^gen_ai\\.usage\\.") where instrumentation_scope.name == "dex_agents"
```

### Tracing failures

Tracing never fails or retries a Step. Every OpenTelemetry call is guarded:
starting, annotating, and ending spans, changing the current context, and
serializing content. An exception there, such as one from a span processor or
exporter, is logged on the `dex_agents.tracing` logger and that span is
skipped. The first failure in a process logs a warning with its traceback;
later ones log at debug level. The Step's own result is unchanged, so a broken
processor can never send a write to `outcome_unknown` or rerun a tool.

### Message content

Spans carry content by reference by default: the Flow ID, Step execution ID,
`AgentMessages` keys, and tool call IDs above, because the content is already
durable in Dex. Read it with `get_agent_snapshot`, `list_agent_messages`, or
`AsyncClient.get_attributes`. With `should_capture_message_content=True`, spans
also carry content as JSON text in the GenAI message format:

- `invoke_agent`: `gen_ai.input.messages`, the user message.
- `chat`: `gen_ai.input.messages`, the stored messages plus the turn's tool
  calls and their recorded results, and `gen_ai.output.messages`, what this
  model call produced.
- `execute_tool`: `gen_ai.tool.call.arguments` and `gen_ai.tool.call.result`.

Content comes from the `AgentMessage` projections (`text` and the `tool_*`
fields), not from `framework_item`. System instructions and tool definitions
are not captured. Content can be sensitive and large; capture it only where the
tracing backend may store it.

The spans set no `gen_ai.input.messages_ref` or `gen_ai.output.messages_ref`.
Those attributes take an absolute URI, and Dex has no URI for an
`AgentMessages` instance. Use the `dex.agent.*` keys with the Flow ID instead.

### Semantic conventions version

The GenAI semantic conventions are still marked Development, so the session
pins a version and sets its schema URL on the tracer.
`GenAISemanticConventionsVersion.V1_42_0_DEV`, the default, follows
[`open-telemetry/semantic-conventions-genai`](https://github.com/open-telemetry/semantic-conventions-genai)
at commit [`dc3571562603552dc555c667dfa327a638d94ab2`](https://github.com/open-telemetry/semantic-conventions-genai/tree/dc3571562603552dc555c667dfa327a638d94ab2)
(2026-11-01). Its schema URL is
`https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev`; the repository has no
release tag yet.
`V1_36_0` follows OpenTelemetry semantic conventions 1.36.0 for backends that
still read the older names: `chat` spans carry `gen_ai.system` instead of
`gen_ai.provider.name`, omit the cache and reasoning token counts, and never
carry content, because 1.36.0 recorded content as events.

## Testing an adapter

`dex_agents.testing` runs integration tests against a private `dexcli dev
--engine zd` engine. It reads `DEXCLI` and expects `dex-server-zd` next to it
(or `DEX_SERVER_ZD_BINARY`). The Worker runs in a child process in its own
process group, so a test can SIGKILL it at an exact moment.

```python
# tests/my_worker.py: the Worker child.
from dex_agents.testing import run_agent_worker
from tests.my_agent import build_flow

if __name__ == "__main__":
    run_agent_worker([build_flow()])
```

```python
# tests/test_approvals.py: the test process builds the same Flow for its Client.
from pathlib import Path
from dex_agents.testing import EXECUTE_COMPLETED_EVENT, AgentTestStack, wait_for_agent_snapshot
from tests.my_agent import build_flow

PACKAGE_DIR = Path(__file__).resolve().parents[1]

async def test_write_survives_a_worker_kill(tmp_path: Path) -> None:
    flow = build_flow()
    async with AgentTestStack(tmp_path, [flow], "tests.my_worker",
                              worker_cwd=PACKAGE_DIR,
                              worker_env={"MY_STATE_DIR": str(tmp_path)}) as stack:
        sessions = stack.sessions(flow)
        worker = await stack.start_worker()
        await sessions.start_session("session-1", "refund order 7")
        interrupt = await wait_for_agent_snapshot(
            sessions, "session-1",
            lambda snapshot: next(iter(snapshot.pending_interrupts), None),
            description="a pending approval")
        worker.kill()                    # SIGKILL while the session waits
        await stack.start_worker()       # replacement Worker
        assert await sessions.resolve_interrupt(
            "session-1", interrupt.interrupt_id, approved=True)
        history = await stack.read_step_history("session-1")
        executed = [entry.step_type for entry in history
                    if entry.event_type == EXECUTE_COMPLETED_EVENT]
```

Run tests with `pytest-asyncio` in auto mode, as `pyproject.toml` here does.

Inside the Worker child, a tool marks a `FileGate(state_dir, name)` and awaits
`wait_for_release()`; the test awaits `wait_until_reached()` and kills the
Worker at that exact point. Two gates reached at once prove that two tool
Steps run in parallel. `InvocationLog(path)` counts real tool and model calls
across Worker processes. `read_step_history(server_address, flow_id)` lists
completed Steps (`StepHistoryEntry` with `event_type`, `step_type`,
`step_execution_id`, `final_attempt`) through `dexcli flow history`.
`wait_until(description, probe)` polls with a deadline instead of sleeping.
The core's own tests in `tests/` use a scripted runner and show every pattern.

Each package's `ci.sh` installs locked dependencies, runs lint and type
checks, and runs the integration tests with `DEXCLI` set. `Agents CI`
discovers every `agents/*/*/ci.sh`.

## Design decisions

The plan records each of these decisions too.

- Step mode: one `RunModel` Step per model call and one Step per tool call,
  READ included. No framework loop runs several model calls in one Step.
- Tool call Steps and their failure routes take the tool call ID as input.
  There is no shared "current tool call" in `AgentState`.
- `RecordToolFailure` records `failed` for a read and `outcome_unknown` for a
  write: it is reached only through crashes, timeouts, or exceptions other
  than `ToolExecutionError`, so a write may have taken effect.
- `AgentToolCall.deferral_order` keeps model order for display and for the
  turn-failure note; AttributeMap keys alone do not keep order.
- By default nothing from a turn is stored until it completes: `TurnCompleted`
  carries all of the turn's messages and `ToolCallsRequested` keeps them in the
  framework run state. A runner may store them incrementally with
  `ToolCallsRequested.new_messages`; Stepwise does.
- Built-in tools are routed by the session, not the runner, so they work under
  every adapter that offers their definitions.
- A failed turn that ran tools leaves a note message instead of appending
  nothing, so its writes are not repeated by the next turn.
- Every Step sets SYNC durability explicitly.
- Message sequences start at 1. A blank `initial_message` starts no turn.
  `last_turn_error` is cleared when a later turn completes.
- `resolve_interrupt` drops `rejection_message` on approval. When it resolves
  an interrupt just as its deadline Timer fires, `AwaitToolApproval` waits
  again and applies the answer.
- `AgentSnapshot.state` is `None` until `StartAgentSession` commits.
- Status, role, effect, and record status values are `StrEnum` types in Python;
  the wire carries the plain strings.

## Limits

- `RunModel` and `get_agent_snapshot` load the whole `AgentMessages` map.
  History is never compacted, so very long sessions load more on every call.
- Each model call and tool call adds a Step round trip.
- A running turn cannot be steered or cancelled; new user messages queue until
  it ends, including while it waits on a question or a durable wait.
- Interrupt deadlines use the Worker clock.
- One `text_delta` call writes one Stream message; batch very small chunks.
- Tool call IDs that are empty or contain `/` fail the turn.
- A tool Step's first output narrows Dex's redelivery window but does not
  close it. The awaited heartbeat only queues the frame. If the Worker dies
  before the frame leaves it, or after it left but before Dex received it, Dex
  can redeliver the same attempt and a single-attempt tool can run twice.
- A tool the framework cannot defer runs inside `RunModel` and repeats on a
  `RunModel` retry.
- Whether turn records cleared at turn end still count against the Server's
  `limits.maxFlowStateBytes` run budget is an open question in the parent
  design.
- `dex_agents.mcp` polls an MCP task inside its tool Step for at most
  `task_wait_timeout`, holding a Worker slot; Timer-driven polling is a
  follow-up.
- An MCP `input_required` answer records `failed`; mapping it to a session
  question interrupt is a follow-up.
- `dex_agents.mcp` starts a new stdio process, or opens a new HTTP connection,
  for every request. An unreachable server is tried again, with its connection
  attempts, on every `list_tools` call until it answers.
- MCP annotations are hints from the server and are trusted by default. A
  wrong `readOnlyHint` on a write makes that write run without approval and
  retry; set `trusts_tool_annotations=False` or a `ToolPolicy` for servers you
  do not control.
- Tracing covers the session's Steps only. The Dex SDK emits no spans, so Dex
  Server and Worker internals do not appear in the traces.
- The adapters in this repository do not return `ModelCallMetadata` yet, so
  their `chat` spans are named `chat` and carry no model or provider. Wiring
  each adapter to report it is a follow-up.
