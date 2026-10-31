# dex-agents

`dex-agents` (import `dex_agents`) is the Python core of the Dex agent
framework adapters. It provides one durable agent session Flow,
`AgentSessionFlow`, and the `AgentFrameworkRunner` contract that each
framework adapter implements. The plan is
[agent-framework-adapters.md](../../../docs/design/plan/agent-framework-adapters.md).

One Flow execution is one agent session; the Flow ID is the session ID. The
Flow owns the conversation, tool approvals, deferred tool execution, usage, and
live events. The framework runs its own loop inside the `RunAgentTurn` Step.
Model calls and READ tools run inside that loop. Every other tool call is
deferred to its own Dex Step, so no write runs twice silently.

## Installation

The package depends on the released `dex-python-sdk==1.30.0` and needs
Python 3.11 or later. An adapter package depends on it by path:

```toml
[project]
dependencies = ["dex-agents", "dex-python-sdk==1.30.0", "<framework>==<version>"]

[tool.uv.sources]
dex-agents = { path = "../core", editable = true }
```

Develop the core itself with uv 0.12.2:

```bash
cd agents/python/core
uv sync --locked
DEXCLI=/path/to/dexcli ./ci.sh
```

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
`AsyncWorker` serves it. A session never completes by itself; it waits for the
next user message after every turn. Stop it with `AsyncClient.stop_flow`.

## The runner contract

An adapter implements three methods. The session calls them from Steps, so
each call may run again after a crash or retry. Keep the runner stateless
between calls and derive everything from the arguments.

```python
class AgentFrameworkRunner(Protocol):
    def build_user_message(self, text: str) -> FrameworkMessage: ...
    async def run_turn(self, turn: AgentTurnContext) -> AgentTurnOutcome: ...
    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult: ...
```

### `build_user_message(text)`

Return `FrameworkMessage(role=MessageRole.USER, framework_item=..., text=text)`.
The session stores it as the turn's first message before `run_turn` runs. Any
other role fails the Step.

### `run_turn(turn)`

`AgentTurnContext` carries committed state:

| Field | Meaning |
| --- | --- |
| `flow_id`, `turn_id`, `attempt` | Session ID, `"turn-<n>"`, and the one-based `RunAgentTurn` attempt. |
| `messages` | Every stored `AgentMessage` in sequence order. The last user message belongs to this turn. |
| `framework_run_state` | `None` on the first segment; otherwise the value from this turn's last `TurnSuspended`. `is_resuming` tests it. |
| `tool_calls` | This turn's `AgentToolCall` records by tool call ID. When resuming, every record is final. `recorded_tool_call(id)` reads one. |
| `resolve_tool_policy(name, framework_effect=None)` | The policy for a tool: explicit policy, else framework metadata, else `default_tool_effect`. |
| `events` | `AgentEventWriter` bound to the turn and attempt: `write_text_delta`, `write_reasoning_delta`, `write_tool_call_started`, `write_tool_call_finished`, `write_event`. |
| `heartbeat()` | Records liveness. The session already heartbeats every 20 seconds in the background. |

A runner must:

1. Without run state, rebuild framework input from each message's
   `framework_item` and start the loop. With run state, restore the framework
   from it and resume.
2. For every tool call the model makes, call `turn.resolve_tool_policy(name,
   framework_effect)`. Pass `framework_effect` when the framework exposes
   metadata, such as MCP `readOnlyHint` (`READ`) or `idempotentHint`
   (`IDEMPOTENT_WRITE`). Run the call inline only when `policy.should_defer` is
   `False`; that is a READ tool without approval.
3. Defer every other call with the framework's own mechanism and return
   `TurnSuspended(framework_run_state, deferred_tool_calls, usage)`. Each
   `DeferredToolCall(tool_call_id, tool_name, arguments, policy)` carries the
   resolved policy. `framework_run_state` must be non-empty. There must be at
   least one deferred call. Tool call IDs must be non-empty, contain no `/`,
   and be new within the turn; never defer an ID that is in `turn.tool_calls`.
4. When resuming, give each deferred call the result from its final record
   instead of running the tool. `describe_tool_call_outcome(record)` renders
   the model-visible text: the output, `"The tool call failed: <error>"`, the
   rejection message (or `DEFAULT_REJECTION_TOOL_RESULT`), or
   `OUTCOME_UNKNOWN_TOOL_RESULT`. A framework with a native rejection API may
   pass `record.rejection_message` there instead.
5. Return `TurnCompleted(new_messages, usage)` once the framework produces the
   final answer. `new_messages` holds every item the turn produced after its
   user message, in order, across all segments: model output, tool requests,
   tool results (deferred ones included), and the final answer. The session
   appends them in one commit. Nothing is appended when a turn suspends.
