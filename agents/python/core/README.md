# dex-agents

`dex-agents` (import `dex_agents`) is the Python core of the Dex agent
framework adapters. It provides one durable agent session Flow,
`AgentSessionFlow`, and the `AgentFrameworkRunner` contract that each
framework adapter implements. The plan is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

One Flow run is one agent session; the Flow ID is the session ID. The
session runs in **Step mode**: every model call is one `RunModel` Step, and
every tool call, READ included, is its own Step. The tool call Steps of one
model response run in parallel, and `ResumeTurn` waits for their results
before the next model call. Each call therefore has its own retry policy,
timeout, failure route, history entry, and Dex Web node, and a crash repeats
at most the one call that was running.

## Installation

Applications usually install an adapter, which brings in this package. To
build a runner of your own, install the core directly. It needs Python 3.11 or
later and requires the released `dex-python-sdk==1.45.1`:

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
| `dex_agents.builtin_loop` | `dex-agents` | The built-in agent loop: `AgentDefinition`, function tools, tool sources, the `ModelClient` protocol, `ScriptedModelClient`, and the runner |
| `dex_agents.litellm` | `dex-agents[litellm]` | `LiteLLMModelClient`, a `ModelClient` on LiteLLM |
| `dex_agents.mcp` | `dex-agents[mcp]` | `McpToolSource`, MCP servers' tools as agent tools |
| `dex_agents.connectors` | `dex-agents[connectors]` | `ConnectorToolSource`, reviewed Connector operations as agent tools |
| `dex_agents.s3` | `dex-agents[s3]` | `S3MediaStore`, image bytes in an S3-compatible bucket; see [Images](#images) |
| `dex_agents.ui_stream` | `dex-agents` | Server-Sent Event frames and snapshot JSON for UIs |
| `dex_agents.testing` | `dex-agents` | Private engines, Worker processes, gates, Step history, and `ScriptedModelClient` (the same class as `dex_agents.builtin_loop`'s) |

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

## Built-in agent loop

The built-in agent loop is for agents defined in Dex terms
without a framework. `BuiltinLoopAgentRunner` implements the runner contract
below, so the session Flow, its Steps, approvals, and events are the ones
every adapter uses. Each `RunModel` makes exactly one model call through a
`ModelClient`, and every tool call is its own Step.

```python
from dex import Registry
from dex_agents import BuiltInTool, ToolEffect
from dex_agents.litellm import LiteLLMModelClient
from dex_agents.builtin_loop import AgentDefinition, ToolCallContext, function_tool
from dex_agents.builtin_loop import builtin_agent_session_flow


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
flow = builtin_agent_session_flow(support_agent, flow_type="SupportAgent")
registry = Registry([flow], allow_async_handlers=True)
```

`AgentDefinition` fields: `name`, `instructions` (a string, or a function of
`InstructionContext` evaluated in every `RunModel`), `model`, `model_settings`,
`tools`, `tool_sources` (see [Tool sources](#tool-sources)), `tool_policies`,
`default_tool_effect`, `built_in_tools`,
`max_model_calls_per_turn` (100; after that many calls in one turn the next
call offers no tools, and a tool call the model makes anyway gets the result
`The tool call was not run: this turn reached its limit of <n> model calls.`
while the turn ends), `interrupt_deadline`, `event_stream_capacity_bytes`,
`plan_mode_enabled` (see [Plans and plan mode](#plans-and-plan-mode)), and
`decide_turn_continuation` (since 0.5.0; see
[Ending or continuing a turn](#ending-or-continuing-a-turn)).
A function tool may not share a name with an enabled built-in tool:
`AgentDefinition` refuses it, and so does `run_model_step`, before any model
request, when only the session options enable that built-in.
`builtin_agent_session_flow` derives the `AgentSessionOptions` from it.

`@function_tool(effect=..., requires_approval=None, name=None,
description=None, parameters_schema=None, prepare_arguments=None)` turns a plain sync or async
function into a tool. The name defaults to the function name, the description
to the docstring, and the JSON Schema is derived from the type hints with the
standard library: `str`, `int`, `float`, `bool`, `None`, `list`, `dict`,
`Literal`, `Enum`, unions, dataclasses, `TypedDict`, and
`Annotated[T, "description"]`. A parameter typed `ToolCallContext` receives the
idempotency key, attempt, Flow ID, turn ID, tool call ID, event writer,
heartbeat, the answers to the tool's questions, and `effect`: the call's effect
after the tool policies, which chose its tool Step. A wrapper that turns errors
into `ToolExecutionError` lets them propagate for `NON_IDEMPOTENT_WRITE` and
`UNCLASSIFIED`, the default of a hand-built context. A sync function runs in a
worker thread. `prepare_arguments`, a synchronous and deterministic function,
converts the decoded arguments before the schema check, such as an argument
shape an earlier version of the tool took; an exception it raises fails the
call like a schema violation, `CancelledError` passes through, and an
awaitable result is refused. The stored tool call and the approval interrupt
keep the model's original arguments. Arguments that break the schema, and an unknown tool name,
raise `ToolExecutionError`, which the model sees: arguments that are not JSON
give `invalid arguments for <tool>: arguments are not valid JSON`. A `str` result is the
output, and a `FunctionToolResult` adds images; see
[Images from function tools](#images-from-function-tools). Any other result is
serialized as JSON.

In `RunModel`, the runner builds the model input from committed state only:
the instructions, every stored item, the plan note from `build_plan_note`
before the turn's items when a plan exists or the turn plans, and the results
of the previous model response from the final records. Every stored tool call is paired with a
result; a missing one becomes the outcome-unknown text, so an orphaned call
never reaches a provider. It stores items as they appear
(`ToolCallsRequested.new_messages`), so `FrameworkRunState` holds only the
turn's model call count, plus the count of consecutive continuations after a
continued answer.

### Ending or continuing a turn

`decide_turn_continuation` lets the application decide, at the end of each
loop iteration, whether the turn ends or continues. An iteration is one model
call and the tool calls it requested. The function receives a
`TurnContinuationContext`: `flow_id`, `turn_id`, `session_settings`, the
iteration's `model_output` items, and its `tool_results` (empty when the model
answered without tools). It returns `"end"`, `"default"`, or a
`ContinueTurn(message)`, directly or as an awaitable.

| The iteration | `"end"` | `ContinueTurn(message)` | `"default"` or no hook |
| --- | --- | --- | --- |
| The model called tools | The turn completes without another model call | The message follows the tool results, and the model is called with both | The model is called with the tool results |
| The model answered without tools | The turn completes | The answer and the message are stored, and the model is called again in the same turn | The turn completes |

```python
def decide(context: TurnContinuationContext) -> TurnContinuation:
    if any(result.tool_name == "submit_decision" for result in context.tool_results):
        return "end"
    answer = "".join(item.text or "" for item in context.model_output)
    if not context.tool_results and "DECISION:" not in answer:
        return ContinueTurn("Finish with a line that starts with DECISION:.")
    return "default"


approvals_agent = AgentDefinition(
    name="approvals",
    instructions="Review the request, then call submit_decision.",
    model=model,
    tools=[submit_decision],
    decide_turn_continuation=decide,
)
```

- After tools, the hook runs in the next `RunModel` Step, before its model
  call. `"end"` completes the turn there with the tool results as its last
  messages and zero model calls in that Step's usage. The snapshot, the
  `turn_completed` event, and the browser stream show a finished turn whose
  last message is the tool result. That `RunModel` still runs the session's
  compaction and spend-limit checks first, and still emits a `chat` span.
- After an answer, the hook runs in the same `RunModel` Step, after the model
  call. A `ContinueTurn` returns the `TurnContinued` outcome: the session
  stores the answer and the message as user text, then moves to `RunModel`
  again. The next model request therefore ends with a user message, which
  every provider accepts. Each model call stays its own `RunModel` Step, so a
  crash or retry repeats only that call. Each continued call counts toward
  `max_model_calls_per_turn`.
- The message is part of the turn's transcript: the model sees it as user
  text, and snapshots hold it as a `user` message of the same turn. A browser
  that renders snapshots can tell it from a user's message: it comes between
  two model calls of one turn.
- At most `MAX_CONSECUTIVE_TURN_CONTINUATIONS` (8) continuations in a row
  may follow answers without tools; a tool call resets the count. The next
  one ends the turn with its answer stored, `last_turn_error` set to
  `TURN_CONTINUATION_LIMIT_ERROR`, and a `turn_failed` event.
- An exception, or any other value, including a `ContinueTurn` with a blank
  message, fails the `RunModel` attempt, and its retry policy runs it again.
  After an answer, the retry repeats the model call too. Keep the function
  free of side effects.

### Tool sources

`AgentDefinition.tool_sources` takes tools that are listed at run time, such
as an MCP server's tools. Any object with a `source_name` and `list_tools`,
`deferred_tool_call`, `execute_deferred_tool_call`, and `poll_tool_task` is a
`ToolSource`; `McpToolSource` is one:

```python
from dex_agents.mcp import McpStdioServerConfig, McpToolSource

files = McpToolSource(
    [McpStdioServerConfig(name="files", command="mcp-server-files", args=["/srv/reports"])],
    resource_and_prompt_tools_enabled=True,
)
agent = AgentDefinition(
    name="reports",
    instructions="You manage reports.",
    model=LiteLLMModelClient("openai/gpt-5-mini"),
    tools=[lookup_order],
    tool_sources=[files],
    built_in_tools=frozenset({BuiltInTool.ASK_USER}),
)
```

In every `RunModel` of a chat or executing turn, the runner awaits each
source's `list_tools`, for at most `tool_source_listing_timeout` (10 seconds),
and offers the tools after the function tools and before the built-in tools.
A source whose listing raises or takes longer offers no tools in that model
call, with a warning log, so a down server never fails a turn. A planning
turn, and a call after the model call limit, list nothing.

A call of a listed tool becomes the source's `deferred_tool_call`, so the
source's policy, effect class, and `tool_source_reference` apply: approvals,
`CallTool` or `CallSingleAttemptTool`, the idempotency key, and tool tasks
work as for any tool. The runner passes the definition it offered as
`listed_tool`, so a listing refreshed during the model call cannot change it.
It stores the source's reference as `{"stepwise_tool_source": <source_name>,
"reference": ...}`, so the tool Step and `PollToolTask` reach the source that
built the call, with its own reference, on any Worker, without a listing, and
whatever the order of `tool_sources`. Source names must be unique and stable
across deploys: `McpToolSource` defaults its `source_name` to `"mcp"`, which
adding or removing a server keeps, and takes `source_name=`. Two
`McpToolSource`s in one agent must therefore name at least one of them. A stored call whose source name this Worker does not have raises
`LookupError`, so its effect class decides the outcome. A name that no listing
offered, such as a tool of a server that was down, fails as `unknown tool
<name>` without an approval (`unclassified`, no approval): it never reaches a
source.

A listed name that repeats a function tool, an enabled built-in tool, or
another source's tool fails the `RunModel` attempt with `ValueError` before any
model request: `tool <name> uses the name of an enabled built-in tool`, `tool
<name> of a tool source uses the name of a function tool`, or `two tool sources
list a tool named <name>`. A source may define `declared_tool_names()`, names
it always lists, and `AgentDefinition` checks those when it is built; two
`McpToolSource`s with the resource and prompt tools are refused there. A
source may also define `cached_tools()`, its last listing without I/O, which
the synchronous `estimate_input_tokens` counts; otherwise it counts the
runner's last listing of that source.

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

`ScriptedModelClient(respond)` from `dex_agents.builtin_loop` answers each call
from a function of the `ModelRequest`, so an agent runs without a model key: a
local demo mode, a tutorial, or a test. `respond` returns a
`ScriptedModelReply` with `text`, `tool_calls` (`ScriptedToolCall`), and
`reasoning`, sync or async. `ScriptedModelClient.user_texts(request)` and
`ScriptedModelClient.tool_results(request)` read the request. `requests` keeps
the latest `max_recorded_requests` requests, 100 by default. Keep the script
deterministic, because a crashed `RunModel` reruns it. `dex_agents.testing`
exports the same class.

```python
from dex_agents.builtin_loop import ModelRequest, ScriptedModelClient, ScriptedModelReply


def reply_as_demo_model(request: ModelRequest) -> ScriptedModelReply:
    results = ScriptedModelClient.tool_results(request)
    if results:
        return ScriptedModelReply(text="; ".join(result.output for result in results))
    return ScriptedModelReply(text=f"Demo reply: {ScriptedModelClient.user_texts(request)[-1]}")


model = ScriptedModelClient(reply_as_demo_model) if demo else LiteLLMModelClient(model_name)
```

## Built-in tools: questions and durable waits

Built-in tools are session features, so they work under every runner. Enable
them in `AgentSessionOptions.built_in_tools`. The runner offers the enabled
definitions from `BUILT_IN_TOOL_DEFINITIONS` (filter by
`turn.enabled_built_in_tools`) to its model, and `RunModel` routes their calls
to their own Steps with no approval and the `read` effect. `write_todos` is
described in [Plans and plan mode](#plans-and-plan-mode). An enabled name is
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

## Plans and plan mode

The `write_todos` built-in replaces the session's plan, the `AgentPlan`
Attribute, as a whole. Its arguments are `{todos: [{content, status}]}`, with
a status of `pending`, `in_progress`, or `completed`. `RunModel` records the
call in its own commit, publishes its result, and still moves to `ResumeTurn`;
no tool Step runs. Each call that succeeds writes revision + 1: no tasks is
`cleared`, a planning turn writes a `draft`, every task completed is
`completed`, and anything else is `active`. The model sees
`{"status":"updated","revision":<n>,"task_count":<k>}` or
`{"status":"cleared","revision":<n>}`, and the session writes `plan_updated`,
which it also writes when an approval marks a draft `active`.
A response with more than one `write_todos` call fails each of them with
`write_todos was called more than once in one response`; invalid arguments fail
with `invalid todos: <reason>`.

Plan mode reviews a plan before it runs. Enable it with
`AgentSessionOptions(plan_mode_enabled=True)`, which needs `write_todos` and a
runner that is a `PlanModeRunner` (`supports_plan_mode` is `True`). The
built-in loop runner is one; `AgentDefinition(plan_mode_enabled=True)` sets the
option. `AgentDefinition.planning_tool_choice` is `"auto"` by default: a
planning turn offers only `write_todos`, the plan note tells the model to write
the plan with it, and a turn that answers in text, such as with a clarifying
question, ends without a draft. `"required"` forces `write_todos` on the first
model call of a planning turn while no draft exists. Claude Opus 5.5, Claude
Sonnet 5.5, and Anthropic extended thinking refuse a forced tool choice, so
`AgentDefinition` raises `ValueError` for `"required"` when the model client's
`supports_forced_tool_choice(settings)` returns `False`, as
`LiteLLMModelClient`'s does for them. For a model LiteLLM routes to Anthropic,
or to Claude on Bedrock or Vertex AI, it counts a `thinking` option that is not
disabled, or a `reasoning_effort` other than `"none"`, as extended thinking.
Gemini and OpenAI models accept a forced tool choice with thinking or reasoning.

```python
sessions = AgentSessionClient(client, flow)
await sessions.send_user_message("session-42", "Plan the migration", plan_mode=True)
snapshot = await sessions.get_agent_snapshot("session-42")
if snapshot.plan is not None and snapshot.plan.status is PlanStatus.DRAFT:
    await sessions.execute_plan("session-42", snapshot.plan.revision)
```

- A message with `plan_mode`, or any message while a `draft` plan exists,
  starts a turn whose `AgentState.interaction_mode` is `planning`. The runner
  reads it from `AgentTurnContext.interaction_mode` and offers only
  `write_todos`.
  `RunModel` rejects any other call of a planning turn with `The tool call was
  not run: a planning turn can only call write_todos.`. Feedback on a draft is
  an ordinary message that revises it.
- `send_user_message` returns `False` for a `plan_mode` message when plan mode
  is off.
- `execute_plan(ExecutePlanRequest(revision))` returns `True` only while the
  session waits for a user message, the plan has that revision and is `draft`
  or `active`, no execution of it is pending, and nothing is queued, steered, or pending.
  It records the approval in `PendingPlanExecution` and publishes it to the
  `PlanExecutions` ChannelMap instance of that revision. It locks
  `PendingPlanExecution`, so a concurrent call gets `RpcLockConflictError`
  (`AgentSessionClient` retries it) and then `False`.
- `AwaitUserMessage` waits on the approval of the current revision only, after
  queued messages, so an approval of a revision that a later turn replaced
  never runs. It marks the plan `active`, writes `plan_execution_started`, and
  starts an `executing` turn with every tool, whose user message is `[Dex] The
  user approved plan revision <n>. Execute it and keep the task statuses
  current with write_todos.`. The stored message has `origin`
  `plan_execution`; see
  [User messages the session writes](#user-messages-the-session-writes).
- `build_plan_note(plan, interaction_mode)` returns the note that shows the
  model the plan. The built-in loop adds it as user text before the turn's items in
  every model call, so the model always sees the latest plan.

## Steering and turn cancellation

A user message is queued or steered. `send_user_message` queues it: it waits
until the running turn ends, and the user can still delete or steer it.
`steer_user_message` steers the running turn with new text or with a queued
message, and `cancel_turn` stops it with no new turn.

```python
sessions = AgentSessionClient(client, flow)
await sessions.send_user_message("session-42", "Also check order 8")
snapshot = await sessions.get_agent_snapshot("session-42")
queued = snapshot.queued_messages[0]
await sessions.steer_user_message("session-42", message_id=queued.message_id)
await sessions.steer_user_message("session-42", text="Use the staging account instead")
await sessions.delete_queued_message("session-42", snapshot.queued_messages[1].message_id)
await sessions.cancel_turn("session-42", snapshot.state.active_turn_id)
```

A steered turn ends at its next safe Step boundary. Steering never interrupts
a model call or a tool call that has started: a running model call finishes,
and a final answer it returns completes the turn as usual; a tool that has
started runs to the end, and so does a tool task it started, questions
included. `cancel_turn` stops the turn now instead: it cancels a running model
call or compaction, whose answer is dropped, and every wait for an approval, an
answer, a durable wait's Timer, or a tool task's next poll. A tool call Step
that has started still runs to the end, and the turn ends after it. A started
tool task is abandoned: its call records `outcome_unknown` with
`cancellation_reason` `turn_cancelled`, and a runner that implements
`ToolTaskCanceller` is asked to cancel the task, best-effort; a result the
task returns later is dropped. A call that has not started records
`rejected` with `cancellation_reason` `steered` or `turn_cancelled`, and the
model later sees
`{"status":"cancelled","reason":"superseded_by_steered_user_message"}` or
`{"status":"cancelled","reason":"turn_cancelled_by_user"}` as its result. That
includes approvals, questions, and durable waits that were still waiting. A
call that is settled first keeps its own result: an `ask_user` answer that
commits first, a durable wait whose Timer fires first, and a built-in call with
invalid arguments, which fails at once. A UI can briefly show an approval or a
question of a turn that is already steered; answering it then returns `False`
or has no effect. `ApplyTurnControl` then ends the turn. When the runner
implements `build_unfinished_turn_messages`, it stores the turn's exact
transcript; otherwise it stores a note that lists the calls that ran, as after
a failed turn. A steered turn is followed by a new turn whose user messages are
every steered message, in order. That turn is a `planning` turn when any of
its messages has `plan_mode` or a `draft` plan exists, as for a queued message;
a queued `plan_mode` message moved by its ID keeps `plan_mode`. A `write_todos`
call settles in `RunModel`'s own commit, like a built-in call with invalid
arguments, so one that a model call made while the user steered still replaces
the plan.

While no turn is active, there is nothing to steer: `steer_user_message` with
`text` queues the message like `send_user_message`, so it starts the next turn
at once.

- `steer_user_message` and `delete_queued_message` take the Channel message ID
  from `AgentSnapshot.queued_messages`. Both return `False` for a message that
  is no longer queued. When the session consumes the message while the RPC
  runs, the RPC raises `ChannelMessageNotFoundError` and changes nothing;
  reload the snapshot and let the user choose again.
- `cancel_turn` returns `True` when the request is recorded. The turn may still
  complete first, when its model call committed the final answer before the
  RPC committed. A second call for the same turn returns `True` and changes
  nothing.
- `steer_user_message` and `delete_queued_message` lock nothing. `cancel_turn`
  locks `TurnCancellationRequest` and `PendingInterrupts`, which only short
  Steps lock, so concurrent cancels apply in order and none of them waits for
  a model call; `AgentSessionClient.cancel_turn` retries the rare
  `RpcLockConflictError`.
- `resolve_interrupt` or `answer_question` racing a steer either commits
  first or returns `False`. An approval or a tool's answer that commits first
  moves its call on, and the tool Step then records it cancelled before the
  tool starts; an `ask_user` answer that commits first is the call's result.
- `AgentSnapshot.steered_messages` lists the steered messages not applied yet.
- A turn that a Worker without steering started or advanced cannot be steered
  or cancelled: `AgentState.accepts_turn_control` is `False`, and both RPCs
  return `False`. It runs to its end as before. Call the RPCs only after every
  Worker of the Flow type runs this release and every turn that was active
  during the rollout has ended, because such a turn can hold a wait that an
  earlier Worker began, which a steer never wakes.

## Message storage and retention

> **Upgrade rule: stop every 0.1.0 Worker of a Flow type before any 0.2.0
> Worker of it starts.** Once a 0.2.0 Worker stores a message in a 0.1.0
> session, it moves the older messages out of `AgentMessages`. A 0.1.0 Worker
> that then runs a Step of the session fails the turn, because it cannot read
> the moved messages.

The newest messages are the window: one `AgentMessages` instance per
message, so storing a message writes only that message. When the window holds
more than `message_window_size` messages (64), its oldest messages move into
immutable `AgentMessageChunks` instances in the same commit. A chunk holds at
most `message_chunk_size` messages (32) and `message_chunk_max_bytes` of
encoded JSON (1 MiB, at most 8 MiB), so it stays well under the Server's
`values.maxValueBytes`. A larger message skips the window and goes straight
into its own chunk in the Step that stores it, so a message too large for the
Server fails that Step's turn at once instead of a later rollover. One commit
moves at most 4 chunks and 8 MiB, so the window can briefly hold more than its
size. The `AgentMessageIndex` Attribute lists each kept chunk's first and last
sequence, where the window starts, and the oldest stored message; it changes
only when chunks are written or deleted. `TurnReplySequence` records where the
latest turn's replies start. Every Step that stores messages loads the whole window.
With compaction, `RunModel` loads only the chunks after the summary, through
per-movement load options. Without compaction, the model input is every
message, so it loads every chunk.

With compaction, retention keeps Flow state bounded however long the session
runs. It deletes the oldest chunk while every message in it is at or before
`ContextSummary.last_sequence` and at least `retained_message_limit` messages
(1,000) are stored after it, so the newest `retained_message_limit` messages
and every message after the summary always stay. A deleted message is gone
from `list_agent_messages` and `get_agent_snapshot`; paging ends at
`AgentMessageIndex.first_sequence`, and every page's
`deleted_before_sequence` says where the deleted messages end. An application
that needs the whole transcript copies new messages out with
`list_agent_messages` before `retained_message_limit` more are stored after
them, such as after each `turn_completed`, or sets
`retained_message_limit=None`. A runner without `ContextCompactionRunner` on
a session with deleted messages logs a warning on each model call, because
its input starts after them with no summary.

```python
from dataclasses import replace

options = replace(
    builtin_agent_session_options(definition),
    message_window_size=64,
    message_chunk_size=32,
    message_chunk_max_bytes=1024 * 1024,
    retained_message_limit=5_000,
)
```

A session that dex-agents 0.1.0 stored has every message in `AgentMessages`
and no index. This release reads it as one large window, and each Step that
stores messages moves at most 4 chunks, so the older messages leave the
window over several commits.

## Compaction

Compaction keeps the model input under the context window. Set
`AgentSessionOptions.compaction` (or `AgentDefinition.compaction` for
the built-in loop) to a `CompactionPolicy`:

```python
from dex_agents import AgentSessionOptions, CompactionPolicy

options = AgentSessionOptions(
    compaction=CompactionPolicy(context_window_tokens=400_000),  # trigger 0.85, keep 0.3
)
```

Before each model call, `RunModel` asks the runner for
`estimate_input_tokens(turn)`. At `trigger_ratio` of the window it asks for
`select_compaction_cutoff(turn, retained_token_budget)`, the last message to
summarize; when that sequence is past the current summary, `RunModel` sets
status `compacting_context` and moves to `CompactContext` without a model
call. `CompactContext` has `RunModel`'s timeouts, retries, and failure route.
It calls `summarize_context(ContextSummaryRequest)` once, stores the
`ContextSummary` Attribute (generation, `first_sequence` 1, `last_sequence`,
text, `created_at`), records the call's usage, writes `compaction_started` and
`compaction_finished`, and moves back to `RunModel`. A crash reruns only the
summary call. From then on the runner receives the messages after the summary
in `turn.messages` and the summary in `turn.context_summary`. A second estimate
that is still high finds no new cutoff, so the pair cannot loop.

Summarized messages stay stored until retention deletes them; see
[Message storage and retention](#message-storage-and-retention). The runner must implement
`ContextCompactionRunner`, and `AgentSessionFlow` raises `TypeError` for a
compaction policy otherwise. `BuiltinLoopAgentRunner` implements it: its input
starts with `CONTEXT_SUMMARY_NOTE_HEADER`
(`[Dex] Summary of the earlier conversation:`) and the summary, it cuts only
between model responses whose tool calls all have stored results, so a turn
with many tool calls compacts mid-turn without separating a call from its
result, and it summarizes with `CONTEXT_SUMMARY_INSTRUCTIONS` and no tools.
`ContextSummaryRequest.input_token_budget` carries the policy's
`context_window_tokens`. When the summary request plus the definition's
`max_output_tokens`, if set, would exceed it, the runner leaves out the oldest
messages, up to a cut that keeps each model response and each tool call with
its result whole, and says so with
`build_context_summary_omission_note(omitted_count)`
(`[Dex] <n> earlier messages did not fit in this request and are left out.`).
Each `CompactContext` attempt emits a `chat` span for the summary call, as
`RunModel` does; return `ContextSummaryResult.model_call_metadata` to name it.

A framework adapter cannot measure or rebuild the framework's own request, so
the core gives adapters one shared rule. `estimate_stored_input_tokens(turn,
fixed_input_tokens=...)` counts each stored `framework_item` with
`estimate_text_tokens`, three UTF-8 bytes per token, which errs high for
English text. `select_turn_boundary_cutoff(turn, retained_token_budget)` cuts
only between turns, never inside the running turn, and returns `None` while
`turn.framework_run_state` is set, because a resumed call continues from that
state. `build_context_summary_prompt(request)` writes the range as one plain
text prompt, starting with `CONTEXT_SUMMARY_TRANSCRIPT_HEADER`, that fits
`input_token_budget`. `CompactionPolicy.reserved_input_tokens` adds what an
adapter's estimate cannot see, such as a framework's system prompt. The
adapters of this repository use them; see each adapter's README.
`dex_agents.testing.ContextWindowSessionScenario` runs the same session past a
small context window for any adapter.

## Session settings

Session settings let one Flow type serve sessions that differ, such as by
model, without a Worker pool per model. They are one JSON object that the
application sets when it starts the session and can replace later:

```python
models = {name: LiteLLMModelClient(name) for name in ("openai/gpt-5-mini", "openai/gpt-5")}
definition = AgentDefinition(
    name="support",
    instructions="You help customers with their orders.",
    model=models["openai/gpt-5-mini"],
    # Choose only from clients the Worker built; the settings come from the application.
    resolve_model=lambda settings: models.get(
        str(settings.get("model")), models["openai/gpt-5-mini"]
    ),
)

await sessions.start_session("session-42", "Hello", session_settings={"model": "openai/gpt-5"})
await sessions.update_session_settings("session-42", {"model": "openai/gpt-5-mini"})
```

- `StartAgentSession` stores `AgentSessionStartInput.session_settings` in the
  `SessionSettings` Attribute. `update_session_settings` replaces it; nothing
  is merged, and `{}` clears it. It returns `False` before the session
  initialized.
- A change takes effect when the next turn starts, the same boundary as
  `plan_mode`. The Step that starts a turn copies the settings into
  `TurnSessionSettings`, tagged with the turn, and the turn's model calls,
  retries, and compaction read only that copy, so a running turn keeps the
  settings it started with, even across a Worker crash. A copy that an earlier
  turn left reads as no settings.
- A runner reads `AgentTurnContext.session_settings`, an empty mapping when
  there are none, such as in a session started on 0.1.0.
  `ContextSummaryRequest.session_settings` carries the same value. The built-in loop's
  `AgentDefinition.resolve_model` maps it to the `ModelClient` of each model
  call, including summary calls and token estimates, and
  `InstructionContext.session_settings` passes it to an instructions function.
- `get_agent_snapshot` returns the next turn's settings as
  `AgentSnapshot.session_settings`, not the running turn's: right after an
  update, the running turn may still use the earlier settings.
- Only model calls see the settings. Tool calls do not receive them, and the
  compaction policy's `context_window_tokens` is one value for every resolved
  model.
- **Never put a secret, or anything a browser must not see, in settings.**
  They are Flow state that `get_agent_snapshot` returns, and
  `dex_agents.ui_stream`'s browser snapshot JSON includes them as stored. Put
  a reference, such as a model or credential name, and let the Worker resolve
  it. An exception that `resolve_model` raises becomes the turn's
  `last_turn_error`, which snapshots also show, so keep the settings out of
  its message.
- The compact JSON text, UTF-8 with non-ASCII unescaped, is at most
  `MAX_SESSION_SETTINGS_BYTES` (16 KiB). `validate_session_settings` and
  `session_settings_size` check it, and `AgentSessionClient` checks before it
  sends. The cores write some floats and U+2028/U+2029 differently, so the
  session's own checks accept `SESSION_SETTINGS_SIZE_TOLERANCE_BYTES` (1 KiB)
  more: `update_session_settings` returns `False` beyond that, or for `None`,
  and `StartAgentSession` fails the session at once, with no retry, with a
  reason that starts with `INVALID_START_SESSION_SETTINGS_REASON`.
- Start sessions with settings, and call `update_session_settings`, only after
  every Worker of the Flow type runs 0.2.0. A 0.1.0 Worker drops the start
  settings and starts turns without them.
- `AgentSessionStartInput.start_environment` (`start_session(...,
  start_environment=...)`) is different: string keys and values that say
  where the starting program ran. It stays in the Flow's start input and the
  session never reads it, so it is not a setting and cannot change.

## Images

A user message and a tool result can carry images. Session state holds only
an `AgentImageReference` for each image; the bytes live in a media store,
keyed by their SHA-256:

```json
{"type": "image", "uri": "s3://agent-media/sessions/sha256/9f2c...", "mime_type": "image/png", "sha256": "9f2c...", "size_bytes": 48213}
```

Upload first, then send the reference:

```python
from dex_agents.s3 import S3MediaStore

store = S3MediaStore("agent-media", "sessions")  # credentials from the standard AWS chain
reference = await store.put_image(Path("chart.png").read_bytes())
await sessions.send_user_message("session-42", "What does this chart show?", images=[reference])
```

A tool source returns images the same way, uploading inside its call. This
is a tool source's `execute_deferred_tool_call` result:

```python
reference = await store.put_image(screenshot_png)
return ToolExecutionResult(output="Took a screenshot.", images=[reference])
```

A function tool returns `FunctionToolResult(output=..., images=[...])`
instead, because the runner serializes its other results as JSON; see
[Images from function tools](#images-from-function-tools).

- **One object per image.** The object key is `<prefix>/sha256/<sha256>`, and
  the reference's `uri` ends in `/sha256/<sha256>`. `S3MediaStore.put_image`
  uploads with `If-None-Match: *` and treats "already exists" (412) as
  success, so a tool Step that a crash retries, a client that sends again, or
  two Workers at once write one object. A 409 conflict retries the upload
  twice; when every attempt conflicts, a `HEAD` must find the object with the
  image's size, or `put_image` raises `MediaStoreError`. On a service that refuses conditional writes it
  checks with `HEAD` first; two concurrent uploads may then both write the
  same content, so deduplication holds for the key and its final content. A
  replay always reads the bytes the reference was made from: `get_image`
  reads only references under its own bucket and prefix, refuses an object
  whose `Content-Length` differs, reads at most one byte past `size_bytes`,
  and `verify_image_bytes` checks the SHA-256, the size, and that the bytes
  are a well-formed image of `mime_type`.
- **Types and sizes.** PNG, JPEG, GIF, and WebP, detected from the bytes with
  a cheap structural check (PNG signature and `IHDR`; JPEG SOI, a marker, and
  EOI at the end; GIF header and screen descriptor; WebP RIFF size and a
  `VP8`, `VP8L`, or `VP8X` chunk), not a full decode, up
  to `MAX_IMAGE_BYTES` (3,750,000 bytes) each. Its base64 text is 5,000,000
  bytes, which fits the per-image limits of Amazon Bedrock (5 MB of base64)
  and the Claude API (10 MB of base64). One message or
  tool result carries at most `MAX_IMAGES_PER_MESSAGE` (20). `put_image`
  raises `ImageValidationError` for other bytes. A `uri` is at most
  `MAX_IMAGE_URI_LENGTH` (1024) bytes as UTF-8 in every core, and a store
  refuses a bucket, prefix, or name that would make longer ones.
  `validate_image_references` checks references, and
  `AgentSessionClient.send_user_message` checks them before it sends.
- **Where they are stored.** `AgentUserMessage.images`, the session copy on
  the stored `AgentMessage.images` for display, `ToolExecutionResult.images`,
  and `AgentToolCall.images`. A runner also keeps the references, never the
  bytes, in its `framework_item`, the replay truth. The browser snapshot JSON
  includes the `images` of messages and tool calls.
- **The runner.** `send_user_message` returns `False` for images unless the
  runner implements `ImageUserMessageBuilder.build_user_message_with_images`,
  which the session calls instead of `build_user_message` for a message with
  images. When it builds model input, the runner reads the bytes with
  `load_images(store, references)`, choosing the references from its replayed
  items or from `collect_turn_images(turn)`. The session sets the stored
  user message's `images` itself; for a tool result message the runner sets
  `FrameworkMessage.images` from the record. A tool result with an invalid
  reference fails the tool Step attempt. The session checks the `images` of
  every `FrameworkMessage` a runner returns before it stores any: an invalid
  one fails the model call like any runner error, and one from
  `build_unfinished_turn_messages` leaves only the turn's note.
- **Stores.** `S3MediaStore(bucket, prefix, endpoint_url=..., region_name=...,
  force_path_style=...)` or `S3MediaStore.from_store_uri("s3://bucket/prefix")`
  for AWS S3 or any S3-compatible service; `InMemoryMediaStore` for tests in one
  process. Credentials come from the standard AWS chain by default; pass
  `access_key_id` and `secret_access_key` (and `session_token`) only for a
  bucket whose keys are not secret, such as a local development bucket with
  fixed loopback keys. The store never logs them or shows them in its `repr`.
  A store of your own implements `MediaStore`: check its location with
  `require_short_image_uris`, build each reference with
  `build_image_reference(data, store_uri)`, and check reads with
  `require_store_image_reference` and `verify_image_bytes`.
- **Local directory.** `LocalDirectoryMediaStore(root)` keeps images on one
  machine, for a client and Workers that share a file system, such as a
  command-line tool in local mode; use `S3MediaStore` across machines. `root`
  must be an absolute path. An image's file is `<root>/sha256/<sha256>`, and
  its `uri` is `file://<root>/sha256/<sha256>`: the root is made absolute as
  text, without resolving symbolic links or normalizing Unicode, and every
  byte other than ASCII letters, digits, `-._~`, and `/` is percent-encoded,
  the same in every core. `put_image` copies the bytes into a temporary file
  in the same directory and renames it into place; an existing file with the
  same bytes is success. Directories it creates get mode `0700` and files
  `0600`. A write syncs the image file before the rename. Before every
  `put_image` returns, even when an identical file already existed and nothing
  was written, it syncs the `sha256` directory, the root, and the root's
  parent; it also syncs the parent of each directory it creates. Directories
  above the root's parent must already be durable; a concurrent creator of a
  deeper missing ancestor is out of scope. It opens every path relative to an
  open directory, so it follows no symbolic link under the root. It refuses a symbolic link, a
  special file, or an image file with more than one hard link there with
  `MediaStoreError`. Reads check the file's size first, read at most one byte past `size_bytes`, and run
  `verify_image_bytes`. It needs a POSIX file system. Deleting a session
  deletes no file.
- **Built-in runners.** The built-in loop and the framework adapters show
  images to their model when built with a `media_store`: they implement
  `ImageUserMessageBuilder`, keep references in their stored items, and load
  the bytes for each model call. Without a store they do not, and their
  sessions refuse images. For the built-in loop, pass it to
  `builtin_agent_session_flow(definition, flow_type=..., media_store=store)`
  or `BuiltinLoopAgentRunner(definition, media_store=store)`; see
  [Images on the built-in loop](#images-on-the-built-in-loop).
- **MCP tools.** `McpToolSource(..., media_store=store)` uploads the `image`
  content of a successful result inside the tool Step and returns the
  references in `ToolExecutionResult.images`; see
  [MCP tools](#mcp-tools-dex_agentsmcp). Give it the store the runner reads.
- **Retention.** Deleting a session deletes no object. A runner reads the
  images again whenever a model call replays them, so a bucket lifecycle rule
  must keep each object at least as long as any session that references it
  can still run a turn or replay its history. When an object is gone, the
  turn that needs it fails with `MediaStoreError` in `last_turn_error`, after
  its user message, with nothing else stored; later turns that replay the
  image fail the same way.
- Send images only after every Worker of the Flow type runs 0.5.0: an earlier
  Worker drops them.

### Images on the built-in loop

```python
store = S3MediaStore("agent-media", "sessions")
flow = builtin_agent_session_flow(support_agent, flow_type="SupportAgent", media_store=store)
```

- **Stored items.** The `user_text` item of a message with images, and the
  `tool_result` item of a call whose record has images, add an `images` list
  of references after their other fields:
  `{"type":"user_text","text":"...","images":[{"type":"image","uri":"...","mime_type":"image/png","sha256":"...","size_bytes":48213}]}`.
  An item without images has no `images` key, so its bytes are unchanged.
  The tool result message also shows the references in `images`. Session
  state never holds image bytes.
- **Model calls.** `UserTextItem.images` and `ToolResultItem.images` carry the
  references into the `ModelRequest`. Right before each model call, the
  runner reads the bytes of every image in the request with `load_images`
  and passes them in `ModelRequest.image_bytes`, keyed by SHA-256. A retried
  `RunModel` attempt, or one on a new Worker after a crash, builds the same
  request from the same references and bytes. A store read that fails fails
  that attempt, which retries; after the last attempt the turn records the
  error in `last_turn_error`.
- **Summaries and estimates.** The model call that writes a context summary
  sees the text of the summarized items, without their images.
  `estimate_input_tokens` gets requests without image bytes; the LiteLLM
  client counts `ESTIMATED_IMAGE_INPUT_TOKENS` (1600) for each image it
  sends, so not for a Gemini model's tool result images.
- **Without a store.** A tool source result's references are still stored
  for display, but the model sees only the text. A function tool's images
  fail the call instead, without a retry.
- **LiteLLM.** `LiteLLMModelClient` sends each image after the item's text as
  a base64 `data:` URL. On the Responses API, a user message's `content` and
  a `function_call_output`'s `output` are an `input_text` part and one
  `input_image` part per image, with `detail` `"auto"`. On Chat Completions, a
  user message's `content` is a `text` part and one `image_url` part per
  image. For Anthropic and Bedrock, whose LiteLLM conversion keeps images in
  a tool result, a tool message's `content` is the same list of parts. Every
  other provider, such as OpenAI Chat Completions or Claude on Vertex AI,
  gets only text in a tool message: the tool message keeps the text, and
  after the last tool message of that run follows one user message per tool
  result with images, holding `TOOL_RESULT_IMAGES_TEXT` (`The <tool_name> tool
  call <tool_call_id> returned these images.`) and then the image parts.
- **Known limitation: Gemini through LiteLLM sees no tool result images.**
  Gemini models on the Gemini API and Vertex AI (`gemini/`, `vertex_ai/`, and
  `vertex_ai_beta/`) get images in user messages, but not the images of a
  tool result. Their tool message, or `function_call_output` on the
  Responses API, holds the text and then `TOOL_RESULT_IMAGES_OMITTED_TEXT`,
  such as `[The tool returned 2 images; this model does not receive images
  from tool results.]`, and no user message follows with the images. Gemini 2.5 refuses images in a function
  response, Gemini 3 accepts them only there, and LiteLLM's conversion gets
  neither right. To show a Gemini model a tool's image, send it in a user
  message.
- **Your own model client.** Convert each reference in `images` to the
  provider's image part with its bytes from `ModelRequest.image_bytes`, and
  raise when they are missing. A client that ignores `images` shows the
  model the text only.
- **Which tools return images.** A tool source's
  `ToolExecutionResult.images`, such as the image content of an
  `McpToolSource(..., media_store=store)` result. Give the source the store
  the runner reads. A function tool returns a `FunctionToolResult`; see
  [Images from function tools](#images-from-function-tools).

### Images from function tools

A function tool gets the runner's media store as `ToolCallContext.media_store`,
or `None` when the runner has none. It uploads the bytes inside its call and
returns the references with its text in a `FunctionToolResult`:

```python
from dex_agents.builtin_loop import FunctionToolResult, ToolCallContext, function_tool


@function_tool(effect=ToolEffect.READ)
async def take_screenshot(context: ToolCallContext) -> str | FunctionToolResult:
    """Take a screenshot of the screen."""
    if context.media_store is None:
        return "Screenshots need a media store."
    reference = await context.media_store.put_image(await screen.capture_png())
    return FunctionToolResult(output="Took a screenshot.", images=[reference])
```

- The runner returns it as `ToolExecutionResult(output, images)`, so the
  images are stored, checked, and shown to the model exactly like a tool
  source's. A plain `dict` with `output` and `images` keys is still
  serialized as JSON.
- More than `MAX_IMAGES_PER_MESSAGE` images, or a reference that
  `validate_image_reference` refuses, fails the attempt with
  `ImageValidationError`. A failed attempt of a `READ` or
  `IDEMPOTENT_WRITE` tool retries; a single-attempt call records
  `outcome_unknown`.
- Images from a runner without a media store raise `ToolExecutionError`,
  because no retry could show them. The session records `FAILED` once,
  without a retry, and the model sees the error text.
- A retried attempt uploads the same bytes to the same object, so uploading
  again is safe. A replay reads the stored references, so the tool does not
  run again.

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

class ImageUserMessageBuilder(Protocol):  # optional; see Images
    def build_user_message_with_images(
        self, text: str, images: Sequence[AgentImageReference]
    ) -> FrameworkMessage: ...
```

`DeferredToolOutcome` is `ToolExecutionResult | ToolInputRequested`.
`AgentFrameworkRunner` is generic in what `execute_deferred_tool_call` returns,
and a bare `AgentFrameworkRunner` means `AgentFrameworkRunner[ToolExecutionResult]`,
as in 0.1.0. A runner whose tools ask the user implements
`AgentFrameworkRunner[DeferredToolOutcome]`.

`AgentTurnOutcome` is `TurnCompleted | ToolCallsRequested | TurnContinued`.
`TurnContinued` and `TurnCompleted.turn_error` are new in 0.5.0.

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
| `framework_run_state` | `None` for the turn's first model call; otherwise the value from this turn's last `ToolCallsRequested` or `TurnContinued`. `is_resuming` tests it. |
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
5. To make another model call within the same turn, return
   `TurnContinued(framework_run_state, new_messages, usage)`, with
   `new_messages` ending with a user message when the provider needs the
   request to end with one. The session stores the messages and runs
   `RunModel` again; such a runner implements
   `build_unfinished_turn_messages`. To end the turn as failed without a
   retry, such as at a limit the runner enforces, return `TurnCompleted` with
   `turn_error`; the session stores `new_messages` and sets `last_turn_error`
   to that text.
6. Report `usage` for this model call only. The session adds it to the session
   total. Optionally return `model_call_metadata=ModelCallMetadata(
   request_model, response_model, provider_name, response_id, finish_reasons)`
   in either outcome. The session never stores it; it names the `chat` span
   (see [Tracing](#tracing)). Leave a field `None` when the framework does not
   expose it.
7. Never run a tool inside `run_model_step`, unless the framework cannot defer
   it. Document such tools as at-least-once: a `RunModel` retry runs them
   again.

Any exception fails the attempt. `RunModel` retries up to three attempts
within 30 minutes, rerunning only this one model call, then routes to
`RecordTurnFailure`. Each attempt has a 15 minute timeout. `run_model_limit` in
`AgentSessionOptions` changes it, with the attempts and the retry budget.

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
`save_heartbeat_checkpoint`, `input_answers`, and `context`, the tool Step's
live `AsyncContext` for the duration of the call. Run the real tool once and
return `ToolExecutionResult(output)`, `ToolInputRequested` when the tool
needs an answer before it takes any effect, or `ToolTaskStarted` when the tool
started remote work that the session polls (see [Tool tasks](#tool-tasks)).
Tool calls of one model response run in parallel Steps, so this method may run
concurrently with itself.

`save_heartbeat_checkpoint(text)` records a heartbeat that carries `text`, and
every later heartbeat of the attempt, the session's background heartbeats
included, carries it too. A retried attempt reads it as `heartbeat_checkpoint`
and can resume work the earlier attempt started, such as polling a job,
instead of starting it again. A `DeferredToolCall` may also carry
`tool_source_reference`, opaque text stored on the call's `AgentToolCall`
record (wire field `tool_source_reference`), so the tool's source can resolve
the tool in the tool Step without listing it again.

Raise `ToolExecutionError(message)` for a failure the model should see; the
session records `failed` with that message and does not retry. Raise
`ToolOutcomeUnknownError(message)` when the tool sent a write and cannot tell
whether it took effect; the session records `outcome_unknown` and does not
retry. A runner that implements `ToolCallStepOptionsProvider` gives a call's
tool Step its own timeout and, for `CallTool`, retry policy. Any other
exception is a Step failure. `read` and `idempotent_write` calls retry up to
three attempts, and then `RecordToolFailure` records `failed` for a read and
`outcome_unknown` for a write, because the write may have taken effect.
Single-attempt calls do not retry; `RecordToolOutcomeUnknown` records
`outcome_unknown`. Both read the recovery error's `attempt_progress`. When it
is `NOT_STARTED`, no attempt reached a Worker and the tool never ran, so both
record `failed` with an `error` that starts with
`TOOL_CALL_NOT_RUN_ERROR_PREFIX` (`the tool did not run, so it is safe to call
again`), then `": "` and the recovery detail. The model may call the tool
again. A call whose tool already asked a question ran before, so it keeps the
status of its effect.

Before calling the runner, the tool Step writes `tool_call_started` and awaits
a heartbeat. A Worker that disconnects after it received the task fails the
attempt, and the call records `outcome_unknown`. The heartbeat matters because
Dex treats a stream that half-closes before any output as one whose task never
ran: the attempt returns to pending with the same attempt number, uses no
retry, and is handed out again. After output, a half-close is a disconnect
that fails the attempt with `NOT_COMPLETED`. So a tool that started is never
returned as not run at the same attempt number, and never reported later as
`NOT_STARTED`.

### Tool tasks

Some tools start remote work and get a handle back, such as an MCP task. A
tool Step must not hold a connection while that work runs, so
`execute_deferred_tool_call` returns
`ToolTaskStarted(task_id, poll_interval_seconds)`, and the runner implements
`ToolTaskPoller.poll_tool_task(poll)`:

```python
class ReportRunner:  # also implements AgentFrameworkRunner[DeferredToolOutcome]
    async def execute_deferred_tool_call(self, call):
        saved = call.heartbeat_checkpoint
        if saved is not None:  # an earlier attempt started the job
            return ToolTaskStarted(task_id=saved, poll_interval_seconds=5)
        job_id = await reports.start(call.idempotency_key)
        await call.save_heartbeat_checkpoint(job_id)
        return ToolTaskStarted(task_id=job_id, poll_interval_seconds=5)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        job = await reports.get(poll.tool_call.task_id)
        answered = poll.tool_call.input_continuation  # ID of the question answered last
        resolved = poll.pending_answer  # its resolution, on the poll right after it
        if resolved is not None and job.question_id == answered:
            # Keyed by the question ID, so a retried poll never answers a later question.
            if resolved.resolution is QuestionResolution.ANSWERED:
                job = await reports.answer(job.id, answered, resolved.answer)
            else:  # DECLINED or DEADLINE_PASSED
                job = await reports.skip(job.id, answered)
        if job.question_id is not None and job.question_id != answered:
            return ToolInputRequested(question=job.question, choices=job.choices,
                                      continuation=job.question_id)
        if not job.done:
            # Keep the answered ID only while the job still shows that question.
            kept = answered if job.question_id == answered else None
            return ToolTaskPending(poll_interval_seconds=10, continuation=kept)
        return ToolExecutionResult(output=job.summary)
```

The tool Step records `waiting_for_task` with `task_id` and
`task_poll_interval_seconds`, writes `tool_task_started`, and ends.
`PollToolTask` waits on a Timer for the interval, rounded up to whole seconds
from 1 to a day, and calls `poll_tool_task` once per execution:

- `ToolExecutionResult` records `succeeded`, and `ToolExecutionError` records
  `failed`.
- `ToolTaskPending(poll_interval_seconds, continuation=None)` stores the next
  interval, stores `continuation` as `input_continuation`, and polls again;
  `None` clears it.
- `ToolInputRequested` asks the user through `AwaitQuestionAnswer`, with the
  interrupt ID `<tool_call_id>-input-<n>`. The task has started, so the
  question never ends the call: an answer, a decline, and a passed deadline
  each go back to the task as a `QuestionAnswer` whose `resolution` is
  `ANSWERED`, `DECLINED`, or `DEADLINE_PASSED`. `PollToolTask` polls at once
  and hands it over as `poll.pending_answer`, only on that first poll; a
  retried attempt of it gets it again. That poll reads the question's
  continuation in `tool_call.input_continuation`, and later polls read what
  the last `ToolTaskPending` returned. Keep the answered question's
  continuation only while the task may still show that question, so that a
  later identical question reads as new. Deliver idempotently, keyed by a
  question ID when the task has one. When a retried attempt cannot tell
  whether an earlier one delivered, return the question again instead: the
  user answers twice rather than a stale answer reaching a later question.
  When the task cannot take a decline, cancel it best-effort and raise an
  exception other than `ToolExecutionError`; the call is then recorded by its
  effect.
- Any other exception fails the poll attempt. A poll gets three attempts; then
  `RecordToolFailure` records `failed` for a `read` call and
  `outcome_unknown` for a write, and `RecordToolOutcomeUnknown` records
  `outcome_unknown` for a single-attempt call, because the task may still run.

No Step holds a connection while the work runs, and a Worker restart only
delays the next poll. A crash during a poll repeats only that poll, so a poll
must only read the task, or deliver an answer idempotently. A `CallTool`
attempt that dies after the remote work started runs again: save the handle
with `save_heartbeat_checkpoint` before you return it, and return the saved
handle when `heartbeat_checkpoint` holds one. Dex may drop a heartbeat
checkpoint, so an attempt that dies before its checkpoint is saved and before
the tool Step commits can still start the work twice; pass the idempotency key
on so the remote system can drop the duplicate. `PollToolTask` owns the record
until it is final and takes no lock. Steering waits for a started task, its
questions included, and a call with `task_id` is never recorded `rejected`.
`cancel_turn` abandons the task instead, so Stop never waits for it: it
cancels `PollToolTask`, drops whatever a running poll returns, and records the
call `outcome_unknown` with `cancellation_reason` `turn_cancelled` in
`CancelToolTask`. Implement `ToolTaskCanceller.cancel_tool_task` to stop the
remote work best-effort; make it idempotent by `task_id`, because a crash or a
failed attempt calls it again. When it keeps failing, the call is still
recorded and the turn ends:

```python
    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        await reports.cancel(cancellation.tool_call.task_id)
```

`DeferredToolExecution.supports_tool_tasks` tells a tool whether the runner polls tasks. A runner without `poll_tool_task`
that returns `ToolTaskStarted` anyway gets `failed` for a `read` call and
`outcome_unknown` otherwise, with `MISSING_TOOL_TASK_POLLER`.

A long task polls many times, and each poll is a Step in the session's
history, so poll at the slowest interval the task allows.

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
optional `interrupt_deadline`, `built_in_tools`, `max_durable_wait`, and
`plan_mode_enabled`. A tool
whose policy requires approval runs only after `AwaitToolApproval` approves it.

## Finding sessions that need a person

`AgentSessionClient.start_session` makes a session searchable by the Steps it
waits in, so one query finds every session waiting for a person, across agents:

```python
await sessions.start_session("session-42", "Refund order 7")
waiting = await client.search_flows('ActiveStepTypes = "AwaitToolApproval"', 50)
```

`AwaitQuestionAnswer` finds questions the same way. A session leaves the search
when it moves on. Set `active_step_search_mode` in the start options to choose
another mode, such as `ActiveStepSearchMode.DISABLED`. `AsyncClient.start_flow`
does not turn this on; pass `AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE` in its
`config_override`, or call `update_flow_config` for a running session. See
[Find sessions that need a person](https://docs.superdurable.io/reference/agents/approvals#find-sessions).

## Showing what an approval acts on

A tool approval can say what the call acts on and what it does. Its tool
source returns an `AgentInterruptDisplay(target, summary, preview)` in
`DeferredToolCall.approval_display`, and the session stores it in
`AgentInterrupt.display`, on the call's record as `approval_display`, and in
the `interrupt_raised` event. A queued ordered write raises its approval later
with the display its source built when the model asked.

```python
snapshot = await sessions.get_agent_snapshot(session_id)
for interrupt in snapshot.pending_interrupts:
    print(interrupt.display.summary if interrupt.display else interrupt.arguments)
```

- `ConnectorToolSource` and the CLI's connector tools build it with
  `build_connector_tool_approval_display`: the target is
  `<connector>/<connection>`, and the summary names the operation, the
  connection, and up to five short arguments, such as
  `Calls slack postChannelMessage with connection support-slack; channel: "#eng".`
- The CLI's local file writes name the workspace path and carry a unified diff
  against the file as it is when the model asks; its header lines say so. Its
  shell commands name the working directory and the command with its inline
  credentials redacted and its control characters escaped, cut at 2 KiB; the
  full command is in the call's arguments.
- A display never says how often the call runs or what it achieves, and never
  uses a tool's description as its effect.
- Redaction in a display is best-effort hygiene, not a security boundary.
  The display's text comes from the call's arguments, which the session
  stores in full as before, and an agent can read a workspace file with its
  own tools. A credential-like argument (`is_credential_like_name`) shows
  `<redacted>`, and `redact_inline_credentials` replaces common credential
  shapes: the value after a credential-like name, read as a shell word, a
  triple-quoted string, or a YAML block; `Authorization` and `Cookie` header
  values; URL passwords; `sshpass -p`; and common token shapes. It reads
  `\uXXXX`, `\xXX`, and `%XX` escapes to find credentials but changes no
  other text: escapes stay as written, and only the characters of each
  credential become `<redacted>`. It is conservative, so a generic
  `key=value` is redacted too, and the CLI leaves out a file preview whose
  text it changes. It finds common forms only, and Unicode lookalike letters
  are out of scope; `agents/conformance/approval_display.json` lists the
  cases both cores share.
- The session cuts a target over `MAX_APPROVAL_TARGET_BYTES` (1 KiB) and a
  summary over `MAX_APPROVAL_SUMMARY_BYTES` (2 KiB), ending them with `…`,
  and drops a preview over `MAX_APPROVAL_PREVIEW_BYTES` (16 KiB). A tool
  without a display, and every approval a Worker before 0.6.0 raised, has
  `display` `None`: show the concrete arguments, not a guessed effect.

## User messages the session writes

Since 0.6.0, a stored `USER` message that the session wrote itself, not a
person, carries `AgentMessage.origin`, an `AgentMessageOrigin` value. Show
such a message as a session note, or hide it, never as text the user typed.

| `origin` | Message |
| --- | --- |
| `plan_execution` | The message with which `execute_plan` starts the turn, `build_plan_execution_message(revision)` |
| `turn_failure_note` | The note `RecordTurnFailure` stores for the model after a failed turn whose tools ran |
| `turn_steered_note` | The note that lists the tool calls that ran before `steer_user_message` ended the turn |
| `turn_cancelled_note` | The note that lists the tool calls that ran before `cancel_turn` ended the turn |

```python
page = await sessions.list_agent_messages(session_id)
for message in page.messages:
    if message.origin == AgentMessageOrigin.PLAN_EXECUTION:
        print("Running the approved plan")
    elif message.origin is None and message.role is MessageRole.USER:
        print(f"> {message.text}")
```

- Only the session sets `origin`. `send_user_message`, the start input's
  initial message, and a runner's messages, such as a
  `decide_turn_continuation` message, have none.
- The wire field is `origin`, `null` when absent. Messages stored before
  0.6.0 have none. A Worker before 0.6.0 ignores the field, and drops it
  when it moves the message into an archive chunk.
- A later release may add values. `origin` is a plain string, so a value
  this release does not know still decodes; treat any `origin` as written by
  the session.

## Listing sessions

Since 0.6.0, a session can keep a summary that a session list needs: its
title, the first 80 Unicode code points of the first user message; the time its
latest turn started or ended; and its turn count. Turn it on with
`AgentSessionOptions(summary_attributes_indexed=True)`; it is off by default.
`list_agent_sessions` reads one page of sessions in one
`AsyncClient.search_flows` call, without a snapshot per session and without a
running Worker:

```python
page = await list_agent_sessions(client, flow.get_flow_type(), title_search="refund")
for session in sort_agent_sessions_by_last_activity(page.sessions):
    print(session.session_id, session.title or "—", session.last_activity_at, session.turn_count)
```

- Dex returns running sessions first, then the most recently closed and
  started ones. It cannot sort by last activity, so
  `sort_agent_sessions_by_last_activity` sorts the sessions you loaded.
- `title_search` matches whole words of the title, without case: `flaky`
  finds "Fix the flaky test", `flak` does not. Pass the same `title_search`
  with each `next_page_token`.
- A session without the option, or one that an earlier release started,
  lists without the summary, and a title search never finds it. An earlier
  session gains the last activity time and the turn count at its next turn on
  a Worker with the option. Show each missing value as a dash. Until a Worker
  with the option has started on the server, a title search raises
  `SessionTitleSearchUnavailableError`.
- A session that fails because its state would exceed the server's limit can
  keep the values of its last turn start.
- Privacy: the title is the user's own text, and anyone who can search the
  namespace's Flows reads it. Turn the option on only when the first message
  may be stored in the search index.
- The three Attributes are indexed under `text1` (FULL_TEXT), `datetime1`
  (DATETIME), and `int1` (INT), the generic keys that every Flow type of a
  namespace shares. A Worker with the option registers them when it starts.
  With PostgreSQL search, a namespace has three keys of each of these types;
  the Worker fails to start with `FAILED_PRECONDITION` when the namespace
  registered one of these keys with another type, or when all three keys of
  the type are taken by other names. Search queries of other Flow types must
  filter on `FlowType`.

## Showing sessions in Dex Web

Dex Web draws a session and offers its Actions from the session Flow
Definition Graph. Let the Worker upload it: pass
`session_flow_definition_document(flow_type, action_permission)` in
`WorkerOptions(flow_definitions=...)`.

```python
from dex import AsyncWorker, Registry, WorkerOptions
from dex_agents import session_flow_definition_document

worker = AsyncWorker(
    Registry([flow], allow_async_handlers=True),
    blob_cache,
    WorkerOptions(
        flow_definitions=[
            session_flow_definition_document("SupportAgent", "support-agent.operate")
        ]
    ),
)
```

The upload needs server-zd 0.1.26 or later. The document's `generated_at` is
`SESSION_FLOW_GRAPH_GENERATED_AT` in `dex_agents.session_graph`, the time the
graph template last changed, so a restart writes nothing and an upgrade
replaces the graph. For an older server, render a file with
`render_session_flow_graph` or the `dex-agents-session-graph` command and pass
its directory to Dex Web with `--flow-rendering-dir`. See
[Show sessions in Dex Web](https://docs.superdurable.io/reference/agents/dex-web).

## Spend limits

`ModelSpendLimit` is an application's spend limit, usually per account or API key
across many sessions. The session asks it before each model call and reports what
the call used. Counters and limits belong to the application. Without one, the
session makes no checks. See the interface's documentation and
[Spend limits](https://docs.superdurable.io/reference/agents/spend-limits).

## Flow shape

```text
StartAgentSession -> AwaitUserMessage -> RunModel
AwaitUserMessage -> steer -> ApplyTurnControl
RunModel -> steered or cancelled -> ApplyTurnControl
cancel_turn -> cancels RunModel, CompactContext, and the waiting Steps -> FinishTurnCancellation(turn_id)
FinishTurnCancellation -> ApplyTurnControl, or CancelToolTask(id) per abandoned task
RunModel -> TurnCompleted -> AwaitUserMessage
RunModel -> CompactContext(through_sequence) -> RunModel             with a compaction policy
RunModel -> ToolCallsRequested -> go_to_many(
              AwaitToolApproval(id) -> CallTool(id) or CallSingleAttemptTool(id)   per call needing approval
              CallTool(id) or CallSingleAttemptTool(id)                            per other call
              AwaitQuestionAnswer(id)                                              per ask_user call
              AwaitTimer(id)                                                       per durable_wait call
              ResumeTurn(expected_result_count))
CallTool(id) -> ToolInputRequested -> AwaitQuestionAnswer(id) -> CallTool(id)
CallTool(id) -> ToolTaskStarted -> PollToolTask(id) -> PollToolTask(id) ...
PollToolTask(id) -> ToolInputRequested -> AwaitQuestionAnswer(id) -> PollToolTask(id)
ResumeTurn -> RunModel, or ApplyTurnControl when steered or cancelled
ApplyTurnControl -> RunModel with steered messages, else AwaitUserMessage
```

| Step (input) | What it does | Next |
| --- | --- | --- |
| `StartAgentSession` (`AgentSessionStartInput`) | Start Step. Initializes state, usage, interrupts, and `SessionSettings`. | `RunModel` with a non-blank `initial_message`, else `AwaitUserMessage` |
| `AwaitUserMessage` (none) | Waits for any of, in this order, `TurnControlRequests`, `QueuedUserMessages.for_one()`, and, while the plan is `draft` or `active`, `PlanExecutions.for_one(<revision>)`. Locks `TurnCancellationRequest` in Execute, so no turn starts while `cancel_turn` decides. A steer: moves to `ApplyTurnControl`. A message: stores it and starts a `chat` or `planning` turn. An approval: clears `PendingPlanExecution`, marks the plan `active`, and starts an `executing` turn. | `ApplyTurnControl`, `RunModel`, or itself for a stale approval |
| `RunModel` (none) | Ends the turn and moves to `ApplyTurnControl` without a model call when the turn is steered or cancelled, or else to `CompactContext` when a compaction policy's threshold is crossed. Otherwise calls `run_model_step`. 15 min timeout, 2 min heartbeat timeout, 3 attempts in 30 min, failure route `RecordTurnFailure`. On `ToolCallsRequested`, stores the run state, one `AgentToolCalls` record per call, and the interrupts; records `write_todos` calls, and every call but `write_todos` of a planning turn, final in this commit. | `AwaitUserMessage`, `ApplyTurnControl`, `CompactContext`, or `go_to_many` with one Step per tool call plus `ResumeTurn` |
| `CompactContext` (`CompactContextInput`) | Calls `summarize_context` once and stores `ContextSummary`. `RunModel`'s timeouts, attempts, and failure route. | `RunModel` |
| `AwaitToolApproval` (tool call ID) | Waits for `ToolCallCancellations.for_one(id)` or `InterruptResolutions.for_one(id)`, plus a Timer at the deadline, rounded up to whole seconds, while the interrupt is pending. Approved: marks the record `approved`. Rejected or expired: records `rejected` and publishes `ToolCallResults`. A Timer that fires before the deadline waits again. A cancellation records `rejected` with its reason. | `CallTool` or `CallSingleAttemptTool`, or ends |
| `AwaitQuestionAnswer` (tool call ID) | Waits for `ToolCallCancellations.for_one(id)` or `InterruptResolutions.for_one(interrupt_id)`, plus a deadline Timer while the question is pending, or a zero Timer while a tool's question is not raised yet. Locks `PendingInterrupts` in Execute, where it raises a tool's question. Records the answer, decline, or deadline and publishes `ToolCallResults`, or returns a tool question's call to its tool Step, or a task's question to `PollToolTask`. Steering never cancels a task's question; `cancel_turn` cancels the wait and abandons the task. | Ends, the tool Step, `PollToolTask`, `FinishTurnCancellation`, or itself |
| `AwaitTimer` (tool call ID) | Waits on a Timer for the whole seconds until `wait_until`, or `ToolCallCancellations.for_one(id)`. Records `succeeded`, or `rejected` when cancelled, and publishes `ToolCallResults`. | Ends |
| `CallTool` (tool call ID) | Runs one `read` or `idempotent_write` call, unless the turn is steered or cancelled before `tool_call_started`. 3 attempts, failure route `RecordToolFailure`. Records `succeeded`, or `failed` for `ToolExecutionError`, and publishes `ToolCallResults`. A `ToolInputRequested` result raises a question instead, and a `ToolTaskStarted` result records `waiting_for_task` and writes `tool_task_started`. | Ends, `AwaitQuestionAnswer`, or `PollToolTask` |
| `CallSingleAttemptTool` (tool call ID) | Runs one `non_idempotent_write` or `unclassified` call. 1 attempt, failure route `RecordToolOutcomeUnknown`. | Ends, `AwaitQuestionAnswer`, or `PollToolTask` |
| `PollToolTask` (tool call ID) | Waits on a Timer for `task_poll_interval_seconds`, or a zero Timer while `task_answer_pending`, then calls `poll_tool_task`. 3 attempts, failure route `RecordToolFailure` for `read` and `idempotent_write` calls and `RecordToolOutcomeUnknown` otherwise. No lock: steering waits for the task's result, and `cancel_turn` cancels the poll and drops its result. Records the result and publishes `ToolCallResults`, stores the next interval and continuation, or records the task's question. | Ends, itself, `AwaitQuestionAnswer`, or `FinishTurnCancellation` |
| `RecordToolFailure` (tool call ID) | Records `failed` for a read and `outcome_unknown` for a write, with the recovery detail in `error`, and publishes `ToolCallResults`. A call whose tool Step never started (`attempt_progress` `NOT_STARTED`) records `failed` with `TOOL_CALL_NOT_RUN_ERROR_PREFIX` instead. | Ends |
| `RecordToolOutcomeUnknown` (tool call ID) | Records `outcome_unknown` with the recovery detail in `error`, and publishes `ToolCallResults`. A call whose tool Step never started records `failed` with `TOOL_CALL_NOT_RUN_ERROR_PREFIX` instead. | Ends |
| `ResumeTurn` (expected result count) | Waits for `ToolCallResults.for_n(count)` or `TurnControlRequests`. Locks `PendingInterrupts` in Execute. A steer wake cancels the pending interrupts of the turn's approvals and of questions no task asked, and wakes each such approval, question, and durable wait through `ToolCallCancellations`, then waits again. | `RunModel`, `ApplyTurnControl` when steered or cancelled, or itself |
| `ApplyTurnControl` (none) | Consumes every steered message and stale wake. Ends an active steered or cancelled turn with the runner's unfinished-turn messages or a note, and writes `turn_steered` or `turn_cancelled`. | `RunModel` with the steered messages, else `AwaitUserMessage` |
| `RecordTurnFailure` (the failing Step's input, ignored) | Records `last_turn_error`. Appends the runner's unfinished-turn messages when it implements `build_unfinished_turn_messages`. When the turn has final tool call records, appends one turn-failure note. Clears the turn's records and run state. | `AwaitUserMessage` |
| `FinishTurnCancellation` (turn ID) | Locks `TurnCancellationRequest` and `PendingInterrupts`. When `cancel_turn` cancelled the model call or compaction, moves to `ApplyTurnControl`. While tool calls run, records each call whose wait was cancelled as `rejected`, hands each started task to `CancelToolTask`, and ends; `ResumeTurn` then ends the turn. | Ends, `ApplyTurnControl`, `CancelToolTask`, or a waiting Step whose answer committed first |
| `CancelToolTask` (tool call ID) | Calls `cancel_tool_task` when the runner implements `ToolTaskCanceller`, then records `outcome_unknown` and publishes `ToolCallResults`. 3 attempts in 2 min, 30 s timeout, failure route `RecordToolOutcomeUnknown`. | Ends |

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
| `AgentMessages` | AttributeMap `AGENT_MESSAGES`, key `message_sequence_key(n)` (12-digit zero-padded) | `AgentMessage`; only the window, from `AgentMessageIndex.window_first_sequence` |
| `AgentMessageChunks` | AttributeMap `AGENT_MESSAGE_CHUNKS`, key `message_sequence_key(first sequence)` | `AgentMessageChunk`, immutable, consecutive messages listed in `AgentMessageIndex.chunks` |
| `AgentMessageIndex` | Attribute `AGENT_MESSAGE_INDEX` | `AgentMessageIndex`; unset until a Step first writes a chunk |
| `TurnReplySequence` | Attribute `TURN_REPLY_SEQUENCE` | `TurnReplySequence`, written by the Step that starts a turn |
| `FrameworkRunState` | Attribute `FRAMEWORK_RUN_STATE` | string, set only while a turn waits for tool calls |
| `AgentToolCalls` | AttributeMap `AGENT_TOOL_CALLS`, key = tool call ID | `AgentToolCall`, deleted when the turn ends |
| `PendingInterrupts` | Attribute `PENDING_INTERRUPTS` | `list[AgentInterrupt]` |
| `AgentUsage` | Attribute `AGENT_USAGE` | `AgentUsage` |
| `QueuedUserMessages` | Channel `QUEUED_USER_MESSAGES` | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap `INTERRUPT_RESOLUTIONS`, key = interrupt ID | `AgentInterruptResolution` |
| `ToolCallResults` | Channel `TOOL_CALL_RESULTS`, internal | `ToolCallCompleted` |
| `SteeredUserMessages` | Channel `STEERED_USER_MESSAGES` | `AgentUserMessage` |
| `TurnControlRequests` | Channel `TURN_CONTROL_REQUESTS`, internal | `TurnControlRequest` |
| `TurnCancellationRequest` | Attribute `TURN_CANCELLATION_REQUEST` | `TurnCancellationRequest`, written only by `cancel_turn` |
| `ToolCallCancellations` | ChannelMap `TOOL_CALL_CANCELLATIONS`, internal, key = tool call ID | `ToolCallCancellation` |
| `AgentTraceContext` | Attribute `AGENT_TRACE_CONTEXT` | string, the W3C `traceparent` of the current activation's root span when its context is valid, sampled or not; deleted otherwise |
| `AgentPlan` | Attribute `AGENT_PLAN` | `AgentPlan`, unset until `write_todos` first succeeds |
| `PlanExecutions` | ChannelMap `PLAN_EXECUTIONS`, key `plan_execution_key(revision)` (decimal) | `PlanExecutionRequest` |
| `PendingPlanExecution` | Attribute `PENDING_PLAN_EXECUTION` | `PlanExecutionRequest` that `execute_plan` accepted and `AwaitUserMessage` has not consumed |
| `ContextSummary` | Attribute `CONTEXT_SUMMARY` | `ContextSummary`, set once the session compacts |
| `AgentSessionTitle` | Attribute `AGENT_SESSION_TITLE`, indexed FULL_TEXT `text1`; only with `summary_attributes_indexed` | `str`, the first user message's title; set by the first turn only |
| `AgentSessionLastActivityAt` | Attribute `AGENT_SESSION_LAST_ACTIVITY_AT`, indexed DATETIME `datetime1`; only with `summary_attributes_indexed` | `str`, RFC 3339 time the latest turn started or ended |
| `AgentSessionTurnCount` | Attribute `AGENT_SESSION_TURN_COUNT`, indexed INT `int1`; only with `summary_attributes_indexed` | `int`, `AgentState.turn_count` as of the latest turn start or end |
| `AgentEvents` | Stream `flow.agent_events`, one per Flow type | `AgentEvent` |

`AgentState.status` is `waiting_for_user_message`, `running_model`,
`running_tools`, or `compacting_context`, and `AgentState.interaction_mode` is `chat`, `planning`, or
`executing`. An `AgentToolCall` record is `awaiting_approval`, `approved`,
`awaiting_answer`, `waiting_for_timer`, or `waiting_for_task` while it waits,
and `rejected`, `succeeded`, `failed`, or `outcome_unknown` when final; runners
only see final records, and `poll_tool_task` a `waiting_for_task` one.

RPCs:

| RPC | Input | Output | Notes |
| --- | --- | --- | --- |
| `send_user_message` | `AgentUserMessage` | `bool` | Transactional; `False` for blank text, and for a `plan_mode` message when plan mode is off. |
| `execute_plan` | `ExecutePlanRequest` | `bool` | Locks `PendingPlanExecution`; `True` only while the session waits for a user message, the plan has this revision and is `draft` or `active`, no execution of it is pending, and nothing is queued, steered, or pending. `AgentSessionClient` retries lock conflicts. |
| `steer_user_message` | `SteerUserMessageRequest` | `bool` | Transactional, locks nothing. Moves a queued message by `message_id`, or steers with new `text`; while no turn is active, queues `text`. `False` for both or neither, blank text, a message no longer queued, or a turn that does not accept steering; `ChannelMessageNotFoundError` when it was consumed while the RPC ran. |
| `delete_queued_message` | `DeleteQueuedMessageRequest` | `bool` | Transactional, locks nothing. `False` for a message no longer queued. |
| `cancel_turn` | `CancelTurnRequest` | `bool` | Locks `TurnCancellationRequest` and `PendingInterrupts`. Cancels the turn's model call, compaction, and waiting Steps, and moves to `FinishTurnCancellation`. `False` when no turn is active, `turn_id` names another turn, or the turn does not accept cancellation. |
| `resolve_interrupt` | `ResolveInterruptRequest` | `bool` | Locks `PendingInterrupts`; `False` when not pending or past its deadline; removes the interrupt and publishes the answer in one commit. On a question, `approved=False` declines it and `approved=True` returns `False`. A concurrent Step can raise `RpcLockConflictError`; `AgentSessionClient` retries it for 5 seconds. |
| `answer_question` | `AnswerQuestionRequest` | `bool` | Locks `PendingInterrupts`; `False` when the interrupt is not a pending question, is past its deadline, or the answer is blank or not an allowed choice; removes it and publishes the answer in one commit. `AgentSessionClient.answer_question` retries lock conflicts like `resolve_interrupt`. |
| `get_agent_snapshot` | none | `AgentSnapshot` | Loads the window and every kept chunk. State, last 200 stored messages, pending interrupts, the active turn's tool calls, usage, the queued message count, the plan, and the queued and steered messages with their IDs. Serves retained closed sessions. |
| `list_agent_messages` | `ListAgentMessagesRequest` | `AgentMessagesPage` | Loads the window and every kept chunk. Oldest first; limit clamped to 1..200; the last page starts at `AgentMessageIndex.first_sequence`, and `deleted_before_sequence` is set once retention deleted earlier messages. |

Events on `AgentEvents` (`AgentEventType`): `turn_started` `{"sequence"}`,
`turn_completed` `{"message_count"}`, `turn_failed` `{"error"}`,
`turn_steered` `{"steered_message_count", "message_count"}`,
`turn_cancelled` `{"message_count"}`, `text_delta`
and `reasoning_delta` `{"text"}`, `tool_call_started` `{"tool_call_id",
"tool_name"}`, `tool_call_finished` `{"tool_call_id", "tool_name", "status"}`
(once per tool call, including rejections), `tool_call_deferred`
`{"tool_call_id", "tool_name", "effect", "requires_approval"}`,
`interrupt_raised` `{"interrupt_id", "tool_call_id", "tool_name", "deadline",
"kind", "question", "choices", "allows_free_text", "display"}` (`display` is
`{"target", "summary", "preview"}` or `null`), `interrupt_resolved`
`{"interrupt_id", "approved", "rejection_message", "kind", "answered",
"cancellation_reason"}` (`steered` or `turn_cancelled` when steering or
cancellation removed the interrupt), `timer_started` `{"tool_call_id",
"wait_until", "reason"}`, `plan_updated` `{"revision", "status",
"task_count"}` (after `write_todos`, or when an approval marks a draft
`active`), `plan_execution_started` `{"revision"}`, `tool_task_started`
`{"tool_call_id", "tool_name", "task_id"}`, `usage_recorded`
`{"recorded", "session"}`, `compaction_started` `{"through_sequence"}`, and
`compaction_finished` `{"generation", "through_sequence"}`.

Every event carries `turn_id`, `step_execution_id` (shared by all attempts of
one Step execution), and the Step `attempt`. A UI groups live text by
`(turn_id, step_execution_id, attempt)`: within one `step_execution_id` it
keeps only the highest `attempt` seen and drops the partial text of lower
attempts. Different `step_execution_id` values of a turn are separate model
call and tool call Steps, and each keeps its text. After `turn_completed`,
`turn_failed`, `turn_steered`, or `turn_cancelled`, replace the turn's live
text with the stored messages. Streams
are best effort; rebuild from `get_agent_snapshot`.

## MCP tools (`dex_agents.mcp`)

`dex_agents.mcp` turns the tools of MCP servers into tools of an agent
session. Every MCP tool call runs in its own tool Step, with an effect class
from the tool's annotations or from the [Dex MCP
conventions](#dex-mcp-conventions), so a crash never repeats a write silently.
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

A built-in loop agent takes the source in `AgentDefinition.tool_sources`; see
[Tool sources](#tool-sources). Another runner offers the tools and defers
every call; the tool Step runs it:

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

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        handlers = {**self._local_handlers, **await tools.deferred_tool_handlers()}
        return await handlers[call.tool_call.tool_name](call)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        return await tools.poll_tool_task(poll)
```

`list_tools` lists each server on its own and caches its tools per Worker.
The first listing of a server is awaited; after `listing_ttl` (5 minutes) a
server is listed again in the background while its earlier tools are
returned. A server that cannot be listed is left out with a warning log and
is not tried again for `listing_failure_backoff` (30 seconds), then in the
background, so one bad server never hides the others' tools or slows every
model call. One listing per server runs at a time. `list_tools(refresh=True)`
lists every server again now. `deferred_tool_call` stores the
server and the tool's MCP name on the call's record, so the tool Step calls
that server directly, without a listing; `call_tool` finds the server through
the listing. Tool names are `<server>__<tool>`, with characters other than
letters, digits, `_`, and `-` replaced by `_` and names over 64 characters
shortened with a hash. When two tools get the same name, the first in server
order is offered and the other is left out with a warning log. Key
`ToolPolicy` entries by that name.

### Resources and prompts

With `resource_and_prompt_tools_enabled=True`, `list_tools` also offers three
`READ` tools after the servers' tools, when at least one server is
configured. Each takes a configured server's name as `server`, an `enum` of
the names, and opens a new connection to that server with the source's
transport, headers, and `connection_timeout`:

| Tool | Arguments | Output |
| --- | --- | --- |
| `mcp_list_resources_and_prompts` | `server` | `{"prompts": [...], "resource_templates": [...], "resources": [...]}`, every page |
| `mcp_read_resource` | `server`, `uri` | `{"contents": [...]}` |
| `mcp_get_prompt` | `server`, `name`, optional `arguments` (strings by name) | `{"description": ..., "messages": [...]}` |

Outputs are the server's items as it sent them, in JSON with sorted keys, so
both cores show the same text. A server that does not offer resources or
prompts lists none. A connection failure is retried `connection_attempts`
times. An MCP error answer, a server that stays unreachable, and an
unconfigured server raise `ToolExecutionError`. Enable the tools on one
source of an agent only. Resource subscriptions and list-change notifications are
not followed. The names are `MCP_LIST_RESOURCES_AND_PROMPTS_TOOL_NAME`,
`MCP_READ_RESOURCE_TOOL_NAME`, and `MCP_GET_PROMPT_TOOL_NAME`; key a
`ToolPolicy` by them to require approval.

### Effects

| MCP annotations | Effect | Tool Step | Approval by default |
| --- | --- | --- | --- |
| any, with the Dex `effect` key in `_meta` | the declared effect; see [Dex MCP conventions](#dex-mcp-conventions) | by effect | Only with `approval: recommended` |
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
`execute_deferred_tool_call(call, request_meta=...)` adds more `_meta`
entries to the request, such as the identity a caller expects the server to
check before it runs the call; the idempotency key wins over an entry of the
same name. `will_resume_tool_task_without_sending(call)` is `True` only when
executing the call returns a task an earlier attempt started and can never send
`tools/call`: the session polls tasks, the server uses Streamable HTTP, and the
call's checkpoint names the task. A task polled inside the call may be sent
again when the server lost it, so such a caller skips a check it makes before
every `tools/call` only when this is `True`.

| What happens | Recorded | Re-issued? |
| --- | --- | --- |
| A result | `succeeded`: text blocks joined with newlines, other content as JSON | - |
| A result with `image` content, on a source built with `media_store` | `succeeded`: each image uploaded and recorded in `images`; the text shows `[image <n>]` in its place, counting from 1. More than 20 images, base64 that is invalid or not canonical, or bytes that are not a supported image: `failed` (`ToolExecutionError`) | - |
| An `isError`, `rejected`, or `unknown` result with `image` content | As above, with `[image <n> omitted]` for each image in the text, with or without a store, so the recorded error holds no image bytes | As above |
| An `isError` result, or a JSON-RPC error response other than `-32603` | `failed` with the text (`ToolExecutionError`) | No |
| A result or error with the Dex `outcome` key | `outcome_unknown` or `failed`; see [Dex MCP conventions](#dex-mcp-conventions) | No |
| The call was never sent: its server is not configured, or 3 connection attempts failed | `failed` | No |
| The stream broke, the server exited, or the request timed out after it was sent; an internal error (`-32603`), an HTTP error status, or a body that is not JSON-RPC (`-32700`), which a gateway can return after the server ran the call; a server that keeps asking to repeat the call | `McpToolCallInterruptedError`, a Step failure | `read` and `idempotent_write`: `CallTool` sends a new request, same idempotency key, up to 3 attempts. Other effects: `outcome_unknown`, never sent again |
| The Worker dies during the call | as for any tool Step | Same as above |

MCP 2026-07-28 removed stream resumption, so a broken stream is never resumed;
the effect class alone decides whether the call is sent again.

`notifications/progress` become `mcp.tool_progress` events on `AgentEvents`
(`{"tool_call_id", "tool_name", "progress", "total", "message"}`).

### Long-running MCP tasks

The source declares the tasks extension, `io.modelcontextprotocol/tasks`, on
every request. A server may then answer `tools/call` with a task handle. MCP
2026-07-28 has no protocol session and lets the server choose the task TTL, so
any connection can poll the task.

A Streamable HTTP server's task becomes a [tool task](#tool-tasks): the tool
Step saves the task ID as its heartbeat checkpoint, returns `ToolTaskStarted`,
and ends. `PollToolTask` then calls `poll_tool_task` on a Timer, which polls
`tasks/get` once and writes an `mcp.task_status` event when the status
changed. The next interval is
the server's `pollIntervalMs`, rounded up to whole seconds, or, when that is
shorter, double the previous interval, up to a minute, so a task that runs for
hours adds a few Steps a minute at most. A completed task is the tool's
result; a failed or cancelled task records `failed`. No Step holds a
connection while the task runs, a Worker restart only delays the next poll,
and a task can run for days. A `CallTool` attempt that dies after `tools/call`
returned the task reads the checkpoint on its retry and returns the same task
without calling the tool again, once the checkpoint reached Dex; a lost
checkpoint leaves only the idempotency key to drop a second task. A runner
without `poll_tool_task` gets every task polled inside the tool Step, as
below. A server that no longer knows the task, or
cannot be reached for three poll attempts, records `failed` for a `read` tool
and `outcome_unknown` otherwise; the source never calls the tool again from a
poll. `cancel_turn` abandons the task, and `cancel_tool_task` sends
`tasks/cancel`; forward it from your runner's `cancel_tool_task`, as the
built-in loop runner does. Any answer from the server ends the cancellation, so a
retried one is harmless.

A stdio task lives in its server process, which ends with the request. The
tool Step polls it on the call's own connection, with a heartbeat per poll,
for at most `task_wait_timeout` (10 minutes by default) per attempt, holding a
Worker slot. When the wait passes, or the Worker dies, `CallTool` retries a
`read` or `idempotent_write` call and the retry calls the tool again; other
effects record `outcome_unknown`. `call_tool`, the framework adapter path,
polls every task this way.

### Input requests

A task in `input_required` that asks one form `elicitation/create` whose
schema has a single `string` field, with or without `enum`, or a single
`boolean` field becomes a session question: `poll_tool_task` returns
`ToolInputRequested` with the elicitation's message, the `enum` values or
`yes` and `no` as choices, and the request key and a fingerprint of the
request in its continuation. `AwaitQuestionAnswer` raises the `question`
interrupt `<tool_call_id>-input-<n>`. The first poll after the question is
resolved, which runs at once, sends the resolution with `tasks/update`: an
`accept` input response with the answer, `decline` for a declined question,
or `cancel` for a passed deadline. It sends it only while the task still
lists that same request, so it never answers a different later request
under the same key, and only on its first attempt, after a heartbeat. With
output sent, a half-close of the stream fails the attempt instead of returning
it as not run, so once the send may have happened, Dex retries that attempt
rather than handing it out again under the same attempt number.
A retried attempt cannot tell whether the first one sent it, and the task may
since have asked the identical request again, so it asks the user again
instead: the user may answer twice, but an answer never reaches a later
question.

Right after `tasks/update`, the task may still list the answered request until
it takes the answer in. For 10 seconds after the send, polls run a second apart
and read that request as working; the record's continuation carries the send
time. Once a poll sees the task leave `input_required`, or after those 10
seconds, an identical request is a new question, so a task that asks the same
thing once per batch is asked each time. The task's own result then ends the
call. When `tasks/update` fails for a decline, or the task is cancelled after
one, the source cancels it best-effort and fails the poll, so the call is
recorded `failed` for a `read` tool and `outcome_unknown` otherwise.

Any other input request records `failed` with `the MCP server asked for input
that this agent session cannot provide yet (...)`, naming each request, such as
`an answer to 'Which account?'`, and the waiting task is cancelled. A stdio
task's input request fails this way too, because its process ends with the
request. An `input_required` answer to `tools/call` itself, outside a task,
records `failed` the same way; mapping it to a question is a follow-up. A
state-only `input_required` answer, which only asks for the same request
again, is retried at once.

### Dex MCP conventions

An MCP server built for Dex, such as one that serves Dex connectors, carries a
connector's guarantees across MCP with `_meta` keys under the
`io.superdurable.dex/` prefix. A server that sets none of them behaves exactly
as described above. Both cores follow the same rules, and
[`agents/conformance/mcp_dex_conventions.json`](../../conformance/mcp_dex_conventions.json)
fixes them case by case. Since 0.5.0.

| Key | Where | Values |
| --- | --- | --- |
| `io.superdurable.dex/effect` (`TOOL_EFFECT_META_KEY`) | tool definition `_meta` in `tools/list` | `read`, `idempotent_write`, `non_idempotent_write` |
| `io.superdurable.dex/approval` (`TOOL_APPROVAL_META_KEY`) | tool definition `_meta` in `tools/list` | `recommended` |
| `io.superdurable.dex/outcome` (`TOOL_OUTCOME_META_KEY`) | `tools/call` result `_meta`, or JSON-RPC `error.data._meta` | `unknown`, `rejected` |
| `io.superdurable.dex/idempotencyKey` (`IDEMPOTENCY_KEY_META_KEY`) | `tools/call` request `_meta`, sent by the source | `<flowId>/<turnId>/<toolCallId>` |

**Tool definitions.** A tool with the `effect` key is declared, as a connector
manifest declares an operation for `ConnectorToolSource`:

- Its effect class is the declared value, whatever its annotations say:
  `read` and `idempotent_write` run in `CallTool`, `non_idempotent_write` in
  `CallSingleAttemptTool`. A declared write runs in the model's order after the
  turn's earlier writes, and a write after one that did not succeed is skipped.
- Without a session policy, its calls wait for approval only with `approval:
  recommended`, like `agent.approval: recommended` in a manifest. Any other
  `approval` value, `null` included, also asks, so an unknown value fails
  closed.
- A session `ToolPolicy` in `AgentSessionOptions.tool_policies` for the tool
  may keep or raise its safety, never lower it. The call gets the more
  conservative of the declared effect and the policy's effect, ranked `READ`,
  `IDEMPOTENT_WRITE`, `NON_IDEMPOTENT_WRITE`, then `UNCLASSIFIED`. So a policy
  `ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE)` on a tool declared `read` runs
  it once in `CallSingleAttemptTool`, in the model's order, after approval. A
  declared `non_idempotent_write` stays one with a `READ` policy.
- The policy's `requires_approval`, when set, wins. When it is `None`, a call
  waits for approval when the tool recommends it or the policy's effect asks
  for approval by default, as `NON_IDEMPOTENT_WRITE` does.
- A framework adapter gets the same rule from
  `tools.merge_tool_policies(your_policies)`. Do not spread your policies
  after `tools.tool_policies()`: yours would replace the declaration and
  could lower its effect.
- `approval` without `effect` is ignored. An `effect` value outside the three
  above gives the tool the session's `default_tool_effect` and ignores its
  annotations, with a warning log.
- `trusts_tool_annotations=False` also ignores both keys.

**Results.** The `outcome` key reports what a call did:

| `outcome` | Recorded | The model sees | Sent again? |
| --- | --- | --- | --- |
| `unknown` | `outcome_unknown` (`ToolOutcomeUnknownError`), with the result's text as `error` | `OUTCOME_UNKNOWN_TOOL_RESULT` | Never, for any effect class |
| `rejected` | `failed` (`ToolExecutionError`) | `The tool call failed: <text>` | Never |
| absent | as in "What a call does" | | |

- The key wins over `isError` and over the JSON-RPC error code. Set
  `isError: true` with either value, so a client that does not know these
  conventions never reads the result as a success.
- The text is the result's text: text blocks joined with newlines, other
  content as JSON. A result without text gets a fixed text. Write a message
  that is safe to show the model and the user.
- In the JSON-RPC error form, put the key in `error.data._meta`. The text is
  `MCP error <code>: <message>`. An internal error (`-32603`) with the key is
  not re-issued.
- A task's completed result follows the same rule, whether the tool Step or
  `PollToolTask` reads it.
- Other values are reserved; the source ignores them with a warning log.

**Idempotency key.** Every `tools/call` from a tool Step carries the session's
idempotency key. It is the same on every attempt of one tool call, including a
`CallTool` retry and an attempt after a Worker restart, and it is unique per
tool call. Treat it as opaque. A server that follows these conventions derives
its own call ID from it, such as a hash, and keeps a fence per call ID: a
retried attempt gets the first attempt's result and never applies the write a
second time. When the server cannot tell whether an earlier attempt took
effect, it answers `outcome: unknown`. It should refuse a declared write that
arrives without the key, because it cannot recognize a retry of that call.
Task requests (`tasks/get`, `tasks/update`, `tasks/cancel`) carry no key; the
task ID names the call there.

A declared tool, as `tools/list` returns it:

```json
{
  "name": "send_payment",
  "description": "Send a payment.",
  "inputSchema": {"type": "object", "properties": {"amount": {"type": "integer"}}},
  "annotations": {"idempotentHint": true},
  "_meta": {
    "io.superdurable.dex/effect": "idempotent_write",
    "io.superdurable.dex/approval": "recommended"
  }
}
```

A result whose outcome is unknown:

```json
{
  "content": [{"type": "text", "text": "the payment provider timed out"}],
  "isError": true,
  "_meta": {"io.superdurable.dex/outcome": "unknown"}
}
```

### With a framework adapter

The runner above is the core's runner-agnostic path. A framework adapter uses
the source when it accepts tools defined by a JSON Schema: wrap each `McpTool`
as a framework tool whose function calls `tools.call_tool(name, arguments,
idempotency_key=...)` with the key from the adapter's tool context, and pass
`tools.merge_tool_policies(your_policies)` as the adapter's `tool_policies`.
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

## Connector tools (`dex_agents.connectors`)

`dex_agents.connectors` offers reviewed Connector operations to a built-in loop
agent as tools. `ConnectorToolSource` implements `ToolSource`, like
`McpToolSource`. Each call runs one operation in its own tool Step through
the Connector SDK's `run_query` or `run_mutation`, so the connector's Call
ID, idempotency key, credential handling, and single-dispatch fence apply.
Install the Connector SDK with the `connectors` extra, plus each Connector
package the agent uses:

```bash
pip install "dex-agents[connectors]" dex-connector-stripe
```

The extra installs Connector SDK 0.9.0, the first release that exports
`raise_dex_retry`.

```python
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.builtin_loop import AgentDefinition
from dex_connectors.slack import new_project_connection as slack_connection
from dex_connectors.stripe import new_project_connection as stripe_connection

slack = slack_connection(project, "support-slack")
stripe = stripe_connection(project, "billing-stripe")

agent = AgentDefinition(
    name="support",
    instructions="You help customers with their orders.",
    model=model,
    tool_sources=[
        ConnectorToolSource([
            connector_tool(slack.post_thread_reply(), slack.reference),
            connector_tool(
                stripe.operations.create_refund(),
                stripe.reference,
                requires_approval=lambda arguments: arguments["amount"] > 5000,
            ),
        ]),
    ],
)
```

Nothing is exposed by default. `connector_tool` reads the operation's entry
in its package's `dex-connector.json`, read as package data, and refuses an
operation whose entry lacks `agent.exposed: true`, an `agent` value it does
not know, or an `inputSchema` keyword it cannot check. The tool name defaults
to `<connectorId>_<operation in snake case>`, such as `stripe_create_refund`;
the description and argument schema come from the manifest. The snake case is
connectorctl's, which also names the Python Step factories: an acronym run
stays one word, so `listStoryIDs` becomes `list_story_ids`.
`agents/conformance/connector_tools.json` pins it.

| Operation | Effect | Tool Step | Retries |
| --- | --- | --- | --- |
| Query | `READ` | `CallTool` | The operation's Execute retry policy, at most three attempts |
| Mutation with `agent.retry: safe` | `IDEMPOTENT_WRITE` | `CallTool` | The same |
| Any other Mutation | `NON_IDEMPOTENT_WRITE` | `CallSingleAttemptTool` | None |

The source gives each call's tool Step the operation's `executeMethodTimeout`
and retry policy through `tool_call_step_options`, a movement-scoped option.
Tool Steps stay `SYNC`. A session `tool_policies` entry for a connector tool
changes only its approval; the effect always comes from the manifest.

Approval: a call waits for `resolve_interrupt` when the manifest says
`agent.approval: recommended`. Other Mutations run without approval, though
`NON_IDEMPOTENT_WRITE` asks by default elsewhere. `requires_approval=True` or
`False` overrides the manifest; a predicate decides per call from the decoded
JSON arguments, and one that raises asks for approval. Arguments that break the
schema never run, so they never wait.

What a call does:

1. Check the arguments against `inputSchema` (a closed subset of JSON Schema
   2020-12: `type`, `properties`, `required`, `additionalProperties`, `items`,
   `enum`, `anyOf`, `$ref` into `$defs`, `minimum`, `maximum`, and
   annotations), then decode them into the manifest's `inputType` with the Dex
   codec. Either failure is `ToolExecutionError`, which the model reads; the
   operation never runs.
2. Build the `Call` with `derive_call_id(flow_id, step_execution_id, operation)`
   from `DeferredToolExecution.context`. The Step execution ID is the same on
   every attempt, so a retry keeps the Call ID and the idempotency key.
3. Hand the operation a `ConnectorStepContext`. It stores each heartbeat value,
   such as the fence's marker, as the tool Step's text checkpoint, so the
   session's background heartbeats repeat the marker and the next attempt
   reads it back. It exposes only what the fence uses, which
   `tests/test_connector_tool_source.py` pins.
4. Run the operation. A connector `RetryError` goes through `raise_dex_retry`,
   so the tool Step's retry policy applies. A single-attempt write that asks to
   retry would then record `OUTCOME_UNKNOWN`. When its fence saved `notSent` or
   `canResend` in this attempt, which proves the provider did not process it, it
   records `FAILED` with `CONNECTOR_NOT_SENT_RETRY_LATER` instead.
5. Map the selected branch with `classify_connector_branch`. The first
   matching row wins. The rows before the listing can only make an outcome
   unknown:

| Branch | Record | The model reads |
| --- | --- | --- |
| `uncertain`, whatever the listing or the required branches say | `OUTCOME_UNKNOWN` (`ToolOutcomeUnknownError`) | `OUTCOME_UNKNOWN_TOOL_RESULT` |
| A Mutation's `defect`, whatever the listing or the required branches say | `OUTCOME_UNKNOWN` | The same text |
| A required (success) branch other than `defect` | `SUCCEEDED` | The value as compact JSON, keys sorted |
| Any Mutation branch when its fence cannot prove that nothing was sent | `OUTCOME_UNKNOWN` | The same text |
| Listed `rejected` under `agent.branches` | `FAILED` (`ToolExecutionError`) | `"<connector> <operation> returned <branch>: <kind>: <message>"` |
| Listed `unknown`, or a Mutation branch not listed | `OUTCOME_UNKNOWN` | `OUTCOME_UNKNOWN_TOOL_RESULT` |
| A Query branch not listed, including `defect` | `FAILED` | `"<connector> <operation> returned <branch>: <kind>: <message>"` |

A Mutation's `defect` is not always local: the Connector SDK selects it for
some attempts that already called the provider, such as an invalid attempt or
an undeclared branch, and the source cannot tell those apart. A Query's
`defect` follows its listing.

A Mutation that uses the single-dispatch fence gets one more check, the same
one the Go Connector SDK makes for Go agent hosts. The source observes every
fence marker the attempt saves through its Step context, bound to the call's
Call ID and attempt. Once the fence claims a send, or records a provider ID,
the attempt may have sent. Only a later `canResend` marker of the same call and
attempt, after a claim without a provider ID, lowers that, as the fence's own
`record_can_resend` does. The operation's own later checkpoints never lower it.
On a retry that reads the earlier checkpoint, a missing checkpoint, one that is
not a fence marker, a malformed one, another call's, or one that does not allow
this attempt a send also means the request may have been sent. While the
request may have been sent, every branch but the success branch records outcome
unknown, whatever the listing says, and a Retry is never proof that nothing was
sent. The core cannot see inside the fence, so it reads the markers instead of
the fence's own events: a forged `canResend` marker of the same call would
count. A Mutation that never reads its checkpoint and never saves a fence
marker gets no such check and relies on its listing.
`agents/conformance/connector_tools.json` pins these cases in
`fenceObservationCases`.

A result over `max_result_bytes` (default `DEFAULT_CONNECTOR_RESULT_MAX_BYTES`,
64 KiB of UTF-8 text, set per source or per tool) is cut, followed by
`CONNECTOR_RESULT_TRUNCATED_NOTE`, and `AgentToolCall.truncated_output_bytes`
holds the full size.

Writes are ordered: the source marks every connector write
`DeferredToolCall.ordered`, so the writes of one model response run one at a
time in the model's order while reads run in parallel. `RunModel` starts the
next queued write (`AgentToolCall.write_queued`) before any model call, and
raises its approval only then. Once a write is not `SUCCEEDED`, the response's
later writes record `FAILED` with `skipped=True` and the error
`TOOL_CALL_NOT_RUN_ERROR_PREFIX + ": " + TOOL_CALL_SKIPPED_AFTER_WRITE_DETAIL`.
Steering and `cancel_turn` cancel queued writes as they cancel unstarted calls.

Every Python framework adapter also takes tool sources, through its own
`tool_sources`; see its README. The Claude Agent SDK adapter runs each source
call when its whole-turn segment defers it. The Step-mode adapters (OpenAI
Agents, Pydantic AI, LangGraph, and Google ADK) share
`dex_agents.builtin_loop.FrameworkToolSourceRouter`: it lists the sources in each
`RunModel`, refuses a source tool that repeats a framework tool or another
source's tool, builds each call through its source, and routes the call and
its tool Step options back to that source. The adapter only offers the listed
tools as native tools whose bodies never run. A tool the turn already called
but the live listing lacks is replay-only (`SourceToolListing.replay_only_tools`):
it may resume its recorded call, and a new call of it is answered inside the
framework with `SOURCE_TOOL_NOT_OFFERED_RESULT`, never in a tool Step. The Go core has function tools
only.

## Browser streams

`dex_agents.ui_stream` lets a browser render a live session from its
`AgentEvents` Stream and recover after a reconnect. It needs no store besides
Dex and no web framework. An application mounts two endpoints:

- **Snapshot.** `read_agent_event_stream_snapshot(client, flow, session_id)`
  returns the `get_agent_snapshot` result plus the Dex resume token to open the
  event stream from. Serve its `to_json()`: `{"snapshot": ..., "resume_token":
  ...}` in the wire format. While a turn is active, the token replays that
  turn's events, so the browser can rebuild its live text. The snapshot leaves
  out the fields only the session's Steps use: `framework_item` on messages,
  the framework's replay data, which can hold the agent's instructions, and
  `tool_source_reference`, `trace_context`, `input_continuation`,
  `input_answers`, `task_answer_pending`, and `runner_tool_state` on tool calls. Read them with `get_agent_snapshot` on the
  server when needed. It includes `session_settings` as stored, so keep
  anything a browser must not see out of session settings, or remove the key.
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
its new attempt's text. After `turn_completed`, `turn_failed`, `turn_steered`,
or `turn_cancelled`, and after any long gap, it reloads the snapshot. Approve or reject a pending interrupt from
the snapshot with `AgentSessionClient.resolve_interrupt`.

Limits:

- Stream events live in the memory of the active Dex Server process, so a
  restart or failover loses them, and trimming can remove old events. With the
  Stream Store disabled (`streamStore.backend: disabled`), the snapshot still works, with an empty token, and the
  event stream yields only keep-alive comments; poll the snapshot instead.
- The event stream never ends by itself; the web framework closes it when the
  browser disconnects. An async generator stops when the framework cancels
  it; a synchronous one first finishes its current long poll, up to
  `poll_timeout`.
- Stopping in the browser only closes the response. To stop the turn, the
  application's stop route calls `cancel_turn`.

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
consumes an input from outside the session: `StartAgentSession`,
`AwaitUserMessage`, or `ApplyTurnControl` with a user message, or
`AwaitToolApproval` with an interrupt answer or an expired deadline. It ends when every Step it started has
ended or waits again. Each activation is one trace:

| Span | Kind | Emitted by | Parent |
| --- | --- | --- | --- |
| `invoke_agent {agent name}` | `INTERNAL` | Each attempt of the Step that starts the activation | None. It is the trace root, with a span link to the previous activation's root. |
| `chat`, or `chat {model}` when the runner returns `ModelCallMetadata` | `CLIENT`; `INTERNAL` when the runner reports `model_calls` other than 1 | Each `RunModel` and `CompactContext` attempt, when it ends | The activation's root |
| `execute_tool {tool name}` | `INTERNAL` | Each `CallTool`, `CallSingleAttemptTool`, and `PollToolTask` attempt | The root of the activation that deferred or approved the call |

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
| `invoke_agent` | `dex.agent.user_message.key` | `message_sequence_key` of the user message |
| `invoke_agent` | `dex.agent.interrupt.id`, `dex.agent.interrupt.approved` | The interrupt and its decision |
| `chat` | `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.provider.name`, `gen_ai.response.id`, `gen_ai.response.finish_reasons` | From `ModelCallMetadata`, when the runner returns it |
| `chat` | `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` | From `usage`, when `model_calls` is 1 and the input or output count is not zero |
| `chat` | `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.reasoning.output_tokens` | `cached_input_tokens` and `reasoning_tokens`, under the same condition, when not zero |
| `chat` | `dex.agent.input_messages.last_key` | `message_sequence_key` of the newest stored message the call received; for `CompactContext`, the last message it summarized |
| `chat` | `dex.agent.output_messages.keys` | `message_sequence_key` values of what this call produced: the `TurnCompleted` messages after the turn's last tool result |
| `chat` | `dex.agent.requested_tool_call.ids` | `AgentToolCalls` keys that a `ToolCallsRequested` created |
| `execute_tool` | `gen_ai.tool.name`, `gen_ai.tool.call.id` | The tool and its `AgentToolCalls` key |
| `execute_tool` | `dex.agent.tool.effect`, `dex.agent.tool_call.status` | The effect and the final status |

The `chat` span is named `chat {request_model}` when the runner returns
`ModelCallMetadata.request_model`, and `chat` otherwise. Each framework
adapter in this repository returns `ModelCallMetadata` with what its framework
reports for the call and leaves the rest unset. Its README lists the fields it
sets and where they come from.

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
message sequence keys, and tool call IDs above, because the content is already
durable in Dex. Read it with `get_agent_snapshot`, `list_agent_messages`, or
`AsyncClient.get_attributes`. With `should_capture_message_content=True`, spans
also carry content as JSON text in the GenAI message format:

- `invoke_agent`: `gen_ai.input.messages`, the user message.
- `chat`: `gen_ai.input.messages`, the stored messages plus the turn's tool
  calls and their recorded results, and `gen_ai.output.messages`, what this
  model call produced. For `CompactContext`, the messages it summarized and
  the summary text.
- `execute_tool`: `gen_ai.tool.call.arguments` and `gen_ai.tool.call.result`.

Content comes from the `AgentMessage` projections (`text` and the `tool_*`
fields), not from `framework_item`. System instructions and tool definitions
are not captured. Content can be sensitive and large; capture it only where the
tracing backend may store it.

The spans set no `gen_ai.input.messages_ref` or `gen_ai.output.messages_ref`.
Those attributes take an absolute URI, and Dex has no URI for a stored
message. Use the `dex.agent.*` keys with the Flow ID instead.

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
(or `DEX_SERVER_ZD_BINARY`). The engine runs with
`dev.durabilityMode: processCrashSafe`, which skips fsync and survives a
process kill, through a `-zd-config` file that also works with `dexcli`
releases that predate `--durability`. A
`server_zd_config` file of your own replaces it, so set `dev.durabilityMode`
there too. The Worker runs in a child process in its own process group, so a
test can SIGKILL it at an exact moment.

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

The 2,000-turn compaction session behind the agent deployment guide's storage
numbers runs for minutes. `pytest` skips it unless
`DEX_AGENTS_LONG_TESTS=1`, and `ci.sh` runs an 800-turn guard with a retention
limit of 100 instead. The
`Agents Long Tests Nightly` workflow runs it every night. Run it by hand with:

```bash
cd agents/python/core
DEX_AGENTS_LONG_TESTS=1 DEXCLI=/path/to/dexcli uv run --frozen pytest -s tests/test_compaction.py -k 2000_turn
```

## Design decisions

The plan records each of these decisions too.

- Step mode: one `RunModel` Step per model call and one Step per tool call,
  READ included. No framework loop runs several model calls in one Step.
- Tool call Steps and their failure routes take the tool call ID as input.
  There is no shared "current tool call" in `AgentState`.
- `RecordToolFailure` records `failed` for a read and `outcome_unknown` for a
  write: it is reached only through crashes, timeouts, or exceptions other
  than `ToolExecutionError`, so a write may have taken effect.
- A failed tool Step whose recovery error reports `attempt_progress`
  `NOT_STARTED` never ran the tool, so its call records `failed` with
  `TOOL_CALL_NOT_RUN_ERROR_PREFIX` rather than a new status. The model sees an
  ordinary failure and can call the tool again.
- `AgentToolCall.deferral_order` keeps model order for display and for the
  turn-failure note; AttributeMap keys alone do not keep order.
- By default nothing from a turn is stored until it completes: `TurnCompleted`
  carries all of the turn's messages and `ToolCallsRequested` keeps them in the
  framework run state. A runner may store them incrementally with
  `ToolCallsRequested.new_messages`; the built-in loop does.
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

- Without compaction, `RunModel` loads every stored message, and nothing is
  deleted. `get_agent_snapshot`, `list_agent_messages`, and a
  `RecordTurnFailure` of a runner that builds unfinished-turn messages load
  every kept chunk; retention bounds that load.
- Each model call and tool call adds a Step round trip.
- Steering never stops a model call or a tool call that has started; it
  applies at the next safe Step boundary. `cancel_turn` stops a model call
  but never a tool call Step that has started.
- Interrupt deadlines use the Worker clock.
- One `text_delta` call writes one Stream message; batch very small chunks.
- Tool call IDs that are empty or contain `/` fail the turn.
- The tool Steps use SYNC durability, and Dex never hands one attempt number
  to a second Worker, so a single-attempt tool does not run twice after a
  Worker crash. Cancellation is cooperative, though: a cancelled run may keep
  running while the failure route starts.
- `attempt_progress` `NOT_COMPLETED` is conservative, so a call whose tool
  never ran can still record `outcome_unknown`, such as when Dex Server
  crashed after it recorded the attempt start but before the task reached the
  Worker.
- A tool the framework cannot defer runs inside `RunModel` and repeats on a
  `RunModel` retry.
- From server-zd 0.1.21, turn records cleared at turn end no longer count
  against the Server's `limits.maxFlowStateBytes` run budget. Older servers
  count them as superseded values until the history window prunes them.
- `dex_agents.mcp` polls a stdio server's MCP task inside its tool Step for at
  most `task_wait_timeout`, holding a Worker slot, because the task ends with
  the server process.
- Each `PollToolTask` poll is a Step in the session's history; a task that
  runs for days adds up to a Step a minute while the server's interval is
  shorter.
- An MCP task that asks the identical request again within 10 seconds of an
  answer, without leaving `input_required` in between, waits until those 10
  seconds pass before the user is asked. A server slower than 10 seconds to
  take an answer in has its question asked twice.
- A retried `PollToolTask` attempt asks the user again rather than send an
  answer that its first attempt may have sent.
- An MCP `input_required` answer to `tools/call` outside a task records
  `failed`; mapping it to a session question interrupt is a follow-up.
- `dex_agents.mcp` starts a new stdio process, or opens a new HTTP connection,
  for every request. An unreachable server is tried again, with its connection
  attempts, on every `list_tools` call until it answers.
- MCP annotations are hints from the server and are trusted by default. A
  wrong `readOnlyHint` on a write makes that write run without approval and
  retry; set `trusts_tool_annotations=False` or a `ToolPolicy` for servers you
  do not control.
- Images: the media stores keep no local read cache, so each `load_images`
  reads the store.
- Tracing covers the session's Steps only. The Dex SDK emits no spans, so Dex
  Server and Worker internals do not appear in the traces.
- The adapters report only the model information their framework exposes, so
  some `chat` spans carry no provider, response ID, or finish reasons. Each
  adapter's README lists what it sets.
