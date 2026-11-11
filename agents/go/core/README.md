# Dex Agents for Go

`github.com/superdurable/dex/agents/go/core` (package `dexagents`) is the Go
core of Dex Agents. It provides the durable agent session Flow,
`AgentSessionFlow`, the `AgentFrameworkRunner` contract, and the built-in agent loop,
the built-in agent loop, with model clients on the official OpenAI and
Anthropic Go SDKs. The contract is
[agent-framework-adapters.md](https://github.com/superdurable/dex/blob/main/docs/design/plan/agent-framework-adapters.md),
the same one the Python core (`dex-agents`) and the TypeScript core
(`@superdurable/dex-agents`) implement.

One Flow execution is one agent session; the Flow ID is the session ID. Every
model call is one `RunModel` Step, and every tool call, READ included, is its
own Step. The tool call Steps of one model response run in parallel, and
`ResumeTurn` waits for their results before the next model call. A crash
repeats at most the one call that was running.

The Step types, persistence names, RPC names, event types, and wire values are
the shared contract. A Python or TypeScript Client drives a session that Go
Workers serve, and a Go Client drives a session that Python Workers serve.
The integration tests check these directions against a real Dex engine.

## Scope

The Go core is a subset of the Python and TypeScript cores, and is meant to
stay one.

Supported:

- the session core: the session Flow and its Steps, persistence, RPCs, and the
  `AgentEvents` Stream;
- approvals and approval deadlines;
- effect classes, including single-attempt tools;
- the turn-failure note;
- incremental turn messages (A1: `ToolCallsRequested.NewMessages` and
  `UnfinishedTurnMessageBuilder`);
- session settings and `update_session_settings`, with the built-in loop model
  resolver;
- the built-in agent loop, with function tools;
- the OpenAI (`openai-go`) and Anthropic (`anthropic-sdk-go`) model clients.

Not planned for Go: questions (`ask_user`, `answer_question`, tool input
requests), `durable_wait`, steering and cancellation, plans and plan mode,
compaction, tool tasks, the MCP tool source, GenAI spans, UI streams, and
framework adapters. Use the Python or TypeScript core for an agent that needs
any of them.

### One language per Flow type

Serve each agent Flow type with Workers of one language. Dex routes every
Step to any Worker that registers it, so in a fleet of Go and Python or
TypeScript Workers, a Go Step can run inside a turn that a Worker with
steering began. Go Steps do not wait on steering or cancellation, or apply
plan mode or compaction, so such a session could stall or run a tool that the
turn forbids.

Each Go Step therefore checks the session first. When it finds state that
needs an addition this core lacks, it fails the session at once with a
`ForceFail` reason that names the state, instead of acting on it:

- a turn that accepts steering and cancellation (`accepts_turn_control`);
- a `planning` or `executing` turn;
- a `cancel_turn` request for the active turn;
- a `draft` or `active` plan, or a pending plan execution;
- a context summary.

A Go-only fleet never writes any of these. Keep an existing Python or
TypeScript session that used steering, plan mode, or compaction on Workers of
its original language: moved onto Go Workers, the guard fails it.

The Flow registers every
persistence name of the contract, so `get_agent_snapshot` and the Clients of
every core read every value.

## Installation

```bash
go get github.com/superdurable/dex/agents/go/core
```

The module needs Go 1.26 or later. It serves the contract's snake_case RPC
names, such as `send_user_message`, through `dex.RPCOptions.Name`, which the
Go SDK gained after `sdk-go/v1.32.0`. Until a release contains it, `go.mod`
requires a pseudo-version of that change.

## Running an agent

Declare a built-in loop agent, build its Flow, and serve it with a Worker:

```go
lookupOrder, err := builtinloop.NewFunctionTool(
	"lookup_order",
	"Look up an order by its ID.",
	json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`),
	dexagents.ToolEffectRead,
	func(ctx context.Context, arguments json.RawMessage, call builtinloop.ToolCallContext) (string, error) {
		var input struct {
			OrderID string `json:"order_id"`
		}
		if err := json.Unmarshal(arguments, &input); err != nil {
			return "", err
		}
		return orders.Describe(ctx, input.OrderID)
	},
)
if err != nil {
	return err
}
definition, err := builtinloop.NewAgentDefinition(
	"support",
	"You help customers with their orders.",
	openaimodel.NewResponsesModelClient(openai.NewClient(), "gpt-5-mini"),
	builtinloop.WithFunctionTools(lookupOrder),
	builtinloop.WithInterruptDeadline(24*time.Hour),
)
if err != nil {
	return err
}
flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, "SupportAgent")
if err != nil {
	return err
}
registry, err := dex.NewRegistry([]dex.Flow{flow})
if err != nil {
	return err
}
worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: "localhost:8801"})
if err != nil {
	return err
}
go func() { serveErrors <- worker.Start() }()
```

Call a session through `AgentSessionClient`, from any process whose Registry
holds the same Flow type:

```go
sessions := dexagents.NewAgentSessionClient(client, flow)
initialMessage := "Where is order 7?"
if _, err := sessions.StartSession(ctx, "session-42", &initialMessage, dex.StartFlowOptions{}); err != nil {
	return err
}
snapshot, err := sessions.GetAgentSnapshot(ctx, "session-42")
if err != nil {
	return err
}
for _, interrupt := range snapshot.PendingInterrupts {
	if _, err := sessions.ResolveInterrupt(ctx, "session-42", interrupt.InterruptID, true, nil); err != nil {
		return err
	}
}
```

`ResolveInterrupt` retries an RPC lock conflict for up to 5 seconds
(`WithLockConflictRetryTimeout`). `ReadAgentEvent` reads the `AgentEvents`
Stream from a resume token; events are best effort, so rebuild from
`GetAgentSnapshot` after a gap.

## Built-in agent loop

`BuiltinLoopAgentRunner` makes at most one model call per `RunModel`, from
committed state only. It stores every item as it appears: the assistant item
that asks for tools commits with the tool call records, and the tool results
commit with the next model call. Its input builder pairs every tool call with
a result, so an orphaned call never reaches a provider; a missing result is
the outcome-unknown text.

`NewAgentDefinition(name, instructions, model, options...)` options:

| Option | Default | Meaning |
| --- | --- | --- |
| `WithInstructionsFunction` | the fixed text | Instructions computed in every `RunModel`; `InstructionContext.SessionSettings` carries the turn's settings |
| `WithModelResolver` | the `model` argument | The model client of each model call, chosen from the turn's session settings |
| `WithModelSettings` | `ModelSettings{}` | Temperature, maximum output tokens, reasoning effort, and provider options |
| `WithFunctionTools` | none | Function tools, with unique names |
| `WithToolPolicies` | none | `dexagents.ToolPolicy` by tool name, overriding a tool's own effect and approval |
| `WithDefaultToolEffect` | `unclassified` | Effect of a tool without a policy |
| `WithMaxModelCallsPerTurn` | 100 | After this many model calls in a turn, the next call offers no tools |
| `WithInterruptDeadline` | none | Deadline for approvals |
| `WithEventStreamCapacityBytes` | 10 MiB | `AgentEvents` capacity |

Function tools come from `NewFunctionTool`, with a JSON Schema and a function
of the raw JSON arguments, or `NewTypedFunctionTool`, which decodes the
arguments into a Go type. Each declares its effect; `WithRequiresApproval`
overrides the effect's default approval. Before each call the arguments are
checked against the JSON Schema subset the Python core checks (`type`, `enum`,
`anyOf`, `properties`, `required`, `additionalProperties`, `items`,
`minimum`, `maximum`), with the same model-visible messages, such as
`invalid arguments for refund_order: arguments.amount must be at least 1`.
`ToolCallContext` gives the tool its idempotency key
`<flowId>/<turnId>/<toolCallId>`, attempt, event writer, and heartbeat.

### Model clients

A `builtinloop.ModelClient` makes exactly one request per `RunModel` attempt,
with no retries of its own, sends the whole input every time, and keeps each
output item as the provider sent it.

| Package | Client | Provider and API on stored items |
| --- | --- | --- |
| `openaimodel` | `NewResponsesModelClient(openai.Client, model)`: one streaming Responses API request with `store: false`, `include: ["reasoning.encrypted_content"]`, and automatic reasoning summaries | `openai`, `responses`, the same as the Python core's LiteLLM client for OpenAI models |
| `anthropicmodel` | `NewMessagesModelClient(anthropic.Client, model)`: one streaming Messages API request; thinking blocks keep their signatures | `anthropic`, `chat_completions`: the LiteLLM chat message the Python core stores, byte for byte; it also replays the TypeScript core's `ai-sdk` items with their reasoning signatures |
| `agentstest` | `NewScriptedModelClient(respond)`: answers from a script, for tests without a model key | `dex-scripted`, `scripted`, byte for byte the Python core's scripted items |

An item replays unchanged to a client with the same provider and API, and as
its text and tool calls to any other, so a session can change model between
turns. An Anthropic response with no text, tool call, or signed thinking
stores no item, because the API rejects empty assistant content on every later
request. Credentials come from the provider client, which reads
`OPENAI_API_KEY` or `ANTHROPIC_API_KEY` by default, never from Flow input.
`ModelSettings.ProviderOptions` set top-level request fields last. The
Anthropic client sends `max_tokens` 4096 unless `MaxOutputTokens` is set, and
takes extended thinking through provider options.

### Session settings

Session settings let one Flow type serve sessions that differ, such as by
model. They are one JSON object, `map[string]any`, set at start and replaced
with `update_session_settings`:

```go
runID, err := sessions.StartSessionWithSettings(ctx, "session-42", nil,
	map[string]any{"model": "gpt-5"}, dex.StartFlowOptions{})
