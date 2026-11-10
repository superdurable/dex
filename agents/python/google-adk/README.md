# dex-agents-google-adk

`dex-agents-google-adk` (import `dex_agents_google_adk`) runs
[Google ADK](https://google.github.io/adk-docs/) agents as durable Dex agent
sessions. It implements the `AgentFrameworkRunner` contract of
[`dex-agents`](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md) and registers the shared agent session Flow
for an ADK agent. One Flow execution is one session, and the Flow ID is both
the Dex session ID and the ADK session ID.

The session runs in **Step mode**. Each `RunModel` Step lets ADK make one
model call. Every tool call the model asks for, READ included, pauses the ADK
invocation through ADK's own tool confirmation and runs in its own Dex Step;
the tool calls of one response run in parallel. Each call therefore has its
own retry policy, failure route, history entry, and Dex Web node, and a crash
repeats at most the one call that was running. The plan is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md).

## Installation

The package needs Python 3.11 or later. It requires `dex-agents` at the same
version, `dex-python-sdk==1.38.0`, and `google-adk` 2.13 or a later 2.x
release:

```bash
pip install dex-agents-google-adk
```

Develop the package inside this repository with uv 0.12.2. There it resolves
`dex-agents` from `../core` by path through `[tool.uv.sources]`; the published
wheel requires `dex-agents==<same version>` from PyPI instead:

```bash
cd agents/python/google-adk
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

## Running an agent

```python
import asyncio

from dex import AsyncClient, AsyncWorker, BlobCacheConfig, ClientOptions, Registry
from dex import WorkerOptions, open_blob_cache
from dex_agents import AgentSessionClient, ToolEffect, ToolPolicy
from dex_agents_google_adk import adk_agent_session_flow, idempotency_key_for_tool_call
from google.adk.agents import LlmAgent
from google.adk.tools.tool_context import ToolContext


def search_orders(customer_id: str) -> list[dict[str, str]]:
    """List a customer's orders."""
    return [{"order_id": "7", "status": "delivered"}]


def refund_order(order_id: str, tool_context: ToolContext) -> str:
    """Refund one order."""
    key = idempotency_key_for_tool_call(tool_context)
    return f"refund for order {order_id} created with key {key}"


def send_email(to: str, body: str) -> str:
    """Send an email to the customer."""
    return f"sent to {to}"


agent = LlmAgent(
    name="support",
    model="gemini-2.5-flash",
    instruction="Help customers with their orders.",
    tools=[search_orders, refund_order, send_email],
)
flow = adk_agent_session_flow(
    agent,
    flow_type="SupportAgent",
    tool_policies={
        "search_orders": ToolPolicy(ToolEffect.READ),
        "refund_order": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, requires_approval=True),
        # send_email has no policy: unclassified, it runs once after approval.
    },
)


async def start_support_session(sessions: AgentSessionClient) -> None:
    await sessions.start_session("session-42", "Refund my last order, I am customer 9")


async def approve_pending_tool_calls(sessions: AgentSessionClient) -> None:
    snapshot = await sessions.get_agent_snapshot("session-42")
    for interrupt in snapshot.pending_interrupts:
        await sessions.resolve_interrupt("session-42", interrupt.interrupt_id, approved=True)


async def main() -> None:
    registry = Registry([flow], allow_async_handlers=True)
    cache = open_blob_cache(BlobCacheConfig(".dex-cache", 64 * 1024 * 1024))
    worker = AsyncWorker(registry, cache, WorkerOptions(server_address="localhost:8801"))
    client = AsyncClient(registry, cache, ClientOptions(server_address="localhost:8801"))
    serving = asyncio.create_task(worker.start())
    await worker.wait_pull_registered(timeout=30)
    sessions = AgentSessionClient(client, flow)
    await start_support_session(sessions)
    # Later, for example from an approval UI:
    await approve_pending_tool_calls(sessions)
    await serving


