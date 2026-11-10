# dex-agents-langgraph

`dex-agents-langgraph` (import `dex_agents_langgraph`) runs agents built with
LangChain's `create_agent` and custom LangGraph graphs as durable Dex agent
sessions. It implements the runner contract of the
[`dex-agents` core](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md); the plan is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

One Flow execution is one agent session; the Flow ID is the session ID. The
session runs in the core's **Step mode**. Every model call is one `RunModel`
Step: the graph makes one model call, and the tool node pauses it with
LangGraph's `interrupt()` before any tool runs. Every tool call, READ tools
included, then runs once in its own Dex Step, in parallel with the other calls
of that model message and after approval when its policy requires it. The next
`RunModel` resumes the graph from the stored LangGraph checkpoint with the
recorded results. A crash repeats at most the one model or tool call that was
running.

## Installation

The package needs Python 3.11 or later. It requires `dex-agents` at the same
version, the released `dex-python-sdk==1.38.0`, `langchain>=1.4.7,<2`,
`langchain-core>=1.7.3,<2`, and `langgraph>=1.2.19,<2`:

```bash
pip install dex-agents-langgraph
```

Develop the package inside this repository with uv 0.12.2. There it resolves
`dex-agents` from `../core` by path through `[tool.uv.sources]`; the published
wheel requires `dex-agents==<same version>` from PyPI instead:

```bash
cd agents/python/langgraph
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

## A `create_agent` agent

Add `DurableToolCallMiddleware` first in the agent's `middleware`, then
register the agent with `langchain_agent_session_flow`. This program serves the
agent, starts a session, approves the refund when the session asks, and prints
the conversation:

```python
import asyncio
import json

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    LongPollTimeoutError,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from dex_agents import AgentEventType, AgentSessionClient, ToolEffect, ToolPolicy
from dex_agents_langgraph import (
    DurableToolCallMiddleware,
    get_tool_call_idempotency_key,
    langchain_agent_session_flow,
)
from langchain.agents import create_agent
from langchain.chat_models import init_chat_model
from langchain_core.runnables import RunnableConfig
from langchain_core.tools import ToolException, tool

ORDERS = {"7": "delivered", "8": "processing"}
REFUNDS_BY_KEY: dict[str, str] = {}


@tool
def search_orders(customer_id: str) -> str:
    """List the customer's orders and their status."""
    return json.dumps(ORDERS)


@tool
def refund_order(order_id: str, config: RunnableConfig) -> str:
    """Refund one order."""
    if order_id not in ORDERS:
        raise ToolException(f"order {order_id} does not exist")
    key = get_tool_call_idempotency_key(config) or order_id
    REFUNDS_BY_KEY.setdefault(key, order_id)
    return f"refunded order {order_id}"


def build_support_agent():
    return create_agent(
        init_chat_model("openai:gpt-5-mini"),
        tools=[search_orders, refund_order],
        system_prompt="You help customers with their orders.",
        middleware=[DurableToolCallMiddleware()],
    )


support_agent = langchain_agent_session_flow(
    build_support_agent,
    flow_type="SupportAgent",
    tool_policies={
        "search_orders": ToolPolicy(ToolEffect.READ),
        "refund_order": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=True),
    },
)


async def run_session(sessions: AgentSessionClient, session_id: str, text: str) -> None:
    await sessions.start_session(session_id, text)
    resume_token = ""
    while True:
        try:
            message = await sessions.read_agent_event(session_id, resume_token)
        except LongPollTimeoutError:
            continue
        resume_token = message.resume_token
        event = message.value
        payload = json.loads(event.payload)
        if event.type == AgentEventType.TEXT_DELTA:
            print(payload["text"], end="", flush=True)
        elif event.type == AgentEventType.INTERRUPT_RAISED:
            print(f"\napproving {payload['tool_name']}")
            await sessions.resolve_interrupt(session_id, payload["interrupt_id"], approved=True)
        elif event.type == AgentEventType.TURN_COMPLETED:
            print()
            return


async def main(address: str = "localhost:8801") -> None:
    registry = Registry([support_agent], allow_async_handlers=True)
    cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024 * 1024))
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address=address))
    client = AsyncClient(registry, cache, ClientOptions(server_address=address))
    serving = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        sessions = AgentSessionClient(client, support_agent)
        await run_session(sessions, "support-session-1", "Please refund order 7")
        snapshot = await sessions.get_agent_snapshot("support-session-1")
        for stored in snapshot.messages:
            print(stored.sequence, stored.role, stored.text or stored.tool_output)
    finally:
        await worker.stop()
        await serving
        await client.close()
        cache.close()


