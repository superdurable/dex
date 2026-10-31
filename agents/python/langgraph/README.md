# dex-agents-langgraph

`dex-agents-langgraph` (import `dex_agents_langgraph`) runs agents built with
LangChain's `create_agent` and custom LangGraph graphs as durable Dex agent
sessions. It implements the runner contract of the
[`dex-agents` core](../core/README.md); the plan is
[agent-framework-adapters.md](../../../docs/design/plan/agent-framework-adapters.md).

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

The package needs Python 3.11 or later, `langchain>=1.4.7,<2`,
`langchain-core>=1.7.3,<2`, `langgraph>=1.2.19,<2`, and the released
`dex-python-sdk==1.30.0`. Until it is published, depend on it by path:

```toml
[project]
dependencies = ["dex-agents-langgraph", "dex-python-sdk==1.30.0", "langchain>=1.4.7,<2"]

[tool.uv.sources]
dex-agents-langgraph = { path = "agents/python/langgraph", editable = true }
dex-agents = { path = "agents/python/core", editable = true }
```

Develop the package itself with uv 0.12.2:

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
see the [core README](../core/README.md#running-an-agent).

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
idempotency keys, the snapshot, and an agent without the middleware.
Construction tests cover two tool nodes with one tool name.

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
  history, while tool calls run. Very long sessions store a large value.
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