asyncio.run(main())
```

Build the Flow once per agent, with a Flow type unique within the Registry,
and build the same Flow in every process that starts sessions or calls their
RPCs. The Steps are `async`, so the Registry needs `allow_async_handlers=True`
and an `AsyncWorker` serves it. Tool calls of one model response run as
parallel Steps, so give the Worker enough task slots
(`PullWorkerOptions.max_concurrent_tasks`) for the largest batch. The model
reads its key from the environment as in any ADK application, such as
`GOOGLE_API_KEY`.

`adk_agent_session_flow(agent, *, flow_type, ...)` takes:

| Argument | Default | Meaning |
| --- | --- | --- |
| `agent` | required | Root `BaseAgent`, or an ADK `App` that carries it with plugins and compaction or context cache configuration. |
| `flow_type` | required | Flow type of the agent's sessions. Keep it stable. |
| `tool_policies` | none | `ToolPolicy` by tool name. |
| `default_tool_effect` | `UNCLASSIFIED` | Effect of a tool without a policy or ADK metadata. |
| `interrupt_deadline` | none | How long an approval may stay pending. |
| `event_stream_capacity_bytes` | 10 MiB | Byte budget of the `AgentEvents` Stream. |
| `run_config` | `RunConfig(streaming_mode=StreamingMode.SSE)` | ADK run configuration of every run. |
| `user_id` | `"user"` | ADK user ID of every session. |
| `artifact_service`, `memory_service`, `credential_service` | none | ADK services, owned by the application. |

`AdkAgentRunner` takes the same agent and ADK arguments when you build
`AgentSessionFlow` yourself.

## Step shape

A turn whose model reads once and then answers runs these Steps:

```text
AwaitUserMessage -> RunModel -> CallTool -> ResumeTurn -> RunModel -> AwaitUserMessage
```

A response with several tool calls starts one Step per call, in parallel. A
call that needs approval waits in `AwaitToolApproval` first. See the
[core README](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md#flow-shape) for every Step.

## Effects and policies

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

A tool's effect comes from, in order:

1. Its `ToolPolicy` in `tool_policies`.
2. ADK metadata: `load_artifacts`, `load_memory`, and `preload_memory` are
   `read`. An MCP tool with `readOnlyHint` is `read`; with `idempotentHint` it
   is `idempotent_write`.
3. `default_tool_effect`.

A tool built with ADK's `FunctionTool(func, require_confirmation=...)` always
waits for approval when its `require_confirmation` is true for the call, even
when its policy says otherwise.

`idempotency_key_for_tool_call(tool_context)` returns a call's idempotency key,
`"<flow ID>/<turn ID>/<tool call ID>"`. It is the core's
`DeferredToolExecution.idempotency_key`, which the adapter passes to the tool
in `tool_context.custom_metadata` under `IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY`.
Every attempt of an `idempotent_write` call gets the same key; pass it to the
external system.

### ADK control tools

Some ADK tools steer the invocation itself, so they run inside `RunModel` and
get no Step: `transfer_to_agent`, `exit_loop`, `set_model_response`,
`finish_task`, and the tools ADK makes for sub-agents in `single_turn` or
`task` mode. The tool calls of those sub-agents get Steps as usual. A model
response that calls a control tool together with other tools fails the turn
before any tool runs, because ADK would apply the control tool before the
other results exist.

## Tool sources and connector tools

`tool_sources` takes the core's tool sources, such as `ConnectorToolSource`,
which offers reviewed Connector operations (install `dex-agents[connectors]`).
See [Connector tools](https://github.com/superdurable/dex/blob/main/docs/content/agent-frameworks/deploy-agents.mdx#connector-tools)
for what the manifest decides.

```python
from dex_agents.connectors import ConnectorToolSource, connector_tool

