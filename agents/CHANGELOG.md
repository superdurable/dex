# Changelog

All eleven Dex agents packages share this changelog and one version line. A
release is tagged `agents/v<version>`. See [README.md](README.md#versions-and-compatibility)
for the Dex SDK and framework versions each release supports.

## 0.2.0 (Unreleased)

### MCP tool source

- `dex_agents.mcp` (extra `dex-agents[mcp]`, `mcp>=2.3,<3`) and
  `@superdurable/dex-agents/mcp` (optional peer `@modelcontextprotocol/sdk`
  `^1.32.2`): `McpToolSource` lists the tools of MCP servers configured on the
  Worker, over stdio or Streamable HTTP, and runs each call in its own tool
  Step. `readOnlyHint` makes a tool `read`, `idempotentHint` makes it
  `idempotent_write`, and an explicit `ToolPolicy` wins. A call whose response
  never arrived is re-issued only for `read` and `idempotent_write` tools;
  other tools record `outcome_unknown`. MCP tasks are polled inside the tool
  Step with heartbeats and a bounded wait, and a retried attempt resumes the
  same task; `input_required` records `failed`.
- Both cores: `DeferredToolCall.tool_source_reference` (stored on
  `AgentToolCall` as `tool_source_reference`) and
  `DeferredToolExecution.heartbeat_checkpoint` / `save_heartbeat_checkpoint`.
- TypeScript: `toolEffectFromMcpToolAnnotations` maps `idempotentHint` to
  `idempotent_write` whatever `destructiveHint` says, as the Python adapters
  do.

### UI streams

- `@superdurable/dex-agents/ui-stream`: an AI SDK UI message stream (protocol
  `v1`) for `useChat`, resumed through a UI stream resume token, with
  `reset-step` for a new `RunModel` attempt and tool approvals through
  `resolve_interrupt`; plain Server-Sent Events resumed with `Last-Event-ID`;
  and snapshots as AI SDK `UIMessage` objects or wire JSON.
- `dex_agents.ui_stream`: Server-Sent Event frames resumed with
  `Last-Event-ID`, and snapshot JSON, for async and synchronous Clients.
- The testing exports' private engines accept `dex-server-zd` configuration
  overrides: `serverZdConfigPath` in TypeScript and `server_zd_config` in
  Python.
- The browser snapshot JSON, `convertAgentSnapshotToJson` and
  `agent_snapshot_to_json`, leaves out the fields only the session's Steps
  use: `framework_item` on messages, and `tool_source_reference`,
  `trace_context`, `input_continuation`, and `input_answers` on tool calls.
  Both cores produce the same JSON.
- The AI SDK UI message stream pauses for an approval only while its interrupt
  is still in `PendingInterrupts`. A reconnect after `resolve_interrupt` and
  before the approval Step commits keeps reading instead of showing the
  approval as pending again.

### OpenTelemetry GenAI spans

Both cores emit OpenTelemetry GenAI spans: one trace per activation, rooted at
`invoke_agent {agent name}`, with a `chat` span per `RunModel` attempt and an
`execute_tool {tool name}` span per tool Step attempt. Every span carries the
session Flow ID as `gen_ai.conversation.id` and `session.id`, and its Dex Flow,
run, and Step identity. Activations link to the previous one. Content is by
reference unless the application opts in. Tracing never fails or retries a
Step: OpenTelemetry errors are logged and the span is skipped.

- `dex-agents`: the optional `otel` extra installs `opentelemetry-api`.
  `AgentSessionOptions.tracing` takes `AgentTracingOptions` (`is_enabled`,
  `tracer_provider`, `agent_name`, `should_capture_message_content`,
  `semantic_conventions_version`). Runners may return `ModelCallMetadata` as
  `model_call_metadata` in `TurnCompleted` and `ToolCallsRequested`.
- `@superdurable/dex-agents`: `@opentelemetry/api@^1.3.0` is an optional peer
  dependency. `AgentSessionOptions.tracing` takes `AgentTracingOptions`
  (`isEnabled`, `tracerProvider`, `agentName`, `shouldCaptureMessageContent`,
  `semanticConventionsVersion`). Runners may return `modelCallMetadata` in
  either outcome.
- Both session Flows register the `AgentTraceContext` Attribute, which holds
  the current activation's `traceparent` whenever its root has a valid span
  context, sampled or not, so the activation's spans share its sampling
  decision. `AgentToolCall` gains an optional `trace_context`
  (`traceContext`), the activation root its tool spans join.
- A `chat` span is named `chat {model}` only when the runner returns
  `ModelCallMetadata`. Every framework adapter returns it with what its
  framework reports for the call and leaves the rest unset; each adapter's
  README lists its fields. When one `RunModel` makes several model calls, an
  adapter keeps only the values every call shares and leaves out the response
  ID and finish reasons. A provider identifier becomes its well-known
  `gen_ai.provider.name` value of the pinned conventions when one applies, and
  other values pass through unchanged. No adapter derives a provider from a
  model name, class, base URL, or environment variable.
  - OpenAI Agents SDK, Python and TypeScript: the requested model when it is
    given by name, and the response ID unless it is the SDK's placeholder
    (`__fake_id__` in Python, `FAKE_ID` in TypeScript). The SDK reports no
    provider, response model, or finish reason.
  - Pydantic AI: the requested model, and the response model, provider,
    response ID, and finish reason of the `ModelResponse`. For the `google`,
    `google-cloud`, and `openai` providers the provider comes from the
    response's `canonical_system`, which names the endpoint the client
    reached, so a Vertex AI client reports `gcp.vertex_ai` and an Azure OpenAI
    client `azure.ai.openai`.
  - LangChain and LangGraph: the chat model's `ls_model_name`, and the
    `model_name`, `model_provider`, `id`, and finish reason of the reply's
    `response_metadata`. `google_genai` becomes `gcp.gen_ai`, because
    `ChatGoogleGenerativeAI` reports it for both the Gemini API and Vertex AI.
  - Google ADK: the requested model, and the response's `model_version` and
    finish reason. ADK keeps no response ID and names no provider.
  - Vercel AI SDK and Mastra: the model ID, the provider ID by its longest
    known prefix (`openai.responses` becomes `openai`, `google.vertex.chat`
    becomes `gcp.vertex_ai`; an unknown ID keeps its first segment, so
    `togetherai.chat` becomes `togetherai`), the response model when it
    differs from the requested one, and the finish reason. They report no
    response ID: when the provider sends none, the AI SDK and Mastra fill in
    a generated ID and the requested model, and their public APIs do not say
    which it is.
  - Claude Agent SDK, Python and TypeScript: the model of the CLI's `init`
    message, the provider from the result's model usage (`firstParty`
    becomes `anthropic`, `bedrock` and `mantle` become `aws.bedrock`, `vertex`
    becomes `gcp.vertex_ai`, and `gateway` reports none), and the response
    model, message ID, and stop reason of the replies. A segment in which a
    subagent called a model reports no request model.