if __name__ == "__main__":
    asyncio.run(main())
```

Run `dexcli dev`, set the model provider's key, and run the program. A real
application runs Workers and Clients in separate processes; build the Flow the
same way in each. `build_support_agent` is a factory, so only a Worker builds
the agent and needs the model key. An `async` factory also works, for example
to load MCP tools first.

`AgentSessionClient`, the session RPCs, events, and the snapshot are the core's;
see the [core README](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#running-an-agent).

## A custom LangGraph graph

A graph built with `StateGraph` works when its state holds the conversation as
LangChain messages with the `add_messages` reducer, as `MessagesState` does.
Build its tool node with `durable_tool_node` instead of `ToolNode`, then
register it with `langgraph_agent_session_flow`:

```python
from dex_agents_langgraph import durable_tool_node, langgraph_agent_session_flow
from langgraph.graph import START, MessagesState, StateGraph
from langgraph.prebuilt import tools_condition

tools = [search_orders, refund_order]
model = init_chat_model("openai:gpt-5-mini").bind_tools(tools)


async def call_model(state: MessagesState) -> dict:
    return {"messages": [await model.ainvoke(state["messages"])]}


builder = StateGraph(MessagesState)
builder.add_node("agent", call_model)
builder.add_node("tools", durable_tool_node(tools))
builder.add_edge(START, "agent")
builder.add_conditional_edges("agent", tools_condition)
builder.add_edge("tools", "agent")

support_graph = langgraph_agent_session_flow(
    builder.compile(),
    flow_type="SupportGraph",
    tool_policies={"search_orders": ToolPolicy(ToolEffect.READ)},
)
```

Pass `messages_key` when the message list has another state key.
`durable_tool_node` defers all calls of one model message with one interrupt,
so they run as parallel Steps.

## Tool sources and connector tools

`tool_sources` takes the core's tool sources, such as `ConnectorToolSource`,
which offers reviewed Connector operations (install `dex-agents[connectors]`).
See [Connector tools](https://github.com/superdurable/dex/blob/main/docs/content/agent-frameworks/deploy-agents.mdx#connector-tools)
for what the manifest decides.

```python
from dex_agents.connectors import ConnectorToolSource, connector_tool

stripe = new_project_connection(project, "billing-stripe")
flow = langchain_agent_session_flow(
    build_support_agent,
    flow_type="SupportAgent",
    tool_sources=[
        ConnectorToolSource([
            connector_tool(stripe.operations.create_refund(), stripe.reference),
        ]),
    ],
)
```

- Each `RunModel` lists every source. `DurableToolCallMiddleware` adds the
  listed tools to every model request of a `create_agent` agent, after the
  agent's own tools. A custom graph binds `get_current_tool_source_tools()`
  next to its own tools in its model node; `durable_tool_node` defers their
  calls like any other. The tools carry the source's name, description, and
  JSON Schema; their bodies never run.
- Every call of a source tool defers, reads included, and runs once in its own
  tool Step through the source's `execute_deferred_tool_call`, never inside
  the framework. A connector call derives its Call ID from the Step execution
  ID and keeps its fence marker in the Step's heartbeat checkpoint.
- The source builds each call, so it decides the effect class, the approval,
  and whether the call is an ordered write; `tool_call_step_options` gives the
  tool Step the operation's timeout and retry policy. A `tool_policies` entry
  for a connector tool changes only its approval.
- Writes of one model response run one at a time in the model's order, as in a
  Stepwise agent. Once one does not succeed, the later ones record `failed`
  with `skipped`.
- Definition fails with `ValueError` when two sources share a `source_name`,
  or a source's `declared_tool_names` repeat a tool of the graph or of
  `tools`, or another source's tool. The same names listed at run time fail
  that `RunModel` attempt.
- The runner polls no tool tasks. A source call that starts one records
  `failed` for a `read` call and `outcome_unknown` otherwise. A source whose
  listing fails or takes longer than `tool_source_listing_timeout` offers no
  tools in that model call, except tools the turn already called, which come
  from the source's `cached_tools()` so the resumed run still finds them.

## How a turn runs

```text
StartAgentSession -> RunModel -> CallTool / CallSingleAttemptTool (one per call, in parallel,
                                 AwaitToolApproval first when needed)
                              -> ResumeTurn -> RunModel -> ... -> RunModel (final answer)