6. Report `usage` for this segment only. The session adds it to the session
   total.

Any exception fails the attempt. `RunAgentTurn` retries up to three attempts
within 30 minutes, rerunning the segment's model calls and READ tools, then
routes to `RecordTurnFailure`. Each attempt has a 15 minute timeout.

### `execute_deferred_tool_call(call)`

`DeferredToolExecution` carries `flow_id`, the approved `tool_call` record,
`idempotency_key` (`"<flow_id>/<tool_call_id>"`), `attempt`, `events`, and
`heartbeat`. Run the real tool once and return `ToolExecutionResult(output)`.
Raise `ToolExecutionError(message)` for a failure the model should see; the
session records `failed` with that message and does not retry. Any other
exception is a Step failure: `IDEMPOTENT_WRITE` and approved `READ` calls retry
up to three attempts and then record `failed` with the error text;
single-attempt calls record `outcome_unknown`.

## Effects and policies

| Effect | Where it runs | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | Inside `RunAgentTurn` | With the turn | Runs again | No |
| `idempotent_write` | `ExecuteToolCall` | Up to 3 attempts, key `<flowId>/<toolCallId>` | Runs again with the same key | No |
| `non_idempotent_write` | `ExecuteSingleAttemptToolCall` | One attempt | Outcome unknown | Yes |
| `unclassified` | `ExecuteSingleAttemptToolCall` | One attempt | Outcome unknown | Yes |

`ToolPolicy(effect, requires_approval=None)` overrides the defaults per tool
name; `None` uses the effect's default approval. `AgentSessionOptions` also
sets `default_tool_effect` (default `unclassified`), the `AgentEvents` Stream
capacity (default 10 MiB), and an optional `interrupt_deadline`. A `read` tool
that requires approval is deferred; after approval it runs in
`ExecuteToolCall`.

## Flow shape

| Step | What it does | Next |
| --- | --- | --- |
| `StartAgentSession` | Start Step, input `AgentSessionStartInput`. Initializes state, usage, and interrupts. | `RunAgentTurn` with a non-blank `initial_message`, else `AwaitUserMessage` |
| `AwaitUserMessage` | Waits for `QueuedUserMessages.for_one()`, stores the user message, starts the turn. | `RunAgentTurn` |
| `RunAgentTurn` | Calls `run_turn`. 15 min timeout, 2 min heartbeat timeout, 3 attempts in 30 min, SYNC durability, failure route `RecordTurnFailure`. | `AwaitUserMessage`, `AwaitInterruptResolution`, or a tool Step |
| `AwaitInterruptResolution` | Waits for `InterruptResolutions.for_one(id)` of every awaiting call, plus a Timer at the earliest pending deadline. Applies one answer, or rejects expired calls with `"approval deadline passed"`. | Itself while calls await approval, then the next approved tool Step, then `RunAgentTurn` |
| `ExecuteToolCall` | Runs one `idempotent_write` or approved `read` call. 3 attempts, SYNC, failure route `RecordToolFailure`. | Next approved tool Step or `RunAgentTurn` |
| `ExecuteSingleAttemptToolCall` | Runs one `non_idempotent_write` or `unclassified` call. 1 attempt, SYNC, failure route `RecordToolOutcomeUnknown`. | Same routing |
| `RecordToolFailure` | Records `failed` with the recovery error text. | Same routing |
| `RecordToolOutcomeUnknown` | Records `outcome_unknown`, with the recovery detail in `error`. | Same routing |
| `RecordTurnFailure` | Records `last_turn_error`, appends nothing, clears the turn's records and run state. | `AwaitUserMessage` |

Approved calls run one at a time in `deferral_order`. Tool Steps read their
call from `AgentState.executing_tool_call_id`, and a tool Step whose record is
already final routes on without running.

Persistence, shared by every agent Flow type and exported from `dex_agents`:

| Name | Kind | Value |
| --- | --- | --- |
| `AgentState` | Attribute `AGENT_STATE` | `AgentState` |
| `AgentMessages` | AttributeMap `AGENT_MESSAGES`, key `message_sequence_key(n)` (12-digit zero-padded) | `AgentMessage` |
| `FrameworkRunState` | Attribute `FRAMEWORK_RUN_STATE` | string, set only while a turn is suspended |
| `AgentToolCalls` | AttributeMap `AGENT_TOOL_CALLS`, key = tool call ID | `AgentToolCall`, deleted when the turn ends |
| `PendingInterrupts` | Attribute `PENDING_INTERRUPTS` | `list[AgentInterrupt]` |
| `AgentUsage` | Attribute `AGENT_USAGE` | `AgentUsage` |
| `QueuedUserMessages` | Channel `QUEUED_USER_MESSAGES` | `AgentUserMessage` |
| `InterruptResolutions` | ChannelMap `INTERRUPT_RESOLUTIONS`, key = interrupt ID | `AgentInterruptResolution` |
| `AgentEvents` | Stream `flow.agent_events`, one per Flow type | `AgentEvent` |

