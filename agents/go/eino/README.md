# github.com/superdurable/dex/agents/go/eino

Durable [CloudWeGo Eino](https://github.com/cloudwego/eino) agents on Dex. One
Flow execution is one agent session, and the Flow ID is the session ID.

You keep writing an Eino ADK `ChatModelAgent`: its model, tools, instruction,
`Handlers`, tool middlewares, and `GenModelInput`. You hand its
`*adk.ChatModelAgentConfig` to this package instead of to
`adk.NewChatModelAgent`, and the session Flow of the
[Go core](../core/README.md) runs it. Each model call is one `RunModel` Step,
each tool call is its own Step, and the tool calls of one model response run
in parallel. A crash repeats at most the one call that was running, a write
that needs approval waits for a person, and a write that may have run is never
run again blindly.

Package name: `dexeino`.

## Installation

```bash
go get github.com/superdurable/dex/agents/go/eino
```

The module needs Go 1.26 or later and `github.com/cloudwego/eino` v0.9.23 or
later. CI runs the tests against v0.9.23 and against the newest
v0.10.0 alpha (v0.10.0-alpha.35 when this was written). It depends on
`github.com/superdurable/dex/agents/go/core` at the same version, and on the
released Go SDK that the core requires. Model packages from `eino-ext` work as
they are; the tests use `github.com/cloudwego/eino-ext/components/model/openai`.
Connectors as tools use the core's package `connectortools`, which requires
`github.com/superdurable/dex-connectors-library/sdkgo` v0.39.0 or later.

The Go agents modules are not tagged yet. Until they are, depend on a commit
of `main`.

## Example

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
)

