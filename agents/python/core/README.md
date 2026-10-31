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
tests can start a private engine and Worker processes.

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

## The runner contract

An adapter implements three methods. The session calls them from Steps, so
each call may run again after a crash or retry. Keep the runner stateless
between calls and derive everything from the arguments.

```python
class AgentFrameworkRunner(Protocol):
    def build_user_message(self, text: str) -> FrameworkMessage: ...
    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome: ...
    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult: ...
```

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
   usage)`. `new_messages` holds every item the turn produced after its user
   message, in order, across all of its model calls: model output, tool
   requests, tool results, and the final answer. The session appends them in
   one commit. Nothing is appended while tool calls run.
5. Report `usage` for this model call only. The session adds it to the session
   total.
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
attempt), `attempt`, `events`, and `heartbeat`. Run the real tool once and
return `ToolExecutionResult(output)`. Tool calls of one model response run in
parallel Steps, so this method may run concurrently with itself.

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
optional `interrupt_deadline`. A tool whose policy requires approval runs only
after `AwaitToolApproval` approves it.

## Flow shape

```text
StartAgentSession -> AwaitUserMessage -> RunModel
RunModel -> TurnCompleted -> AwaitUserMessage
RunModel -> ToolCallsRequested -> go_to_many(
              AwaitToolApproval(id) -> CallTool(id) or CallSingleAttemptTool(id)   per call needing approval
              CallTool(id) or CallSingleAttemptTool(id)                            per other call
              ResumeTurn(expected_result_count))
ResumeTurn -> RunModel
```

| Step (input) | What it does | Next |
| --- | --- | --- |
| `StartAgentSession` (`AgentSessionStartInput`) | Start Step. Initializes state, usage, and interrupts. | `RunModel` with a non-blank `initial_message`, else `AwaitUserMessage` |
| `AwaitUserMessage` (none) | Waits for `QueuedUserMessages.for_one()`, stores the user message, starts the turn. | `RunModel` |
| `RunModel` (none) | Calls `run_model_step`. 15 min timeout, 2 min heartbeat timeout, 3 attempts in 30 min, failure route `RecordTurnFailure`. On `ToolCallsRequested`, stores the run state, one `AgentToolCalls` record per call, and the interrupts. | `AwaitUserMessage`, or `go_to_many` with one Step per tool call plus `ResumeTurn` |
| `AwaitToolApproval` (tool call ID) | Waits for `InterruptResolutions.for_one(id)`, plus a Timer at the deadline, rounded up to whole seconds, while the interrupt is pending. Approved: marks the record `approved`. Rejected or expired: records `rejected` and publishes `ToolCallResults`. A Timer that fires before the deadline waits again. | `CallTool` or `CallSingleAttemptTool`, or ends |
| `CallTool` (tool call ID) | Runs one `read` or `idempotent_write` call. 3 attempts, failure route `RecordToolFailure`. Records `succeeded`, or `failed` for `ToolExecutionError`, and publishes `ToolCallResults`. | Ends |
| `CallSingleAttemptTool` (tool call ID) | Runs one `non_idempotent_write` or `unclassified` call. 1 attempt, failure route `RecordToolOutcomeUnknown`. | Ends |
| `RecordToolFailure` (tool call ID) | Records `failed` for a read and `outcome_unknown` for a write, with the recovery detail in `error`, and publishes `ToolCallResults`. | Ends |
| `RecordToolOutcomeUnknown` (tool call ID) | Records `outcome_unknown` with the recovery detail in `error`, and publishes `ToolCallResults`. | Ends |
| `ResumeTurn` (expected result count) | Waits for `ToolCallResults.for_n(count)`. | `RunModel` |
| `RecordTurnFailure` (none) | Records `last_turn_error`. When the turn has final tool call records, appends one turn-failure note; otherwise appends nothing. Clears the turn's records and run state. | `AwaitUserMessage` |

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
| `AgentEvents` | Stream `flow.agent_events`, one per Flow type | `AgentEvent` |

`AgentState.status` is `waiting_for_user_message`, `running_model`, or
`running_tools`.

RPCs:

| RPC | Input | Output | Notes |
| --- | --- | --- | --- |
| `send_user_message` | `AgentUserMessage` | `bool` | Transactional; `False` for blank text. |
| `resolve_interrupt` | `ResolveInterruptRequest` | `bool` | Locks `PendingInterrupts`; `False` when not pending or past its deadline; removes the interrupt and publishes the answer in one commit. A concurrent Step can raise `RpcLockConflictError`; `AgentSessionClient` retries it for 5 seconds. |
| `get_agent_snapshot` | none | `AgentSnapshot` | State, last 200 messages, pending interrupts, the active turn's tool calls, usage, queued message count. Serves retained closed sessions. |
| `list_agent_messages` | `ListAgentMessagesRequest` | `AgentMessagesPage` | Oldest first; limit clamped to 1..200. |

Events on `AgentEvents` (`AgentEventType`): `turn_started` `{"sequence"}`,
`turn_completed` `{"message_count"}`, `turn_failed` `{"error"}`, `text_delta`
and `reasoning_delta` `{"text"}`, `tool_call_started` `{"tool_call_id",
"tool_name"}`, `tool_call_finished` `{"tool_call_id", "tool_name", "status"}`
(once per tool call, including rejections), `tool_call_deferred`
`{"tool_call_id", "tool_name", "effect", "requires_approval"}`,
`interrupt_raised` `{"interrupt_id", "tool_call_id", "tool_name", "deadline"}`,
`interrupt_resolved` `{"interrupt_id", "approved", "rejection_message"}`, and
`usage_recorded` `{"recorded", "session"}`.

Every event carries `turn_id`, `step_execution_id` (shared by all attempts of
one Step execution), and the Step `attempt`. A UI groups live text by
`(turn_id, step_execution_id, attempt)`: within one `step_execution_id` it
keeps only the highest `attempt` seen and drops the partial text of lower
attempts. Different `step_execution_id` values of a turn are separate model
call and tool call Steps, and each keeps its text. After `turn_completed` or
`turn_failed`, replace the turn's live text with the stored messages. Streams
are best effort; rebuild from `get_agent_snapshot`.

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
- Nothing from a turn is stored until it completes. `TurnCompleted` carries all
  of the turn's messages; `ToolCallsRequested` keeps them in the framework run
  state.
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
  it ends.
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
