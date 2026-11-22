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

Each agents release tags this module as `agents/go/eino/v<version>`, starting
with 0.6.16, on the same commit as the Go core tag it requires. Install a
tagged version. Go ignores the module's `replace` of the core when you depend
on it, so a commit of `main` between releases builds against the core release
that its `go.mod` names, not against the core on `main`: it may fail to
compile, or run with the older core's behavior.

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
  order. Before any tool middleware runs, the Step checks the arguments, as
  Eino converted them, against the tool's current schema.
  `compose.GetToolCallID` and `ToolCallContextFrom` work inside the tool. A
  call of a tool source's tool runs through its source instead; see
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
middleware tool, or a tool search middleware tool that the model is offered
as a deferred tool, is not known at construction. It gets its effect from
`WithToolPolicies` or `WithToolEffects` by name, then from its own
`DexToolEffect` method, or else the default effect, `unclassified` unless you
set one. To find the tool, the adapter calls the Handlers' `BeforeAgent`
again outside the Eino run: in `RunModel` when the model calls such a tool,
to read its effect, and in its tool Step, to run it. Pass policies and the
default effect with this package's options, not through `WithSessionOptions`:
the adapter reads them, and the session receives them too. A tool Step
compares the call's record with them, so a policy that only the session
knows could stop its calls from running.

A tool reports a failure that settles the call by returning a
`*dexagents.ToolExecutionError`, directly or wrapped in its own error type. The
model sees `The tool call failed: <message>`, and the call is not retried. Any
other error fails the Step attempt, so the session decides the outcome from the
call's effect: a `read` retries, and a write that may have run records
`outcome_unknown`. Return `ToolExecutionError` only for an answer that settles
the call, such as invalid arguments or a refusal before anything changed.

A tool that runs a whole Eino agent, an `adk.NewAgentTool` tool or the skill
middleware's `skill` tool with a fork skill, returns the error of the
sub-agent's failed tool. A `ToolExecutionError` there is the inner tool's
answer, and the sub-agent's earlier tools may have run, so for a call whose
effect is not `read` the adapter fails the attempt with the error's text and
without its type. The effect then decides: a `non_idempotent_write` or
`unclassified` call records `outcome_unknown`, and an `idempotent_write`
retries. A `read` call keeps it as a definite failure. A tool of your own that
runs other tools should pass an inner `ToolExecutionError` on only when
nothing ran before it.

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
- **Schemas as the source wrote them.** The adapter hands Eino's model client
  each input schema with every keyword the source wrote, including those that
  Eino's JSON Schema type has no field for, such as draft-07 `definitions`,
  `nullable`, and `x-` keys. What reaches the provider depends on the client.
  The eino-ext OpenAI, Ark, and Gemini clients send the whole schema, so a
  `$ref` still resolves. The Claude client sends only the root `properties`
  and `required`, so a root `$defs`, `definitions`, `anyOf`, or
  `additionalProperties` does not reach Claude, and a `$ref` into it does not
  resolve there. A listed tool whose schema is not a JSON Schema,
  or would still reach the model changed, is left out of that model call with a
  warning in the log. A call of its name is not sent to the source: it is
  deferred as `unclassified` without an approval, and its tool Step answers
  `unknown tool <name>`.
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

- **Tool call IDs.** An empty or blank ID, an ID with `/`, or an ID already
  used in the turn is replaced with `<StepExecutionID>-<index>` before the
  message is stored, so a retry of the same attempt stores the same IDs. Other
  IDs are kept.
- **Unknown tools.** The adapter sets `UnknownToolsHandler`. A tool the model
  invents, or one a deploy renamed or removed, is deferred like a read (as
  `unclassified` without an approval when the agent has tool sources), and
  its tool Step answers `unknown tool <name>`, so the model sees why. On a later
  attempt of a write, the core records `outcome_unknown`, because the first
  attempt may have run under the old name.