isStored, err := sessions.UpdateSessionSettings(ctx, "session-42", map[string]any{"model": "gpt-5-mini"})
```

`StartAgentSession` stores them in the `SessionSettings` Attribute; an empty
map clears them, and a nil map makes the RPC return false. A change takes
effect when the next turn starts: the Step that starts a turn copies them into
`TurnSessionSettings`, tagged with the turn, and the turn's model calls and
retries read only that copy; a copy that an earlier turn left reads as no
settings. Runners read `AgentTurnContext.SessionSettings`, an empty map when
there are none, and `WithModelResolver` maps them to a model client the Worker
built. Only model calls see them; tool calls do not. `get_agent_snapshot`
returns the next turn's settings, not the running turn's.

They are Flow state that `get_agent_snapshot` returns, so never put a secret,
or anything a browser must not see, in them; put a reference that the Worker
resolves. An error from the resolver becomes the turn's `LastTurnError`, which
snapshots show, so keep the settings out of its message. Their compact JSON
text is at most `MaxSessionSettingsBytes` (16 KiB); `ValidateSessionSettings`
checks it, and `StartSessionWithSettings` and `UpdateSessionSettings` call it.
Go escapes U+2028 and U+2029 and the Python core writes floats in shortest
form, so the cores' sizes can differ by a few bytes; the session's own checks
accept `SessionSettingsSizeToleranceBytes` (1 KiB) more, and a start beyond
that fails the session at once, with no retry, with a reason that starts with
`InvalidStartSessionSettingsReason`. Start sessions with settings only after
every Worker of the Flow type runs 0.2.0. JSON numbers decode as `float64`.

## The runner contract

A framework adapter implements `AgentFrameworkRunner`:

```go
type AgentFrameworkRunner interface {
	BuildUserMessage(text string) (FrameworkMessage, error)
	RunModelStep(ctx context.Context, turn AgentTurnContext) (AgentTurnOutcome, error)
	ExecuteDeferredToolCall(ctx context.Context, call DeferredToolExecution) (DeferredToolOutcome, error)
}
```

- `RunModelStep` makes at most one model call and returns `*TurnCompleted` or
  `*ToolCallsRequested`, with one `DeferredToolCall` per tool call and its
  policy from `turn.ResolveToolPolicy`. A runner that sets
  `ToolCallsRequested.NewMessages` implements `UnfinishedTurnMessageBuilder`;
  `RecordTurnFailure` stores what it returns before the turn-failure note.
- `ExecuteDeferredToolCall` runs one tool, possibly concurrently with others.
  A `*ToolExecutionError` records `failed` with its message and is not
  retried; any other error fails the Step attempt.
- When a tool Step's attempts fail and its recovery error's `AttemptProgress`
  is `dex.StepAttemptProgressNotStarted`, no attempt reached a Worker and the
  tool never ran. `RecordToolFailure` and `RecordToolOutcomeUnknown` then
  record `failed` with an `Error` that starts with `ToolCallNotRunErrorPrefix`
  (`the tool did not run, so it is safe to call again`), then `": "` and the
  recovery detail, so the model may call the tool again. Any other progress
  records `failed` for a read and `outcome_unknown` otherwise.
- `DeferredToolOutcome` is sealed and has one result, `*ToolExecutionResult`.
- `DeferredToolExecution.HeartbeatCheckpoint` is the checkpoint an earlier
  attempt saved with `SaveHeartbeatCheckpoint`. Every heartbeat of a tool Step
  repeats the latest checkpoint, as in the Python core, because a bare
  heartbeat would clear it.

## Effects and policies

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, key `<flowId>/<turnId>/<toolCallId>`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

`NewAgentSessionFlow(runner, flowType, options...)` takes `WithToolPolicies`,
`WithDefaultToolEffect` (default `unclassified`), `WithEventStreamCapacityBytes`
(default 10 MiB), `WithInterruptDeadline` (default none),
`WithMessageWindowSize` (default 64), `WithMessageChunkSize` (default 32),
`WithMessageChunkMaxBytes` (default 1 MiB), and `WithLogger`. `builtinloop.NewBuiltinAgentSessionFlow` accepts the same options
after the Flow type.

## Message storage

**Upgrade rule: stop every 0.1.0 Worker of a Flow type before any Worker of
this release starts.** A 0.1.0 Worker that runs a session after this release
moved its older messages into chunks fails that session's turns, because it
reads every message from `AgentMessages`.

Messages live in four persistence names, the same in every core:

| Name | Kind | Holds |
| --- | --- | --- |
| `AgentMessages` | AttributeMap, key `MessageSequenceKey(sequence)` | The window: the newest messages, one instance per message |
| `AgentMessageChunks` | AttributeMap, key `MessageSequenceKey(first sequence)` | Archived messages, written once |
| `AgentMessageIndex` | Attribute | `chunks` (each kept chunk's `first_sequence` and `last_sequence`), `first_sequence`, and `window_first_sequence`; written only when a chunk is added or deleted |
| `TurnReplySequence` | Attribute | `turn_id` and `sequence`: where the latest turn's replies start, written when a turn starts |

Every Step that stores messages loads the whole window. When the window holds
more than `WithMessageWindowSize` messages, its oldest messages move into
chunks in the same commit. A chunk takes messages until it holds
`WithMessageChunkSize` messages or the next one would carry its JSON encoding
past `WithMessageChunkMaxBytes`, so no chunk outgrows server-zd's
`values.maxValueBytes`. A message whose own chunk, 15 bytes larger than the
message, would pass that cap skips the window: the Step that stores it writes
it straight into a single-message chunk. A message too large for
`values.maxValueBytes` therefore fails the commit that first stores it, and
the turn fails, instead of a later rollover failing on it. One Step moves at
most `MessageRolloverMaxChunksPerCommit` (4) chunks and
`MessageRolloverMaxBytesPerCommit` (8 MiB) of them, so when one commit stores
many messages the window can briefly hold more than its size. An append writes
only the new message, so no larger value is rewritten, and an append with no
messages writes nothing.

The index changes only when a chunk is added or deleted, about once per chunk
of messages, so an ordinary turn does not rewrite its growing chunk list. Turn
events count a turn's replies from `TurnReplySequence`; when it names another
turn, as for a turn a 0.1.0 Worker started, the core counts from the stored
messages and records it before rolling them over.

The Go core has no compaction, so its model input is every stored message:
`RunModel`, `get_agent_snapshot`, and `list_agent_messages` load every chunk.
The window bounds what each append writes and reloads; the conversation still
grows with the session. Retention, which deletes summarized chunks, needs a
context summary and runs only in the Python and TypeScript cores. A Go Worker
that meets a session whose older messages a Python or TypeScript Worker
deleted logs a warning, because its runner cannot use the summary that covers
them. `AgentMessagesPage.DeletedBeforeSequence` names the oldest kept message
when retention deleted earlier ones.

A session without an index, a new one or one that a dex-agents 0.1.0 Worker
wrote, keeps every message in `AgentMessages`. This release reads it as it is. Each Step that
stores a message moves up to four chunks of its older messages, so a long
history migrates over several commits and reads stay whole meanwhile.

## Flow shape

```text
StartAgentSession -> AwaitUserMessage -> RunModel
RunModel -> TurnCompleted -> AwaitUserMessage
RunModel -> ToolCallsRequested -> GoToMany(
              AwaitToolApproval(id) -> CallTool(id) or CallSingleAttemptTool(id)   per call needing approval
              CallTool(id) or CallSingleAttemptTool(id)                            per other call
              ResumeTurn(expected result count))