stripe = new_project_connection(project, "billing-stripe")
flow = adk_agent_session_flow(
    agent,
    flow_type="SupportAgent",
    tool_sources=[
        ConnectorToolSource([
            connector_tool(stripe.operations.create_refund(), stripe.reference),
        ]),
    ],
)
```

- ADK resumes a confirmed call only with one of the agent's own tools, so
  the runner adds one toolset to the `tools` of every `LlmAgent` of the tree
  when it is built. Each `RunModel` lists every source, and the toolset offers
  that listing, with the source's name, description, and JSON Schema. Outside
  the runner's `RunModel` it lists nothing, so the agents behave as before.
  The agent's `before_tool_callback` chain still sees each call first.
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
  or a source's `declared_tool_names` repeat a tool, an agent name, or an ADK
  control tool of the tree, or another source's tool. The same names listed at
  run time fail that `RunModel` attempt.
- The runner polls no tool tasks. A source call that starts one records
  `failed` for a `read` call and `outcome_unknown` otherwise. A source whose
  listing fails or takes longer than `tool_source_listing_timeout` offers no
  tools in that model call, except tools the turn already called, which come
  from the source's `cached_tools()` so the resumed run still finds them.

## How a tool call runs

The adapter adds a plugin, after the App's own plugins, to a copy of the App
for every `RunModel` attempt. For each tool call except a control tool, the
plugin resolves the policy, then runs the calling agent's
`before_tool_callback` chain, so a guardrail callback can still answer the
call without a Step. Otherwise it calls `tool_context.request_confirmation`,
and ADK pauses the invocation on an `adk_request_confirmation` call once the
response's calls are all paused. `RunModel` returns `ToolCallsRequested` with
the turn's events so far as the framework run state.

Each tool Step calls `execute_deferred_tool_call`, which finds the first tool
with the call's name in a depth-first walk of the agent tree and calls its
`run_async` once. The next `RunModel` answers each `adk_request_confirmation`
call, one message per ADK branch, with `confirmed` set to whether the call was
approved. ADK re-executes the confirmed calls, and the plugin returns the
recorded result instead of running the tool:

| Record status | Function response the model sees |
| --- | --- |
| `succeeded` | The tool's response. A bare string result is `{"result": "<text>"}`. |
| `failed` | `{"error": "The tool call failed: <error>"}`, where `<error>` is the `ToolExecutionError` message, the failure of a read whose attempts ran out, or `TOOL_CALL_NOT_RUN_ERROR_PREFIX`, `": "`, and the failure for a call whose tool never ran |
| `rejected` | `{"error": "<rejection message>"}` |
| `outcome_unknown` | `{"error": "<OUTCOME_UNKNOWN_TOOL_RESULT>"}` |

Raise `dex_agents.ToolExecutionError` from a tool to report a failure the
model should see without a retry; the record becomes `failed`. Any other
exception, a timeout, or a Worker crash fails the tool Step: a `read` or
`idempotent_write` call retries, and once its attempts run out the record
becomes `failed` for a read and `outcome_unknown` for a write. A
single-attempt call records `outcome_unknown` at once. When the recovery
error's `attempt_progress` is `NOT_STARTED`, the tool never ran, and the call
records `failed` with `TOOL_CALL_NOT_RUN_ERROR_PREFIX` instead, so the model
may call the tool again.

When a turn fails after some of its tool calls finished, the session stores a
turn-failure note through `build_user_message`: an ADK user event whose text
lists each call and its outcome. The next turn's model sees it as user text,
so it does not repeat those writes.

## Multi-agent trees

The adapter runs the App as resumable (`ResumabilityConfig(is_resumable=True)`),
so a paused `SequentialAgent`, `LoopAgent`, `ParallelAgent`, or sub-agent
continues where it stopped. A task-mode sub-agent that asked the user a
question receives the user's later messages, because the adapter lets ADK
append the turn's user message and replays stored user messages with the
isolation scope ADK gave them.

The next `RunModel` answers the paused calls of each ADK branch in its own
message, and ADK runs on once the first branch finishes. That is safe only
when every branch is an `LlmAgent` directly under a root `ParallelAgent`, so a
model step whose tool calls span several branches anywhere else fails the turn
before any tool runs: for example a `ParallelAgent` inside a `SequentialAgent`
or `LoopAgent`, a root `ParallelAgent` whose branch is a `SequentialAgent` or
`LoopAgent`, or two `single_turn` sub-agents called in one response. After every resume, each
answered call must have reached its agent with the recorded result; otherwise
the turn fails and the session stores the turn-failure note.

Sub-agents in `single_turn` or `task` mode need a root `LlmAgent`, because ADK
runs them only there; construction fails otherwise.

An `LlmAgent` makes one model call per `RunModel`. Agents that continue
without tools make their model calls in the same `RunModel`: the next agent of
a `SequentialAgent`, the target of `transfer_to_agent`, a sub-agent in
`single_turn` or `task` mode, and the other branches of a `ParallelAgent`. A
`RunModel` retry repeats those calls too.

A tool Step finds its tool by name, so different tools in different agents
must have different names. Construction fails when two agents declare
different tools with one name; a toolset's tools are checked when they are
called. An `AgentTool` runs its whole agent inside one tool Step, with the
App's plugins but without Steps for its inner calls. A read `AgentTool` whose
agent has a tool that is not `read` fails the turn, because a read Step can
retry and repeat those writes; give such an `AgentTool` a write effect.

## History and live events

Each `RunModel` rebuilds the ADK session from the stored messages: every
message's `framework_item` is one ADK `Event` as JSON, in the format ADK's own
database session services store. Session state is the replay of the events'
state deltas. A completed turn stores every event ADK appended, as if ADK had
run each tool call directly: it leaves out the `adk_request_confirmation`
calls, their answers, the placeholder responses, and content-less
resumability markers. The display fields project each event:

| Event | `role` | Tool fields |
| --- | --- | --- |
| User text | `user` | none |
| Model text or function calls | `assistant` | Set when the event has one function call. |
| Function responses | `tool` | Set when the event has one function response. |

With the default `RunConfig`, model text streams to `AgentEvents` as
`text_delta` events, and provider thought summaries as `reasoning_delta`
events. Control tools write `tool_call_started` and `tool_call_finished`; the
session writes them for every tool Step. Every event carries `turn_id`,
`step_execution_id`, and `attempt`; group live text by them as the
[core README](https://github.com/superdurable/dex/blob/main/agents/python/core/README.md) describes. Usage comes from each response's
usage metadata: `input_tokens` counts prompt and tool-use prompt tokens,
`output_tokens` counts candidate and thought tokens, and `model_calls` counts
model requests.

## Tracing

With tracing on in `AgentSessionOptions.tracing`, each `RunModel` returns
`ModelCallMetadata` for its `chat` span, taken from the plugin's model
callbacks:

| Field | Source |
| --- | --- |
| `request_model` | `LlmRequest.model`, when every model call of the `RunModel` requested the same model |
| `response_model` | `LlmResponse.model_version` of the complete responses, for a single model call |
| `finish_reasons` | The last reported `LlmResponse.finish_reason`, lowercased as ADK's telemetry does, for a single model call |
| `provider_name` | Never set. ADK names no provider for a call; its telemetry guesses one from the model name, and a custom `BaseLlm` can serve any model |
| `response_id` | Never set. `LlmResponse` does not carry the provider's response ID |

A `RunModel` whose agents continue without tools makes several model calls,
so its span is `INTERNAL` and carries only a shared request model. The
responses of parallel branches interleave, so the adapter cannot tell which
call each one answered.

## Testing

`ci.sh` installs the locked dependencies, runs ruff, the format check, and
mypy, and runs the integration tests against private `dexcli dev --engine zd`
engines. It reads `DEXCLI`. The tests use real ADK agents, driven by scripted
`BaseLlm` models in [`tests/`](https://github.com/superdurable/dex/tree/main/agents/python/google-adk/tests), so no model key is needed. They assert
each turn's Step history with `dex_agents.testing.read_step_history`. A Worker
child runs in its own process group, so a test can SIGKILL it during
`RunModel` or a tool Step.

`tests/test_connector_tools.py` runs connector tools from a copy of the core
tests' fake ledger Connector (`tests/fake_ledger`): a Query in its tool
Step, an approved Mutation that runs once, a session policy that changes only
approval, a `retry: safe` Mutation retried with the same Call ID, `uncertain`
writes recorded `outcome_unknown`, two writes of one response in the model's
order, the operation's tool Step options, an unexposed operation, and refused
name collisions.

## Compaction

Since 0.3.0 the runner can compact a session that would outgrow the model's
context window. Pass `compaction=CompactionPolicy(context_window_tokens=...)` to `adk_agent_session_flow`, and optionally `summary_model`; the summary call uses the model of the first `LlmAgent` of the tree. The summary replaces the summarized events in the rebuilt ADK session. Without a policy nothing changes, and a session
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
- ADK's `EventsCompactionConfig` adds compaction events that ADK's own request builder reads. Dex stores them as events and owns the record; use one of the two.
- The rebuilt session must not lose what the summarized events set. The
  summary therefore ends with a line, `[Dex] ADK session state: {...}`, that
  carries the session state and the agent that replied last. The runner
  restores both and keeps that line out of the model input. A task-mode
  sub-agent that waits for the user across turns blocks compaction until it
  finishes, because ADK finds that task from its events.

See [Compaction and the storage bound](https://docs.superdurable.io/agent-frameworks/deploy-agents#compaction).

## Limits

- The App root must be a `BaseAgent`. ADK `Workflow` roots are not supported.
- A tool runs outside the ADK run, with a fresh `ToolContext` whose session
  has no events and empty state. Its state and artifact changes are discarded.
  Pass what it needs as arguments.
- A tool that asks for a confirmation or credential inside its Step fails with
  `ToolExecutionError`.
- A turn fails when ADK pauses for something other than a tool call, such as a
  credential request.
- Agents that continue without tools make their model calls in the same
  `RunModel`, as described above.
- Agent `before_tool_callback`s run in the `RunModel` that requests the
  call, before its Step, and again if that `RunModel` retries. When the turn
  resumes, the plugin answers first, so they do not run then;
  `after_tool_callback`s see both the placeholder and the recorded response,
  as with ADK's own confirmations.
- Code executors other than `BuiltInCodeExecutor` run model-written code
  inside `RunModel`, or inside an `AgentTool`'s Step, where a retry would run
  it again, so construction fails. Run such code from a tool instead.
- A model step whose tool calls span several ADK branches fails the turn,
  except for `LlmAgent` branches of a root `ParallelAgent`, as described above.
- An application plugin's `on_user_message_callback` may rewrite the message
  ADK appends for the turn; the session still stores and replays the original
  text.
- `app:` and `user:` state keys live in the Dex session, not across sessions.
- The adapter never closes the agent's toolsets or plugins; the application
  owns them.
- The core's limits apply: each model call and tool call adds a Step round
  trip. See [Compaction](#compaction) for long sessions.