- **Stricter policies.** A tool Step does not run a call whose record has a
  weaker effect than this deploy gives the tool, or no approval where this
  deploy's policy needs one, such as a name the model invented that a later
  deploy added as a write. The model sees why and may call it again under the
  new policy, which records the call with its approval.
- **Arguments.** A tool Step checks the call's arguments against the schema
  that the tool's `Info` reports now, with the Go built-in loop's JSON Schema
  subset: `type`, `enum`, `anyOf`, `properties`, `required`,
  `additionalProperties`, `items`, `minimum`, and `maximum`. Eino converts the
  arguments first, with `ToolAliases` argument aliases and then
  `ToolArgumentsHandler`. The check runs next, before the tool middlewares, the
  `Handlers`' tool wrappers, and the tool. Arguments that break the schema, or
  that are not a JSON object, do not run, and the model sees `invalid arguments
  for <name>: <problem>`, the built-in loop's text. This protects a call that
  a deploy interrupted: Dex runs it again with its stored arguments, and after
  a deploy that renamed a parameter, a typed tool such as one from
  `utils.InferTool` would decode the old names into zero values. On that later
  attempt of a write, the core records `outcome_unknown`, because the first
  attempt may have run. The stored arguments stay as the model sent them. To
  keep old calls running after a rename, map the old names with
  `ArgumentsAliases`.
- **One model call.** A Handler or model wrapper that calls the model a second
  time ends the turn as failed before the second call reaches the model. The
  one call that ran is billed, and no retry pays for it again. When that call
  failed, its error reaches the session instead, as a model error: a 408,
  409, 429, or 5xx backs off and `RunModel` retries, and no usage is billed
  for it. The
  adapter counts calls of the agent's `Model`, so a Handler's `WrapModel`
  must call the model it receives. One that answers from another model ends
  the turn as failed, with the usage that model's message reports: choose the
  model with an `AgentConfigBuilder` instead. A fallback that answers from a
  backup model after the call of the agent's model failed ends the turn the
  same way, since two model calls then ran in one Step. Return the error
  instead, and the session backs off or ends the turn by its status.
- **The stored message.** Dex stores and runs the message that the model call
  returned through the Handlers' `WrapModel`, so change the model's text or
  tool calls there. A Handler's `AfterModelRewriteState` or an
  `AgentMiddleware.AfterChatModel` that changes that message, such as its
  arguments, a tool name, or its text, ends the turn as failed, because the
  change would not reach the stored conversation or the tool Steps. So does
  one that changes the messages before the model's, such as by dropping old
  tool results, or the state's tools. Eino would carry such a change into the
  run's next model call, but Dex replays the stored messages and offers the
  agent's tools at every model call, so it would never reach one. Make it in a
  Handler's `BeforeModelRewriteState`, which runs before every model call.
- **Turns that end as failed.** A stop that a retry would repeat ends the turn
  at once with `TurnCompleted.TurnError`: the second model call above, a
  `WrapModel` that answers from another model, also after the model call
  failed, a turn at `MaxIterations`, an Eino interrupt that a Handler raised
  in `RunModel`, a model message, earlier messages, or tools that an
  after-model hook changed, and a model call configuration from the
  `AgentConfigBuilder` that construction would refuse: a refused setting or
  middleware, two tools of one name, an alias that names another tool, a tool
  without a Dex effect, or an agent tool that reads the agent's history. The
  session sets `LastTurnError` to the reason and writes `turn_failed`; the
  next turn that completes clears it. The tool results the turn has not
  stored yet are stored, so every stored call keeps its result. The model
  message of the refused call is not stored. A tool whose `Info` fails is not
  a refusal: it fails the `RunModel` attempt, which retries.
- **Model errors.** The Go core reads the HTTP status of a failed model call
  from a `StatusCode`, `Response`, or `HTTPStatusCode` field, so go-openai and
  Ark errors reach it unchanged. The adapter's outermost model wrapper wraps an
  error in `ModelAPIError` only where the core would miss something: the
  status in the `Code` of genai errors, or response headers that an error with
  `HTTPStatusCode` carries. The core then backs off a 408, 409, 429, or 5xx,
  honoring `Retry-After`, and ends the turn at once on another 4xx.