type orderRequest struct {
	OrderID string `json:"order_id" jsonschema:"description=The order ID"`
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey: os.Getenv("OPENAI_API_KEY"),
		Model:  "gpt-5-mini",
	})
	if err != nil {
		return err
	}
	lookupOrder, err := utils.InferTool("lookup_order", "Look up an order by its ID.",
		func(ctx context.Context, request orderRequest) (string, error) {
			return fmt.Sprintf("order %s shipped on Monday", request.OrderID), nil
		})
	if err != nil {
		return err
	}
	refundOrder, err := utils.InferTool("refund_order", "Refund one order.",
		func(ctx context.Context, request orderRequest) (string, error) {
			call, _ := dexeino.ToolCallContextFrom(ctx)
			// Pass call.IdempotencyKey to the payment API so a retry never refunds twice.
			return fmt.Sprintf("refunded order %s (key %s)", request.OrderID, call.IdempotencyKey), nil
		})
	if err != nil {
		return err
	}
	config := &adk.ChatModelAgentConfig{
		Name:        "support",
		Description: "Answers questions about orders.",
		Instruction: "You help customers with their orders. Refund only when asked.",
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{lookupOrder, refundOrder},
		}},
	}
	flow, err := dexeino.NewAgentSessionFlow(config, "SupportAgent",
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
			"lookup_order": dexagents.ToolEffectRead,
			"refund_order": dexagents.ToolEffectNonIdempotentWrite,
		}),
		dexeino.WithStreaming(true),
		dexeino.WithInterruptDeadline(24*time.Hour))
	if err != nil {
		return err
	}

	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return err
	}
	cache, err := blobcache.New(&blobcache.Config{Dir: ".dex-cache", MaxBytes: 64 << 20})
	if err != nil {
		return err
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: "localhost:8801"})
	if err != nil {
		return err
	}
	go func() {
		if err := worker.Start(); err != nil {
			log.Print(err)
		}
	}()

	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: "localhost:8801"})
	if err != nil {
		return err
	}
	defer client.Close()
	sessions := dexagents.NewAgentSessionClient(client, flow)
	initialMessage := "Please refund order 42"
	if _, err := sessions.StartSession(ctx, "session-42", &initialMessage, dex.StartFlowOptions{}); err != nil {
		return err
	}
	// Later, for example from a UI that polls the snapshot:
	snapshot, err := sessions.GetAgentSnapshot(ctx, "session-42")
	if err != nil {
		return err
	}
	for _, interrupt := range snapshot.PendingInterrupts {
		if _, err := sessions.ResolveInterrupt(ctx, "session-42", interrupt.InterruptID, true, nil); err != nil {
			return err
		}
	}
	return nil
}
```

`refund_order` is a `non_idempotent_write`, so each call waits for
`resolve_interrupt` and then runs at most once. Everything about the session
itself, such as messages, snapshots, `AgentEvents`, approvals, and session
settings, is the Go core's; see its README.

To choose the model or the instruction from the session settings, pass an
`AgentConfigBuilder` to `NewAgentSessionFlowWithBuilder`. It runs in every
`RunModel` attempt and every tool Step, with `AgentBuildContext.SessionSettings`
set for model calls:

```go
build := func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
	chosen, isAllowed := models[fmt.Sprint(build.SessionSettings["model"])]
	if !isAllowed {
		chosen = models["gpt-5-mini"]
	}
	return &adk.ChatModelAgentConfig{Name: "support", Description: "Answers questions about orders.",
		Instruction: "You help customers with their orders.", Model: chosen, ToolsConfig: toolsConfig}, nil
}
flow, err := dexeino.NewAgentSessionFlowWithBuilder(build, "SupportAgent", options...)
```

The builder also runs once at construction, with `BuildPurposeCheck`, so a
configuration the adapter refuses fails at startup. Keep the tools the same
for every session setting: tool Steps do not receive the settings.

## How a turn maps to Steps

A turn that looks an order up and answers runs these Steps:

```text
StartAgentSession -> RunModel -> CallTool(lookup_order) -> ResumeTurn -> RunModel
```

- **`RunModel`** builds a fresh Eino `ChatModelAgent` and `adk.Runner` from the
  current configuration and runs it over the session's stored messages, plus a
  tool result for each call of the turn whose result is not stored yet. The
  adapter puts its own Handler first in `Handlers`. That Handler turns every
  tool call into an Eino interrupt instead of running the tool, so the run
  stops after exactly one model call, and the interrupt names every call. The
  model's message is stored with the call records in one commit.
- **Each tool call** runs in its own Step: `CallTool` for `read` and
  `idempotent_write`, `CallSingleAttemptTool` after `AwaitToolApproval` for the
  others. The Step runs the call through a one-call `compose.ToolsNode` built
  from the agent's `ToolsNodeConfig`: its tools, `ToolAliases`,
  `ToolArgumentsHandler`, and `ToolCallMiddlewares`, then each
  `AgentMiddleware.WrapToolCall`, then the `Handlers`' tool wrappers, in Eino's
  order. `compose.GetToolCallID` and `ToolCallContextFrom` work inside the
  tool. A call of a tool source's tool runs through its source instead; see
  [Connectors as tools](#connectors-as-tools).
- **`ResumeTurn`** waits for every result, and the next `RunModel` makes the
  next model call. A final answer ends the turn.

Each `RunModel` is a new Eino run, so the instruction, the tools, and the
`Handlers` of the current deploy apply to every model call, including one in
the middle of a turn that a deploy interrupted. The `FrameworkRunState` holds
only the turn's model call count, `{"v":1,"model_calls":N}`.

## Effects and policies

Every tool needs a Dex effect. Eino tools carry none, so give each one with
`WithToolEffects`, `WithToolPolicies`, a `DexToolEffect` method on a tool type
you own (`ToolEffectDeclarer`), or an explicit `WithDefaultToolEffect`.
Construction fails for a tool with none. A policy wins over the other sources
and may set `RequiresApproval`.

| Effect | Step | Retry | Crash mid-call | Default approval |
| --- | --- | --- | --- | --- |
| `read` | `CallTool` | Up to 3 attempts; then `failed` | Runs again | No |
| `idempotent_write` | `CallTool` | Up to 3 attempts, same `IdempotencyKey`; then `outcome_unknown` | Runs again with the same key | No |
| `non_idempotent_write` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |
| `unclassified` | `CallSingleAttemptTool` | One attempt; then `outcome_unknown` | Outcome unknown | Yes |

A tool that a Handler adds in `BeforeAgent`, such as a filesystem or plan
middleware tool, is not known at construction. It gets its effect from
`WithToolEffects` or `WithToolPolicies` by name, or else the default effect,
`unclassified` unless you set one. Pass policies and the default effect with
this package's options, not through `WithSessionOptions`: the adapter reads
them, and the session receives them too.

A tool reports a failure that settles the call by returning a
`*dexagents.ToolExecutionError`, directly or wrapped in its own error type. The
model sees `The tool call failed: <message>`, and the call is not retried. Any
other error fails the Step attempt, so the session decides the outcome from the
call's effect: a `read` retries, and a write that may have run records
`outcome_unknown`. Return `ToolExecutionError` only for an answer that settles
the call, such as invalid arguments or a refusal before anything changed.

`utils.WrapToolWithErrorHandler` turns every tool error into ordinary result
text. Under Dex that hides a crash as a success, so a write that may have run
would read as succeeded. Do not wrap Dex tools with it.

## Connectors as tools

`WithToolSources` offers the tools of a `dexagents.ToolSource`, such as a
`connectortools.ConnectorToolSource` of Dex Connector operations, beside the
agent's own tools:

```go
agentTools, err := slackConnection.AgentTools()
if err != nil {
	return err
}
tools := []*connectortools.ConnectorTool{}
for _, agentTool := range agentTools {
	if agentTool.Metadata().ToolName != "slack_post_thread_reply" {
		continue
	}
	tool, err := connectortools.NewConnectorTool(agentTool)
	if err != nil {
		return err
	}
	tools = append(tools, tool)
}
source, err := connectortools.NewConnectorToolSource(tools)
if err != nil {
	return err
}
flow, err := dexeino.NewAgentSessionFlow(config, "SupportAgent",
	dexeino.WithToolEffects(effects),
	dexeino.WithToolSources(source))