RPCs:

| RPC | Input | Output | Notes |
| --- | --- | --- | --- |
| `send_user_message` | `AgentUserMessage` | `bool` | Transactional; `False` for blank text. |
| `resolve_interrupt` | `ResolveInterruptRequest` | `bool` | Locks `PendingInterrupts`; `False` when not pending; removes the interrupt and publishes the answer in one commit. A concurrent Step can raise `RpcLockConflictError`; `AgentSessionClient` retries it for 5 seconds. |
| `get_agent_snapshot` | none | `AgentSnapshot` | State, last 200 messages, pending interrupts, the active turn's tool calls, usage, queued message count. Serves retained closed sessions. |
| `list_agent_messages` | `ListAgentMessagesRequest` | `AgentMessagesPage` | Oldest first; limit clamped to 1..200. |

Events on `AgentEvents` (`AgentEventType`): `turn_started` `{"sequence"}`,
`turn_completed` `{"message_count"}`, `turn_failed` `{"error"}`, `text_delta`
and `reasoning_delta` `{"text"}`, `tool_call_started` `{"tool_call_id",
"tool_name"}`, `tool_call_finished` `{"tool_call_id", "tool_name", "status"}`,
`tool_call_deferred` `{"tool_call_id", "tool_name", "effect",
"requires_approval"}`, `interrupt_raised` `{"interrupt_id", "tool_call_id",
"tool_name", "deadline"}`, `interrupt_resolved` `{"interrupt_id", "approved",
"rejection_message"}`, and `usage_recorded` `{"recorded", "session"}`. Every
event carries `turn_id` and the Step `attempt`; drop a turn's partial text when
a later attempt writes. Streams are best effort; rebuild from
`get_agent_snapshot`.

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
from dex_agents.testing import AgentTestStack, wait_for_agent_snapshot
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
```

Run tests with `pytest-asyncio` in auto mode, as `pyproject.toml` here does.

Inside the Worker child, a tool marks a `FileGate(state_dir, name)` and awaits
`wait_for_release()`; the test awaits `wait_until_reached()` and kills the
Worker at that exact point. `InvocationLog(path)` counts real tool runs across
Worker processes. `wait_until(description, probe)` polls with a deadline
instead of sleeping. The core's own tests in `tests/` use a scripted runner
and show every pattern.

Each package's `ci.sh` installs locked dependencies, runs lint and type
checks, and runs the integration tests with `DEXCLI` set. `Agents CI`
discovers every `agents/*/*/ci.sh`.

## Decisions beyond the plan

- `AgentToolCall` has an extra `deferral_order` field, so approved calls run in
  the order the turn deferred them. AttributeMap keys alone do not keep order.
- A `read` tool that requires approval runs in `ExecuteToolCall` after
  approval, so a resumed turn always sees only final records.
- Nothing from a turn is stored until it completes. `TurnCompleted` carries all
  of the turn's messages; `TurnSuspended` keeps them in the framework run state.
- Message sequences start at 1. A blank `initial_message` starts no turn.
- `last_turn_error` is cleared when a later turn completes.
- `AwaitInterruptResolution` waits on the records still awaiting approval, not
  on `PendingInterrupts`. An answer published just before a deadline Timer
  fires is consumed on the next wait instead of being lost. Deadlines apply
  only to interrupts that are still pending.
- `resolve_interrupt` drops `rejection_message` on approval.
- `AgentSnapshot.state` is `None` until `StartAgentSession` commits.
- Status, role, effect, and record status values are `StrEnum` types in Python;
  the wire carries the plain strings.
- The plan's `registry.add_flow(...)` does not exist in the Python SDK; pass the
  Flows to `Registry([...], allow_async_handlers=True)`.

## Limits

- `RunAgentTurn` and `get_agent_snapshot` load the whole `AgentMessages` map.
  History is never compacted, so very long sessions load more on every turn.
- Approved calls run one at a time, never in parallel.
- A running turn cannot be steered or cancelled; new user messages queue until
  it ends.
- A `RunAgentTurn` retry repeats the segment's model calls and READ tools.
- Interrupt deadlines use the Worker clock.
- One `text_delta` call writes one Stream message; batch very small chunks.
- Tool call IDs that are empty or contain `/` fail the turn.