```

1. The turn's first `RunModel` rebuilds every stored message with
   `framework_message_to_langchain_message` and runs the graph with
   `{"messages": history}` on a fresh, turn-local checkpointer. It replaces
   any checkpointer compiled into the graph.
2. The graph makes one model call. For every tool call, the middleware or
   tool node resolves the tool's policy and raises `interrupt()` with a
   `dex_deferred_tool_calls` value. No tool runs inside `RunModel`.
3. The runner returns `ToolCallsRequested`, with one `DeferredToolCall` per
   call in model order. Its run state is the last checkpoint stored in each
   checkpoint namespace plus its pending writes, serialized with the
   checkpointer's serializer, and the calls each interrupt deferred.
4. The session runs each call in its own Step, in parallel, through
   `execute_deferred_tool_call`, which validates the arguments against the
   tool's schema and calls the tool's own `ainvoke` once.
5. After `ResumeTurn` collects every result, the next `RunModel` restores the
   checkpoint and invokes the graph with
   `Command(resume={interrupt_id: recorded_results})`. The interrupted tool
   tasks return the recorded results as their `ToolMessage` values, and the
   graph makes its next model call. The runner fails the call if any recorded
   result did not reach the graph.
6. When the graph finishes, every message it added to the message state is
   stored as one `AgentMessage`: `framework_item` holds
   `message_to_dict(message)` as JSON, and the other fields are a display
   projection.

During `RunModel` the runner streams visible model text as `text_delta` and
provider reasoning summaries as `reasoning_delta`, from LangGraph's
`messages` stream mode, including subgraphs. Group live text by `(turn_id,
step_execution_id, attempt)` as the core README describes, because a
`RunModel` retry streams its model call again. Usage counts each model call's
`usage_metadata`: `input_tokens`, `output_tokens`, `cache_read` input tokens,
and `reasoning` output tokens.

Each `RunModel` also returns `ModelCallMetadata`, so the core's `chat` span is
named `chat <model name>`. The request model is the run metadata's
`ls_model_name`, which the chat model's `_get_ls_params` reports. The response
model, provider, response ID, and finish reason come from the reply's
`response_metadata`: `model_name`, `model_provider`, `id`, and `finish_reason`
or `stop_reason`. A field stays unset when the chat model does not report it.
The runner never uses `ls_provider`, because LangChain derives it from the
class name when an integration does not set it.

The provider becomes the well-known `gen_ai.provider.name` value when one
applies, and any other `model_provider` passes through unchanged:

| `model_provider` | `gen_ai.provider.name` |
| --- | --- |
| `openai` | `openai` |
| `anthropic` | `anthropic` |
| `google_genai` | `gcp.gen_ai`, because `ChatGoogleGenerativeAI` reports `google_genai` for both the Gemini API and Vertex AI |
| `google_vertexai`, `google_anthropic_vertex` | `gcp.vertex_ai` |
| `bedrock`, `bedrock_converse`, `anthropic_bedrock`, `bedrock_mantle_anthropic`, `bedrock_mantle_openai` | `aws.bedrock` |
| `azure`, `azure_openai` | `azure.ai.openai` |
| `xai` | `x_ai` |
| `mistralai` | `mistral` |
| `ibm` | `ibm.watsonx.ai` |
| `cohere`, `deepseek`, `groq`, `perplexity` | The same name |

When one `RunModel` makes
several model calls, the span keeps only the values that every call shares and
leaves out the response ID and finish reason.

## Effects and policies

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

A tool's effect comes from, in order: its `ToolPolicy` in `tool_policies`;
MCP annotations in the tool's `metadata` (`readOnlyHint: True` is `read`,
otherwise `idempotentHint: True` is `idempotent_write`), as tools converted
from MCP carry them; then `default_tool_effect`, which defaults to
`unclassified`. `tool_effect_from_metadata(tool)` shows what the annotations
give.

A tool call Step runs the tool outside the graph:

- The tool is the graph's tool of that name, from any `ToolNode` in the graph
  or its subgraphs, or one passed in `tools=`. Pass tools that middleware adds
  dynamically in `tools=`. Two tool nodes with different tools of one name
  fail the runner's construction unless `tools=` names the one to run.
- A call to a tool the runner does not know, and that no policy names, gets
  a `read` Step without approval, which records `failed`: "the agent has no
  tool named ...".
- `get_tool_call_idempotency_key(config)` returns the session's idempotency
  key, `"<flowId>/<turnId>/<toolCallId>"`, from the tool's `RunnableConfig`.
  The key is the same on every attempt; pass it to systems that deduplicate
  requests.
- Arguments that do not match the tool's `tool_call_schema`, a
  `ToolException`, or an error `ToolMessage` record `failed`, and the model
  sees `"The tool call failed: <message>"`. Any other exception, including a
  `ValidationError` raised inside the tool body, is a Step failure: a `read`
  or `idempotent_write` retries up to three attempts, a single-attempt call
  does not, and then the session records `failed` for a read and
  `outcome_unknown` for a write, because the write may have taken effect.
- The result reaches the model as text. Content that is not a string becomes
  JSON text. A rejection or unknown outcome reaches the model as an error
  `ToolMessage` with the core's text.
- A sub-agent that the tool invokes with `DurableToolCallMiddleware` runs its
  own tools inline, as part of the tool's Step.

The runner also guards every tool. It blocks any tool that starts inside
`RunModel` and fails the call, with `last_turn_error` naming the tool. This
catches an agent built without `DurableToolCallMiddleware`, a plain
`ToolNode`, and a node that invokes a tool through its `invoke`, `ainvoke`,
`run`, or `arun` method. The guard sees tools through LangChain callbacks, so
code that calls a tool's underlying function directly, for example with
`loop.run_in_executor(None, tool.func, ...)`, bypasses it.

The policy of a call comes from the tool its Step runs. A tool in `tools=`
therefore decides the policy of a call to a node's tool of the same name.

## Failed turns

When `RunModel` fails three times, the session records `last_turn_error`.
If the turn already has final tool call records, the session first stores a
turn-failure note built with this runner's `build_user_message`: a
`HumanMessage` listing each call and its result. The next turn replays it to
the model like any other user message, so the model does not repeat a write
that already ran.

## Middleware order

Put `DurableToolCallMiddleware` first in `middleware`, so it is the outermost
tool wrapper. Middleware listed before it see every tool call. Middleware
listed after it never see a call inside a session, because the tool runs
outside the graph. Model middleware, such as dynamic tool selection or
summarization, keeps working; its model calls run inside `RunModel`.

LangChain's `HumanInTheLoopMiddleware` raises its own interrupts, which the
session cannot answer. Use `ToolPolicy(..., requires_approval=True)` and
`resolve_interrupt` instead.

## Testing

`ci.sh` installs the locked dependencies, runs ruff, the format check, and
mypy, and runs the integration tests against private `dexcli dev --engine zd`
engines. It reads `DEXCLI`; `dex-server-zd` sits next to it.

The tests drive a real `create_agent` agent and a custom `StateGraph` with a
scripted chat model, so no model key is needed, and assert each turn's Step
history with `read_step_history`. For both they cover: a READ tool turn and
history replay; parallel tool Steps; approvals that survive a Worker restart,
run once, or wait while a read of the same message finishes; rejections; a
second model call that requests tools again; a Worker SIGKILL during
`RunModel`, which reruns only that model call, and during
`CallSingleAttemptTool`, which records `outcome_unknown`; a failed turn's
note; dynamic and unknown tools; a `ValidationError` inside a tool body; a
subgraph compiled with `checkpointer=False`; and a Worker whose clock runs
behind. With `create_agent` they also cover a second `resolve_interrupt`,
idempotency keys, the snapshot, an agent without the middleware, and the
model and response that name each `chat` span, with an in-process Worker and
an in-memory span exporter. Construction tests cover two tool nodes with one
tool name.

`tests/test_connector_tools.py` runs connector tools from a copy of the core
tests' fake ledger Connector (`tests/fake_ledger`), once with a `create_agent` agent and once with a custom graph: a Query in its tool
Step, an approved Mutation that runs once, a session policy that changes only
approval, a `retry: safe` Mutation retried with the same Call ID, `uncertain`
writes recorded `outcome_unknown`, two writes of one response in the model's
order, the operation's tool Step options, an unexposed operation, and refused
name collisions.

## Compaction

Since 0.3.0 the runner can compact a session that would outgrow the model's
context window. Pass `compaction=CompactionPolicy(context_window_tokens=...)` and `summary_model`, a chat model, to the session flow function. A compiled graph does not expose its model or system prompt, so the summary model is required and the system prompt belongs in the policy's `reserved_input_tokens`. The summary is the first message of the graph input, with the fixed ID `dex-context-summary-<generation>`. Without a policy nothing changes, and a session
started before the upgrade starts compacting once its Worker has one.

- It compacts only between turns, before the first model call of a turn. A
  model call that continues a turn after its tool calls does not compact, so
  one turn's growth must fit in the rest of the window. Lower the trigger
  ratio when a turn can add more than about 15 percent of the window.
- The summary enters the model input as a user message before the kept turns.
  It is never stored as a message, and every message stays readable until
  retention deletes it behind the summary.
- The estimate counts three bytes of each stored message as one token, plus
  the instructions and tool definitions the runner can see. Set the policy's
  reserved input tokens for the rest, such as dynamic instructions.
- The summary call gets the earlier turns as plain text, without tools.
- Summarization middleware changes only the graph's message state. Dex stores only messages with new IDs and owns the record, so a middleware summary is stored as one more message. Use one of the two.

See [Compaction and the storage bound](https://docs.superdurable.io/agent-frameworks/deploy-agents#compaction).

## Limits

- A graph whose nodes make several model calls before they reach a tool
  node, such as summarization middleware or a planner node, makes all of
  them in one `RunModel`, and a retry repeats them all.
- A plain `ToolNode` behind `awrap_durable_tool_call` defers one call of a
  task at a time; use `durable_tool_node` for parallel Steps. An outer
  wrapper that awaits before calling `awrap_durable_tool_call` can reorder
  the interrupts of one task, which fails the `RunModel` call.
- Provider-run tools, such as built-in tools passed as dicts, run inside the
  model call, so they are at-least-once: a `RunModel` retry repeats them.
- History is append-only. Only messages whose IDs are new are stored, so a
  graph that removes, summarizes, or edits earlier messages in its state does
  not change the stored history; the next turn replays it unchanged.
- Only the message list survives between turns. Other state keys, such as
  `structured_response`, start from their defaults on every turn. Within one
  turn they survive, because they are in the checkpoint.
- `FrameworkRunState` holds the graph checkpoint, including the replayed
  history, while tool calls run. Very long sessions store a large value;
  [compaction](#compaction) bounds it to the summary and the kept turns.
- Tools run outside the graph, so they cannot use `ToolRuntime`,
  `InjectedState`, or `InjectedStore`, and cannot return a graph `Command`.
  `InjectedToolCallId` works. A response artifact (`content_and_artifact`) is
  dropped; the model sees the content.
- A subgraph compiled with `checkpointer=False` restarts on resume, so the
  runner fails the turn instead of letting it repeat its tool calls.
- The graph may raise no interrupts of its own, and static `interrupt_before`
  or `interrupt_after` nodes fail the turn.
- State channels of type `DeltaChannel` are rejected, because the stored
  checkpoint keeps only the last checkpoint of each namespace.
- The graph runs without a runtime `context`, so `Runtime.context` is
  `None` in nodes and middleware.
- Tool call IDs from the model must be non-empty, contain no `/`, and be
  unique within one turn.

## Design decisions

- Every tool call, READ included, is deferred with `interrupt()`, so each
  `RunModel` runs the graph up to one model call. There is no replay.
- `durable_tool_node` subclasses `ToolNode` and raises one interrupt for all
  calls of a model message; `create_agent` already sends each call to its
  own task.
- The run state is versioned JSON (`dex-agents-langgraph.checkpoint.v1`)
  holding base64 values from the checkpoint serializer.
  `checkpoint_serializer` accepts any LangGraph `SerializerProtocol`, such as
  `EncryptedSerializer`; every Worker must use the same one.
- The run state keeps the checkpoint stored last in each namespace, not the
  one with the largest ID, because checkpoint IDs follow each Worker's clock.
- A graph may be passed as a factory, sync or async, so model clients and
  tool connections exist only in Workers.
- User messages get the ID `dex-user-<hex>`; a stored message without an ID
  replays as `dex-message-<sequence>`.