```

How a source tool works with Eino:

- **Offered as Eino tools.** Every `RunModel` lists each source, waiting at
  most `WithToolSourceListingTimeout` (10 seconds by default), and offers the
  listed tools after the agent's own, with their names, descriptions, and
  argument schemas. Their bodies never run in `RunModel`: the adapter's gate
  stops the run at their calls like any other. A source whose listing fails or
  takes longer offers no tools in that model call, and the turn goes on.
- **Built by the source.** The source picks each call's effect, approval, and
  write ordering, and stores its own reference with the call. A session
  `ToolPolicy` for the tool name reaches the source, so it may change the
  approval or a connector's send cap. The connector effects and Steps are in
  the Go core's [Connectors as tools](../core/README.md#connectors-as-tools)
  table: a Query is a `read` in `CallTool`, a Mutation with `IsRetrySafe` an
  `idempotent_write` in `CallTool`, and any other Mutation a
  `non_idempotent_write` in `CallSingleAttemptTool`.
- **Run by the source, outside Eino.** The tool Step routes the call to its
  source first, once per attempt, with the Step's `DeferredToolExecution`. No
  `compose.ToolsNode`, `ToolCallMiddlewares`, `AgentMiddleware.WrapToolCall`,
  or Handler tool wrapper runs around it, and the `AgentConfigBuilder` is not
  called, so an Eino retry or error handler can never send a connector write
  twice or turn an unknown outcome into a success. The call's tool Step gets
  the operation's timeout and retry policy through `ToolCallStepOptions`.
- **Names.** Construction fails when two sources share a `SourceName` or a
  name a source declares repeats an agent tool or a tool alias. A name that
  neither the agent nor a listing offers is deferred as `unclassified` without
  an approval, as in the built-in loop, and its tool Step answers
  `unknown tool <name>`.

## Integration mode

Step mode, by stateless replay. No Eino `CheckPointStore` is used, and no run
is resumed: every `RunModel` is a new `Runner.Run` over the Dex records. This
is the same shape as the Go built-in agent loop, so a deploy that renames or
removes a tool, or changes the instruction, never breaks an open turn.

| Stop after one model call | Run state | Feed tool results back |
| --- | --- | --- |
| The adapter's first Handler turns every tool call, and every unknown tool, into `tool.Interrupt`; the run ends with one `Interrupted` event that names every call | `{"v":1,"model_calls":N}` | The next `RunModel` replays the stored messages plus one `role=tool` message per final record |

Details that follow from it:

- **Tool call IDs.** An empty ID, an ID with `/`, or an ID already used in the
  turn is replaced with `<StepExecutionID>-<index>` before the message is
  stored, so a retry of the same attempt stores the same IDs. Other IDs are
  kept.
- **Unknown tools.** The adapter sets `UnknownToolsHandler`. A tool the model
  invents, or one a deploy renamed or removed, is deferred like a read (as
  `unclassified` without an approval when the agent has tool sources), and
  its tool Step answers `unknown tool <name>`, so the model sees why. On a later
  attempt of a write, the core records `outcome_unknown`, because the first
  attempt may have run under the old name.
- **Stricter effects.** A tool Step does not run a call whose record has a
  weaker effect than this deploy gives the tool, such as a name the model
  invented that a later deploy added as a write. The model sees why and may
  call it again under the new policy.
- **One model call.** A Handler or model wrapper that calls the model a second
  time ends the turn as failed before the second call reaches the model. The
  one call that ran is billed, and no retry pays for it again.
- **Turns that end as failed.** A stop that a retry would repeat ends the turn
  at once with `TurnCompleted.TurnError`: the second model call above, a turn
  at `MaxIterations`, an Eino interrupt the adapter did not raise, tool calls
  that a Handler changed after the model call, and a configuration from the
  `AgentConfigBuilder` that construction would refuse. The session sets
  `LastTurnError` to the reason and writes `turn_failed`; the next turn that
  completes clears it. The tool results the turn has not stored yet are
  stored, so every stored call keeps its result. The model message of the
  refused call is not stored.
- **Model errors.** The Go core reads the HTTP status of a failed model call
  from a `StatusCode`, `Response`, or `HTTPStatusCode` field, so go-openai and
  Ark errors reach it unchanged. The adapter's outermost model wrapper wraps an
  error in `ModelAPIError` only where the core would miss something: the
  status in the `Code` of genai errors, or response headers that an error with
  `HTTPStatusCode` carries. The core then backs off a 429 or 5xx, honoring
  `Retry-After`, and ends the turn at once on another 4xx.
- **Streaming.** With `WithStreaming(true)` the model call streams, and its text
  and reasoning arrive as `text_delta` and `reasoning_delta` events. Without it
  the model call uses `Generate`, and the whole text is one delta.
- **Usage.** `ResponseMeta.Usage` maps to the session's usage: prompt, completion,
  cached prompt, and reasoning tokens.
- **Turn limit.** `MaxIterations` (Eino's default 20) caps the model calls of one
  turn, as it caps one Eino run. A turn at the cap ends as failed with
  `TurnCompleted.TurnError` before another model call.

## Messages

Each stored `framework_item` is `{"type":"eino_message","format":1,"message":{...}}`,
the Eino `schema.Message` as `encoding/json` writes it. Extra keys that Eino
ADK sets for itself, such as `_eino_msg_id`, are dropped, so a retried model
call stores the same bytes. Only user, assistant, and tool messages are
stored; the instruction is rebuilt for every model call. `EncodeMessage` and
`DecodeMessage` read and write these items.

The model's message is stored with the call records, and the tool results with
the next model call. When a turn fails, the session stores the results that
are not stored yet before the turn-failure note, so the conversation pairs
every call with its result. A call without a stored result, such as one whose
turn an older Worker ended, is answered with the unknown-outcome text in the
model input only.

Messages another runner stored, such as the built-in agent loop, replay as
Eino text and tool calls. A message of another framework keeps its text and
its single tool call or result, when it has one.

## What replaces Eino's features

| Eino feature | Under Dex |
| --- | --- |
| `adk.Runner`, `TurnLoop`, `CheckPointStore`, `WithCheckPointID`, `ResumeWithParams` | The session Flow. Dex stores the messages and resumes from them |
| Human-in-the-loop `tool.Interrupt` and `compose.Interrupt` | Dex approvals: give the tool `RequiresApproval` or a write effect, and answer with `resolve_interrupt`. An interrupt the adapter did not raise ends the turn as failed |
| `ModelRetryConfig` | `RunModel` retries; tune it with `WithModelRetryPolicy`. Refused at construction |
| `ModelFailoverConfig` | Choose the model per call with an `AgentConfigBuilder`. Refused at construction |
| Sub-agents and `transfer_to_agent`, supervisor, plan-execute | Not supported: they make several model calls in one Step. Wrap a sub-agent as a tool instead |
| `ToolsConfig.ReturnDirectly`, `Exit` | Refused at construction |
| Summarization middleware | Refused at construction: it makes its own model call |
| `UnknownToolsHandler` | Replaced by the adapter's, which answers `unknown tool <name>` |
| Eino's tool event sender | `AgentEvents`; the session writes tool call events |

## Moving an existing Eino agent

1. Pass the `*adk.ChatModelAgentConfig` to `NewAgentSessionFlow` instead of
   building an agent and a `Runner`. Drop `CheckPointStore`, `TurnLoop`, and
   `WithCheckPointID`.
2. Give every tool an effect.
3. Remove `ModelRetryConfig` and `ModelFailoverConfig`. Turn off retries in the
   model client where it has them: set Ark's `RetryTimes` to 0. The eino-ext
   Claude client retries twice inside its SDK and has no setting to stop it, so
   one `RunModel` attempt may send up to three requests.
4. Replace Eino human-in-the-loop interrupts with Dex approvals.
5. Return `*dexagents.ToolExecutionError` for definite tool failures, and stop
   using `utils.WrapToolWithErrorHandler`.

## Limits

- **One model call per `RunModel`.** Configurations that make more refuse to
  build. An `adk.NewAgentTool` sub-agent runs its whole inner agent inside one
  tool Step, at least once, so give it the effect of the inner agent's tools.
- **Run-local state resets every Step.** Each `RunModel` is a new Eino run, so
  `SetRunLocalValue` values, session values, and middleware caches, such as the
  tool search, skill, and agents.md middlewares keep, do not carry from one
  model call to the next. A tool wrapper that reads agent run state, such as
  `compose.ProcessState`, fails in a tool Step.
- **Session values.** The adapter passes no `adk.WithSessionValues`, so the
  default `GenModelInput` does not format the instruction.
- **Message fidelity.** Stored messages round-trip through JSON. Replay fields
  that providers need, such as reasoning signatures, survive. Typed Go values in
  `Extra` come back as JSON maps.
- **Tools must be safe for concurrent use**, because the calls of one model
  response run as parallel Steps, possibly in one Worker.
- **Go core gaps.** The adapter runs on the Go core, which has no steering or
  `cancel_turn`, questions or tool input requests, compaction, the MCP tool
  source, tool tasks, images, plans, GenAI spans, `ModelCallMetadata`, or an
  approval display for connector calls. Serve each agent Flow type with
  Workers of one language, and follow the Go core's upgrade rule for tool
  sources.

## Testing

`ci.sh` checks formatting, `go vet`, and public API docs, then runs the unit
tests and the integration tests against a private `dexcli dev` engine with
scripted Eino models, so no model key is needed. The connector integration
tests use a test Connector whose operations run behind real
`sdkgo.AgentTool`s. The integration tests run
Workers as child processes and kill them to test crashes. It runs the suite a
second time against the newest Eino v0.10.0 alpha in a temporary copy of the
module, with build tag `eino_v010`.

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```