- `createMastraAgentSessionFlow` and `createOpenAIAgentsSessionFlow` pass
  `tracing` to the session Flow. They dropped it before, so their sessions
  traced with the global tracer provider and the default options.
- The Python helpers `claude_agent_session_flow`, `adk_agent_session_flow`,
  `openai_agents_session_flow`, `langchain_agent_session_flow`,
  `langgraph_agent_session_flow`, and `pydantic_ai_session_flow` take
  `tracing`, an `AgentTracingOptions`.
- The TypeScript Claude Agent SDK adapter counts a subagent's model calls in
  `AgentUsage.modelCalls`, as the Python adapter does. A turn segment with a
  subagent call therefore gets an `INTERNAL` `chat` span without usage in both
  languages.

### Built-in agent loop (Stepwise mode)

Phase 1 of the M2 plan. Stepwise mode runs an agent defined in Dex terms,
with no framework: one model call per `RunModel` Step, and every tool call its
own Step, through the same session Flow the adapters use.

- `dex-agents`: `dex_agents.stepwise` with `AgentDefinition`,
  `@function_tool` (JSON Schemas from type hints, standard library only), the
  `ModelClient` protocol, `StepwiseAgentRunner`, and
  `stepwise_agent_session_flow`. After `max_model_calls_per_turn` model calls
  the next call offers no tools; a tool call the model makes anyway gets the
  result `The tool call was not run: this turn reached its limit of <n> model
  calls.`, and the turn ends.
- `dex-agents`: `dex_agents.litellm.LiteLLMModelClient` behind the new extra
  `dex-agents[litellm]` (`litellm>=1.104,<1.106`; 1.106 makes every Responses
  API call import the undeclared `mcp` package). OpenAI models use the
  Responses API with `store=False` and encrypted reasoning; other providers use
  streaming Chat Completions with Anthropic thinking signatures. Reasoning items
  and signatures replay byte for byte, and the client never retries.
- `dex-agents`: `ScriptedModelClient` in `dex_agents.testing`, and
  `AgentTestStack.start_worker(python_executable=...)` to run a Worker on
  another environment, such as an earlier release.
- `@superdurable/dex-agents/stepwise`: Stepwise mode:
  `createStepwiseAgentSessionFlow`, `StepwiseAgentRunner`, `functionTool`, and
  the `ModelClient` interface.
- `@superdurable/dex-agents/ai-sdk`: `createAiSdkModelClient` for any AI SDK
  `LanguageModel`, behind the optional peer `ai@^7.0.159`. It makes one
  `streamText` call per `RunModel` (one step, `maxRetries` 0, tools without
  `execute`) and replays reasoning and tool call parts with their provider
  metadata unchanged.