CallTool(id) -> failure route RecordToolFailure(id)
CallSingleAttemptTool(id) -> failure route RecordToolOutcomeUnknown(id)
ResumeTurn -> RunModel
RunModel -> failure route RecordTurnFailure -> AwaitUserMessage
```

Every Step sets SYNC durability for its WaitFor and Execute. Each tool Step
and its failure route take the tool call ID as input and load only that
call's record. A tool Step writes `tool_call_started` and awaits a heartbeat
before it calls the tool. Every final record publishes one
`ToolCallResults` message in the commit that makes it final.
`dexcli visualize session_flow.go --schema-version 2.0` renders this graph;
its one blocking diagnostic, `dynamic_type_name`, comes from the Flow type
being chosen per agent at construction.

## Testing

`agentstest` has what the Python core's `dex_agents.testing` has:
`ScriptedModelClient`, `PrivateDexEngine` (a `dexcli dev` engine on free
ports, with an optional dex-server-zd configuration file), `KillableProcess` (a child process in its own process group, for
SIGKILL tests), `ServeAgentWorker`, and `ReadStepHistory`. `PrivateDexEngine`
runs with `dev.durabilityMode: processCrashSafe`, which skips fsync and
survives a process kill. A configuration file of your own replaces that
setting, so set `dev.durabilityMode` there too.

`./ci.sh` with `DEXCLI` set to the `dexcli` binary runs vet, gofmt, the public
API documentation check, and every test with the race detector. The
integration tests in `integ/` start a private engine per test and run Workers
as children of the test binary:

- a turn with a read tool, and the next turn replaying the history;
- an approval that survives a Worker restart and runs once, a rejection, and
  an approval deadline;
- SIGKILL during `CallSingleAttemptTool` (`outcome_unknown`) and during
  `RunModel` (only that model call reruns);
- the turn-failure note after a write ran;
- snapshot and message paging, including pages across window and chunk
  boundaries;
- rollover into many chunks with a SIGKILL during the `RunModel` whose commit
  rolls over, chunks closed by the byte cap and an oversized message stored
  straight into its own chunk, a message too large for `values.maxValueBytes`
  failing the turn that first stores it while the next turn completes, the
  index written only by Steps that add a chunk, a 50-message session in the
  0.1.0 layout migrating four chunks per commit, and a 60-turn session whose
  window never exceeds its size;
- a Python Client and a TypeScript Client each running a session on a Go
  Worker, and a Go Client running a session on the Python core's built-in loop test
  Worker;
- a Go Step that lands in a turn a Python Worker with steering began, which
  fails the session with a reason instead of stalling;
- a heartbeat checkpoint carried across tool Step attempts, and no stale trace
  context on records;
- every wire value the Python core encodes, decoded and re-encoded byte for
  byte after canonicalization, plus event payloads, built-in loop framework items,
  the turn-failure note, function tool argument errors, and an Anthropic item
  that the Python core stores and replays with its thinking.

The model clients are tested against fake HTTP servers; no test needs a key.

## Wire format notes

- Go writes Attribute and RPC values with `encoding/json`, in contract field
  order, with `<`, `>`, and `&` escaped. Python writes sorted keys and ASCII
  only; TypeScript writes contract order and raw UTF-8. All three decode each
  other; the parity test compares Go's values with Python's after writing both
  in Python's canonical form.
- Event payload text, built-in loop framework items, built-in loop run state, and
  model-visible texts are byte for byte the Python core's, through an internal
  writer that reproduces `json.dumps`.
- Times are UTC with milliseconds and `Z`; readers accept the `+00:00` form.

## Design decisions

- The Steps, persistence definitions, and every Step decision live in
  `session_flow.go`, so the Flow analyzer sees the whole graph. Helpers commit
  state and return what the Step decides from.
- RPC methods have Go names (`SendUserMessage`) and contract protocol names
  (`send_user_message`) through `dex.RPCOptions.Name`.
- `RunModel` and `RecordTurnFailure` take `*CompactContextInput`, always nil
  from this core, because a Python Registry requires a failure route to share
  its Step's input type.
- The event writer writes each text delta as one event, as Python does;
  TypeScript batches small deltas.
- Without tracing, tool call records carry no trace context, even when
  another Worker left one in `AgentTraceContext`.

## Limits

- Without compaction, `RunModel`, `get_agent_snapshot`, and
  `list_agent_messages` load every `AgentMessageChunks` instance, and the
  stored conversation grows with the session.
- Each model call and tool call adds a Step round trip.
- Interrupt deadlines use the Worker clock, rounded up to whole seconds.
- Dex never hands a tool Step's attempt number to a second Worker, so a
  single-attempt tool does not run twice after a Worker crash. A cancelled run
  may still overlap its failure route. See the contract's Known limits.
- `dex.StepAttemptProgressNotCompleted` is conservative, so a call whose tool
  never ran can still record `outcome_unknown`, such as when Dex Server
  crashed after it recorded the attempt start but before the task reached the
  Worker.
- `EstimateInputTokens` of the provider clients counts four characters per
  token; the Go core does not compact, so nothing reads it today.