- **Streaming.** With `WithStreaming(true)` the model call streams, and its text
  and reasoning arrive as `text_delta` and `reasoning_delta` events. Without it
  the model call uses `Generate`, and the whole text is one delta.
- **Usage.** `ResponseMeta.Usage` maps to the session's usage: prompt, completion,
  cached prompt, and reasoning tokens. A streamed call keeps each field's
  largest value across chunks, as Eino merges them, so input tokens reported
  on the first chunk count.
- **Turn limit.** `MaxIterations` (Eino's default 20) caps the model calls of one
  turn, as it caps one Eino run. A turn at the cap ends as failed with
  `TurnCompleted.TurnError` before another model call.

## Messages

Each stored `framework_item` is `{"type":"eino_message","format":1,"message":{...}}`,
the Eino `schema.Message` as `encoding/json` writes it. The Extra keys that
Eino ADK sets for itself, `_eino_msg_id` and keys that start with
`_eino_adk_`, are dropped, so a retried model call stores the same bytes.
The other Extra keys are kept, including the replay keys of eino-ext model
clients, such as the thinking and signature keys of the Claude client, so the
next model call sends them back. Only user, assistant, and tool messages are
stored; the instruction is rebuilt for every model call. `EncodeMessage` and
`DecodeMessage` read and write these items.

The model's message is stored with the call records, and the tool results with
the next model call. A before-model hook that edits a tool result in place,
such as a reduction middleware that clears old results, changes only that
model call's input: the stored result stays the one the tool returned. When
a turn fails, the session stores the results that are not stored yet before
the turn-failure note, so the conversation pairs every call with its result.
A call without a stored result, such as one whose turn an older Worker ended,
is answered with the unknown-outcome text in the model input only.

Messages another runner stored, such as the built-in agent loop, replay as
Eino text and tool calls. A message of another framework keeps its text and
its single tool call or result, when it has one.

## What replaces Eino's features

| Eino feature | Under Dex |
| --- | --- |
| `adk.Runner`, `TurnLoop`, `CheckPointStore`, `WithCheckPointID`, `ResumeWithParams` | The session Flow. Dex stores the messages and resumes from them |
| Human-in-the-loop `tool.Interrupt` and `compose.Interrupt` | Dex approvals: give the tool `RequiresApproval` or a write effect, and answer with `resolve_interrupt`. An interrupt that a Handler raises in `RunModel` ends the turn as failed. An interrupt that a tool raises fails its tool Step attempt, so the call's effect decides the outcome: a `read` retries and then records `failed`, and a write that may have run records `outcome_unknown` |
| `ModelRetryConfig` | `RunModel` retries; tune it with `WithModelRetryPolicy`. Refused at construction |
| `ModelFailoverConfig` | Choose the model per call with an `AgentConfigBuilder`. Refused at construction |
| Sub-agents and `transfer_to_agent`, supervisor, plan-execute | Not supported: they make several model calls in one Step. Wrap a sub-agent as a tool instead; see Eino middlewares and prebuilt agents |
| `ToolsConfig.ReturnDirectly`, `Exit` | Refused at construction |
| `ToolsConfig.ExecuteSequentially` | Refused at construction: the tool calls of one model response run as parallel Steps |
| Summarization middleware, and the v0.10 automemory and dream middlewares | Refused at construction: they make their own model calls |
| `UnknownToolsHandler` | Replaced by the adapter's, which answers `unknown tool <name>` |
| Eino's tool event sender | `AgentEvents`; the session writes tool call events |

## Eino middlewares and prebuilt agents

The table gives the status under Dex of every middleware and prebuilt agent
that Eino v0.9.23 ships. The integration tests run each one that runs,
configured as its package documents it, through tool rounds and a final
answer, with `Generate` and with `WithStreaming(true)`, on Eino v0.9.23 and
the v0.10 alpha.

A middleware's tools are tools like any other, so give each one an effect.
The effects below are what each tool does; a tool you leave out gets
`WithDefaultToolEffect`, or `unclassified` with an approval. Middleware tools
run in their tool Steps, often on another Worker than the model call, so give
the middleware a backend that every Worker reaches, not an in-memory one.

| Middleware or agent | Status | Under Dex |
| --- | --- | --- |
| `agentsmd.New` | Runs | Its `BeforeModelRewriteState` adds the `AGENTS.md` content before every model call. The added message is model input only, so Dex does not store it |
| `patchtoolcalls.New` | Runs | The adapter already answers a call without a stored result with the unknown-outcome text, so the middleware finds no dangling call |
| `dynamictool/toolsearch.New` | Runs, in both modes | `tool_search` is a `read`. Give each dynamic tool its effect; it is offered by name, as a Handler-added tool. In native mode (`UseModelToolSearch: true`) the model gets a placeholder as the `tool_search` result; see Message fidelity |
| `filesystem.New` | Runs | `ls`, `read_file`, `glob`, and `grep` are `read`; `write_file` is an `idempotent_write`; `edit_file` and `execute` are `non_idempotent_write`. `execute` runs the `Shell` or `StreamingShell` in its tool Step. The schemas of `read_file`, `glob`, and `edit_file` mark `offset` and `limit`, `path`, and `replace_all` as required, although their descriptions say they may be left out. The tool Step checks arguments against the schema, so a call without them gets `invalid arguments for read_file: arguments.offset is required`. To accept such calls, fill the fields in a `ToolArgumentsHandler`, which runs before the check |
| `filesystem.NewMiddleware` (deprecated) | Runs | Its tools and its large-result offloading run in the tool Step. The offloaded result is written to the backend there, at `/large_tool_result/<tool call ID>`, so a retry writes the same file |
| `plantask.New` | Runs | `TaskCreate` is a `non_idempotent_write`, because it takes the next task number; `TaskUpdate` is an `idempotent_write`; `TaskGet` and `TaskList` are `read`. In one Eino run the four tools share a lock, so the calls of one response take task numbers one after another. Under Dex each call is its own Step, so the adapter marks plantask calls `DeferredToolCall.Ordered`: the session runs their writes one at a time in the model's order, and when one does not succeed, records the later ones as not run, so the model may make them again. `TaskGet` and `TaskList` run in parallel with the writes and may read a task list that a write is changing |
| `reduction.New` | Runs | Truncation runs in the tool Step, so the stored result is the truncated text, and its offloaded file is written there. The clear phase runs in `BeforeModelRewriteState` before every model call, over the stored messages, and changes only that call's input. With a backend, the clear phase writes the cleared content inside `RunModel`, at every model call over the threshold and on each retry, to the same path, so the backend's `Write` must be safe to repeat |
| `reduction.NewClearToolResult`, `reduction.NewToolResultMiddleware` (deprecated) | Runs | They clear old tool results in place before the model call. The model call sees the placeholder, and Dex stores the result the tool returned |
| `skill.NewMiddleware`, `skill.New` (deprecated) | Runs inline and `context: fork` skills | A fork skill's sub-agent, with its model calls and tools, runs inside the `skill` tool Step, so give `skill` the effect of what its sub-agents do; a `ToolExecutionError` from a sub-agent's tool is handled as for `adk.NewAgentTool`. A `context: fork_with_context` skill reads the agent's history in the tool Step, which a Dex tool Step does not have, so its call fails as a `ToolExecutionError` that names the mode, before anything runs. A skill's `model` in inline mode does not switch the model: it lasts one Eino run, and each model call is a new run. Choose the model with an `AgentConfigBuilder` |
| `summarization.New` | Refused at construction | It makes its own model call before a model call |
| `adk.NewAgentTool` | Runs | The sub-agent, with its model calls and tools, runs inside the agent tool's Step, so give the agent tool the effect of what its sub-agent does. A `ToolExecutionError` from a sub-agent's tool fails the attempt of a call whose effect is not `read`, so the effect decides the outcome; see Effects and policies. An agent tool made with `adk.WithFullChatHistoryAsInput` reads the agent's history in its tool Step, so it is refused at construction, and a call of one that a Handler adds fails as a `ToolExecutionError` before it runs. Pass what the sub-agent needs in the call's arguments |
| `adk.NewEventSenderModelWrapper`, `adk.NewEventSenderToolWrapper` | Runs | The model call's Eino events are not read, and the tool Step skips Eino's own Handlers; the session writes its own events |
| `prebuilt/deep`, `prebuilt/supervisor`, `prebuilt/planexecute` | Not accepted | Their `New` returns a built agent, not a `ChatModelAgentConfig`, so they do not compile as an argument. For a deep agent, pass a `ChatModelAgentConfig` with the `filesystem.New` and `plantask.New` Handlers and each sub-agent as an `adk.NewAgentTool`. A supervisor's transfer and plan-execute's agent loop make several model calls in one Step |

None of these middlewares changes the agent state after the model call, so
the after-model checks under Integration mode, in The stored message, end no
turn of theirs. The Eino v0.10 alpha adds the `automemory` and `dream` middlewares, which are
refused at construction, and the `backgroundtask` and `subagent` middlewares,
which these tests do not cover.

## Moving an existing Eino agent

1. Pass the `*adk.ChatModelAgentConfig` to `NewAgentSessionFlow` instead of
   building an agent and a `Runner`. Drop `CheckPointStore`, `TurnLoop`, and
   `WithCheckPointID`.
2. Give every tool an effect.
3. Remove `ModelRetryConfig`, `ModelFailoverConfig`, and
   `ToolsConfig.ExecuteSequentially`. Turn off retries in the model client
   where it has them: set Ark's `RetryTimes` to 0. The eino-ext
   Claude client retries twice inside its SDK and has no setting to stop it, so
   one `RunModel` attempt may send up to three requests.
4. Replace Eino human-in-the-loop interrupts with Dex approvals.
5. Return `*dexagents.ToolExecutionError` for definite tool failures, and stop
   using `utils.WrapToolWithErrorHandler`.
6. Move changes to the model's message from a Handler's
   `AfterModelRewriteState` or an `AgentMiddleware.AfterChatModel` into a
   Handler's `WrapModel`, and changes to earlier messages or to the tools into
   a Handler's `BeforeModelRewriteState`. Let every `WrapModel` call the model
   it receives and return its error, with no fallback to another model. Choose
   the model per call with an `AgentConfigBuilder`.
7. Convert arguments, such as renamed parameters, with `ToolAliases`
   argument aliases or `ToolArgumentsHandler`, not in a tool middleware or a
   Handler's tool wrapper: the tool Step checks the arguments against the
   tool's schema after the conversion and before the wrappers.

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
  `Extra` come back as JSON maps. Handlers that run after the model call, and
  the rest of the Eino run, already see the model's message that way, because
  Eino copies the run state with a serializer that refuses unregistered types.
  Dex stores text tool results, so a result part that is not text reaches the
  model as a placeholder. One such part is the result of the `tool_search`
  tool in the tool search middleware's native mode
  (`UseModelToolSearch: true`); the deferred tools that the model's own search
  finds still run, each with its effect.
- **Tools must be safe for concurrent use**, because the calls of one model
  response run as parallel Steps, possibly in one Worker, in any order. For
  this reason `ToolsConfig.ExecuteSequentially` refuses to build. When one call
  needs another's result, let the model make them in separate responses. A
  lock that tools share in one Eino run, such as one a Handler's `BeforeAgent`
  creates for the tools it adds, does not hold across Steps; the adapter runs
  Eino's plantask calls in order for this reason, and other such tools need a
  lock in their shared backend.
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
module, with build tag `eino_v010`, plus the tests in `testdata/eino_v010` of
packages that only v0.10 has. A failed version lookup or an alpha that does
not resolve fails the run; only an empty alpha list skips the second pass.

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```