- `@superdurable/dex-agents/testing`: `ScriptedModelClient` for Stepwise tests
  without a model key.

### Questions, durable waits, and incremental turn messages

Both cores, with identical Step types, RPC names, wire values, events, and
model-visible text. Everything is inert until an application enables it.

- `AgentSessionOptions.built_in_tools` (`builtInTools`) enables the
  `ask_user` and `durable_wait` built-in tools, whose definitions are
  `BUILT_IN_TOOL_DEFINITIONS` (`builtInToolDefinitions`). `RunModel` routes
  their calls to their own Steps with no approval and the `read` effect, and
  fails invalid arguments at once. `null` for an optional `ask_user` argument
  means absent.
- `ask_user` raises an interrupt of kind `question`, which the new
  `answer_question` RPC answers and the new `AwaitQuestionAnswer` Step waits
  for; the answer is the tool result and the turn continues. A tool can ask too
  by returning `ToolInputRequested`; `AwaitQuestionAnswer` raises its question
  under the `PendingInterrupts` lock, so parallel tool calls can ask at once.
  `interrupt_deadline` applies to questions.
- `durable_wait` waits on a whole-second Timer in the new `AwaitTimer` Step.
  `AgentSessionOptions.max_durable_wait` (`maxDurableWaitMs`) defaults to 365
  days and must be whole seconds.
- `ToolCallsRequested.new_messages` (`newMessages`) stores a turn's items as
  they appear. A runner that sets it implements
  `build_unfinished_turn_messages` (`buildUnfinishedTurnMessages`), which
  `RecordTurnFailure` calls; a runner without it fails the turn in `RunModel`'s
  own commit, after one model call.
- New wire values: `AgentToolCall` statuses `awaiting_answer` and
  `waiting_for_timer` (non-final) and fields `wait_until`,
  `input_continuation`, `input_answers`, `input_question`, `input_choices`, and
  `input_allows_free_text`; `AgentInterrupt` fields `question`, `choices`, and
  `allows_free_text`; `AgentInterruptResolution.answer`;
  `AnswerQuestionRequest`; `QuestionAnswer`; the `timer_started` event; and new
  keys on `interrupt_raised` and `interrupt_resolved`.
- Both cores write RFC 3339 times with milliseconds and `Z`, such as
  `2026-11-01T12:00:00.123Z`. Every reader in both cores, 0.1.0 included,
  accepts the earlier `+00:00` form too.

### Plans and plan mode

Both cores, with identical Step types, persistence names, RPC names, wire
values, events, and model-visible text. Everything is inert until an
application enables it.

- The `write_todos` built-in replaces the new `AgentPlan` Attribute
  (`revision`, `status`, `tasks`, `updated_at`) with the next revision.
  `RunModel` records the call in its own commit and still moves to
  `ResumeTurn`; no tool Step runs. An empty list clears the plan, and a
  response with two `write_todos` calls fails both. The session writes
  `plan_updated`, and again when an approval marks a draft `active`.
- `AgentSessionOptions.plan_mode_enabled` (`planModeEnabled`) accepts
  `AgentUserMessage.plan_mode` (`planMode`) messages, which start a turn whose
  new `AgentState.interaction_mode` is `planning`; `send_user_message` refuses
  them otherwise. It needs `write_todos` and a runner that declares
  `supports_plan_mode` (`supportsPlanMode`, `PlanModeRunner` in Python). A
  planning turn rejects every call but `write_todos`.
- The new `execute_plan` RPC approves one plan revision. It locks the new
  `PendingPlanExecution` Attribute and publishes to the new `PlanExecutions`
  ChannelMap, keyed by revision, which `AwaitUserMessage` waits on after
  queued messages; it starts an `executing` turn and writes
  `plan_execution_started`. A stale revision, or a second approval of a
  revision not started yet, returns false.
- Stepwise: `AgentDefinition.plan_mode_enabled` (`planModeEnabled`). A planning
  turn offers only `write_todos`, and every model call shows the plan with
  `build_plan_note` (`buildPlanNote`) before the turn's messages.
  `AgentDefinition.planning_tool_choice` (`planningToolChoice`) is `auto` by
  default, so a planning turn may also answer in text and end without a draft;
  `required` forces `write_todos` on the first call while no draft exists. In
  Python, `required` is refused for a model client whose
  `supports_forced_tool_choice` returns false, as `LiteLLMModelClient` does
  for Claude Opus 5.5, Claude Sonnet 5.5, and Anthropic extended thinking,
  which on an Anthropic, Bedrock Claude, or Vertex AI Claude model is a
  `thinking` option or any `reasoning_effort` but `"none"`.
  `InstructionContext` gains `interaction_mode` and `plan`.
