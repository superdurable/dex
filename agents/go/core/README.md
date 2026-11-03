# Dex Agents for Go

`github.com/superdurable/dex/agents/go/core` (package `dexagents`) is the Go
core of Dex Agents. It provides the durable agent session Flow,
`AgentSessionFlow`, the `AgentFrameworkRunner` contract, and Stepwise mode,
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
- Stepwise mode, with function tools;
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

Declare a Stepwise agent, build its Flow, and serve it with a Worker:

```go
lookupOrder, err := stepwise.NewFunctionTool(
	"lookup_order",
	"Look up an order by its ID.",
	json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`),
	dexagents.ToolEffectRead,
	func(ctx context.Context, arguments json.RawMessage, call stepwise.ToolCallContext) (string, error) {
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
definition, err := stepwise.NewAgentDefinition(
	"support",
	"You help customers with their orders.",
	openaimodel.NewResponsesModelClient(openai.NewClient(), "gpt-5-mini"),
	stepwise.WithFunctionTools(lookupOrder),
	stepwise.WithInterruptDeadline(24*time.Hour),
)
if err != nil {
	return err
}
flow, err := stepwise.NewStepwiseAgentSessionFlow(definition, "SupportAgent")
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

## Stepwise mode

`StepwiseAgentRunner` makes at most one model call per `RunModel`, from
committed state only. It stores every item as it appears: the assistant item
that asks for tools commits with the tool call records, and the tool results
commit with the next model call. Its input builder pairs every tool call with
a result, so an orphaned call never reaches a provider; a missing result is
the outcome-unknown text.

`NewAgentDefinition(name, instructions, model, options...)` options:

| Option | Default | Meaning |
| --- | --- | --- |
| `WithInstructionsFunction` | the fixed text | Instructions computed in every `RunModel` |
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

A `stepwise.ModelClient` makes exactly one request per `RunModel` attempt,
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
(default 10 MiB), `WithInterruptDeadline` (default none), and `WithLogger`.

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
ports), `KillableProcess` (a child process in its own process group, for
SIGKILL tests), `ServeAgentWorker`, and `ReadStepHistory`.

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
- snapshot and message paging;
- a Python Client and a TypeScript Client each running a session on a Go
  Worker, and a Go Client running a session on the Python core's Stepwise test
  Worker;
- a Go Step that lands in a turn a Python Worker with steering began, which
  fails the session with a reason instead of stalling;
- a heartbeat checkpoint carried across tool Step attempts, and no stale trace
  context on records;
- every wire value the Python core encodes, decoded and re-encoded byte for
  byte after canonicalization, plus event payloads, Stepwise framework items,
  the turn-failure note, function tool argument errors, and an Anthropic item
  that the Python core stores and replays with its thinking.

The model clients are tested against fake HTTP servers; no test needs a key.

## Wire format notes

- Go writes Attribute and RPC values with `encoding/json`, in contract field
  order, with `<`, `>`, and `&` escaped. Python writes sorted keys and ASCII
  only; TypeScript writes contract order and raw UTF-8. All three decode each
  other; the parity test compares Go's values with Python's after writing both
  in Python's canonical form.
- Event payload text, Stepwise framework items, Stepwise run state, and
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

- `RunModel` and `get_agent_snapshot` load the whole `AgentMessages` map.
- Each model call and tool call adds a Step round trip.
- Interrupt deadlines use the Worker clock, rounded up to whole seconds.
- A tool Step's first output narrows Dex's redelivery window but does not
  close it: a single-attempt tool can run twice if the Worker dies before its
  heartbeat reaches Dex. See the contract's Known limits.
- `EstimateInputTokens` of the provider clients counts four characters per
  token; the Go core does not compact, so nothing reads it today.