- Both cores reject `NaN` and `Infinity` in built-in tool arguments and report
  the first unknown argument by code point, so the errors the model sees are
  the same. An explicit `null` for `plan_mode` or `interaction_mode` reads as
  the default in both cores.
- `AgentTurnContext` gains `interaction_mode` and `plan`, `AgentSnapshot`
  gains `plan`, and `AgentSessionClient` (Python) and the TypeScript testing
  client gain `execute_plan` and a `plan_mode` argument.

### Fixes

- TypeScript: `send_user_message`, the `AwaitUserMessage` Step that takes its
  messages, and `StartAgentSession`'s initial message check for blank text with
  Python's `str.strip()` whitespace, as the Python core does. Text made only of
  U+001C to U+001F or U+0085 is now blank, and text made only of U+FEFF is now
  a message. 0.1.0 used `String.trim()`.

### Compatibility

- Enable the new options and call the new RPCs only after every Worker of the
  Flow type runs 0.2.0. A session started on 0.1.0 continues on 0.2.0.
- Python: `ToolCallStatus` gains the two non-final members, so an exhaustive
  `match` or `assert_never` over the status of a stored record, such as
  `AgentSnapshot.tool_calls`, must handle them. Runners still see only final
  records. `AgentFrameworkRunner` is generic in what
  `execute_deferred_tool_call` returns; a bare `AgentFrameworkRunner` means
  `AgentFrameworkRunner[ToolExecutionResult]`, as in 0.1.0, and a runner whose
  tools ask the user implements `AgentFrameworkRunner[DeferredToolOutcome]`.
  `AgentSessionFlow.runner` returns `AgentFrameworkRunner[DeferredToolOutcome]`.
  Every other 0.1.0 API accepts 0.1.0 code: new fields and parameters have
  defaults.
- TypeScript: `AgentFrameworkRunner<ToolOutcome = ToolExecutionResult>` is
  generic, so existing runners and code that calls them compile unchanged. A
  runner whose tools ask the user implements
  `AgentFrameworkRunner<DeferredToolOutcome>`. `AgentSnapshot.toolCalls` and
  records read with `Client.getAttributes` are now `AgentToolCallRecord`, whose
  status also allows the non-final `awaiting_answer` and `waiting_for_timer`;
  code that assigns them to `AgentToolCall` must widen the type.
  `AgentToolCallStatus`, which runners see, keeps its six values. Every other
  0.1.0 type still accepts 0.1.0 code: new fields are optional.
- TypeScript Claude Agent SDK: `AgentUsage.modelCalls` counts a subagent's
  model calls, as the Python adapter always did; 0.1.0 counted only the main
  conversation. The `AgentUsage` Attribute is cumulative, so in a session that
  started on 0.1.0 the total mixes both meanings: turns that ran on 0.1.0 count
  main-conversation calls only, and later turns count subagent calls too.
- The Python adapters' lockfiles record the core's new extras; adapter code is
  unchanged.

## 0.1.0 (2026-11-01)

First release of the agent framework adapters. Every package runs agent
sessions in Step mode: each model call is one Dex Step and each tool call is
its own Step.

### Python (PyPI)

- `dex-agents`: the shared agent session Flow, the runner contract, and
  `dex_agents.testing`. Requires `dex-python-sdk==1.30.0`.
- `dex-agents-openai-agents`: OpenAI Agents SDK, `openai-agents>=0.24,<0.25`.
- `dex-agents-pydantic-ai`: Pydantic AI, `pydantic-ai-slim>=2.64,<3`.
- `dex-agents-langgraph`: LangChain `create_agent` agents and LangGraph
  graphs, `langgraph>=1.2.19,<2`.
- `dex-agents-google-adk`: Google ADK, `google-adk>=2.13,<3`.
- `dex-agents-claude-agent-sdk`: Claude Agent SDK,
  `claude-agent-sdk>=0.2.176,<0.3`.

### TypeScript (npm)

- `@superdurable/dex-agents`: the shared agent session Flow, the runner
  contract, and `@superdurable/dex-agents/testing`. Peer-depends on
  `@superdurable/dex@1.30.0`.
- `@superdurable/dex-agents-vercel-ai`: Vercel AI SDK, `ai@^7.0.159`.
- `@superdurable/dex-agents-openai-agents`: OpenAI Agents SDK,
  `@openai/agents@^0.21.0`.
- `@superdurable/dex-agents-mastra`: Mastra, `@mastra/core@^1.81.0`.
- `@superdurable/dex-agents-claude-agent-sdk`: Claude Agent SDK,
  `@anthropic-ai/claude-agent-sdk@^0.3.316`.

Every npm adapter peer-depends on `@superdurable/dex-agents` and
`@superdurable/dex`, so an application holds one copy of each.
