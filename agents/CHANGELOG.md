# Changelog

All eleven Dex agents packages share this changelog and one version line. A
release is tagged `agents/v<version>`. See [README.md](README.md#versions-and-compatibility)
for the Dex SDK and framework versions each release supports.

## Unreleased

### Dex MCP conventions

- Python and TypeScript cores: **McpToolSource** follows the Dex MCP
  conventions, so an MCP server built for Dex carries connector guarantees
  across MCP. A tool definition's `_meta` key `io.superdurable.dex/effect`
  (`read`, `idempotent_write`, `non_idempotent_write`) fixes the tool's effect
  class, whatever its annotations say, and `io.superdurable.dex/approval:
  recommended` asks for approval, as a connector manifest does. A session tool
  policy for such a tool may raise its effect class or its approval but never
  lowers its effect class: the call gets the more conservative of the two
  effects. Its writes run in the model's order. Servers without these keys
  behave as before.
- New `McpToolSource.merge_tool_policies` (`mergeToolPolicies`) gives a
  framework adapter the application's tool policies merged by the same rule.
  Use it instead of spreading your policies after `tool_policies()`
  (`toolPolicies()`), which would let them lower a declared effect.
- A tool result's `_meta` key `io.superdurable.dex/outcome` (or a JSON-RPC
  error's `data._meta`) set to `unknown` records `outcome_unknown` and never
  sends the call again; `rejected` records `failed`. Either wins over `isError`.
- New constants: `TOOL_EFFECT_META_KEY`, `TOOL_APPROVAL_META_KEY`,
  `TOOL_OUTCOME_META_KEY` and their values in `dex_agents.mcp`;
  `mcpToolEffectMetaKey`, `mcpToolApprovalMetaKey`, `mcpToolOutcomeMetaKey`
  and their values in `@superdurable/dex-agents/mcp`. New `McpTool` fields
  `dex_effect` / `dexEffect` and `is_approval_recommended` /
  `isApprovalRecommended`.
- Both cores: `poll_tool_task` (`pollToolTask`) may raise
  `ToolOutcomeUnknownError`; the session records `outcome_unknown` and stops
  polling. It used to fail the poll attempt.
- `agents/conformance/mcp_dex_conventions.json` fixes the conventions for every
  core.
- TypeScript core: **McpToolSource.executeDeferredToolCall** takes an optional
  second argument, `executeDeferredToolCall(call, { requestMeta })`, and
  **McpToolCallOptions** has `requestMeta`: more entries for the `tools/call`
  request's `_meta`, such as the identity the caller expects the tool to have.
  The idempotency key entry wins on a name clash.
- TypeScript core: new **McpToolSource.willResumeToolTaskWithoutSending(call)**
  is `true` only when `executeDeferredToolCall` will return a session-polled
  task that an earlier attempt started and can never send `tools/call` for the
  call. A task polled inside the tool Step, such as one bound to a protocol
  session, may be called again when the server no longer knows it.

### Images

- Python and TypeScript cores: user messages and tool results can carry
  images. Session state stores only an **AgentImageReference**
  (`{"type": "image", "uri", "mime_type", "sha256", "size_bytes"}`); the bytes
  live in a **MediaStore**, one object per image under
  `<prefix>/sha256/<sha256>`, so uploads are idempotent and a replay reads the
  same bytes.
- New fields, all empty by default: **AgentUserMessage.images**,
  **AgentMessage.images** (display copy), **AgentToolCall.images**,
  **ToolExecutionResult.images**, and **FrameworkMessage.images**. Every
  writer now stores `"images": []`; earlier Workers ignore the field, so send
  images only after every Worker of the Flow type runs 0.5.0.
- **S3MediaStore** (`dex_agents.s3`, extra `dex-agents[s3]`;
  `@superdurable/dex-agents/s3`, optional peer `@aws-sdk/client-s3`) keeps
  images in AWS S3 or an S3-compatible bucket, with credentials from the
  standard AWS chain. **InMemoryMediaStore** is for tests.
- PNG, JPEG, GIF, and WebP, detected from the bytes with a cheap structural
  check, up to **MAX_IMAGE_BYTES** (`maxImageBytes`, 3,750,000 bytes,
  5,000,000 bytes as base64) each, and at most **MAX_IMAGES_PER_MESSAGE**
  (`maxImagesPerMessage`, 20) per message or tool result. A `uri` is at most
  **MAX_IMAGE_URI_LENGTH** (`maxImageUriLength`, 1024) bytes as UTF-8.
  **ImageValidationError** names what is wrong.
- Reads check the SHA-256, the size, and the type: **verify_image_bytes**
  (`verifyImageBytes`) refuses bytes that are not a well-formed image of the
  reference's `mime_type`. **S3MediaStore** uploads with `If-None-Match: *`
  (412 means stored; a 409 conflict retries, then needs a `HEAD` that finds
  the object),
  refuses an object whose `Content-Length` differs from the reference, and
  reads at most one byte past `size_bytes`. It takes explicit credentials
  (`access_key_id` and `secret_access_key`; `credentials`) for a local
  development bucket, and the standard AWS chain by default.
- The session checks the images of every `FrameworkMessage` a runner returns
  before it stores any. Keep each object for as long as sessions can replay
  it; a missing object fails the turn with **MediaStoreError**.
- `send_user_message` returns `false` for invalid images, or for images when
  the runner does not implement **ImageUserMessageBuilder**
  (`buildUserMessageWithImages`). No built-in runner implements it yet; image
  input for Stepwise and the framework adapters comes later.
- Runners read bytes with **load_images** (`loadImages`) and may choose them
  with **collect_turn_images** (`collectTurnImages`).
- Go core: no image input. Its values carry `images`, so a value written by
  another core round-trips, and `send_user_message` returns `false` for a
  message with images.
- Shared cases: `agents/conformance/images.json`.

### Changed

- The built-in agent loop has new names that say what it is. The old names
  still work for one release, refer to the same objects, and are deprecated.
  Stored sessions, Flow types, and wire values are unchanged, so a running
  session resumes after the upgrade. Migrate as follows:
  - Python core: module `dex_agents.stepwise` -> `dex_agents.builtin_loop`;
    `StepwiseAgentRunner` -> `BuiltinLoopAgentRunner`;
    `stepwise_agent_session_flow` -> `builtin_agent_session_flow`;
    `stepwise_agent_session_options` -> `builtin_agent_session_options`;
    `encode_stepwise_message` -> `encode_builtin_loop_message`;
    `decode_stepwise_message` -> `decode_builtin_loop_message`. Every other
    name keeps its name in the new module. Each name read from
    `dex_agents.stepwise` or its submodules emits a `DeprecationWarning`.
  - TypeScript core: subpath `@superdurable/dex-agents/stepwise` ->
    `@superdurable/dex-agents/builtin-loop`; `StepwiseAgentRunner` ->
    `BuiltinLoopAgentRunner`; `createStepwiseAgentSessionFlow` ->
    `createBuiltinAgentSessionFlow`. The old subpath and names are marked
    `@deprecated`.
  - Go core: package `github.com/superdurable/dex/agents/go/core/stepwise` ->
    `github.com/superdurable/dex/agents/go/core/builtinloop`;
    `StepwiseAgentRunner` -> `BuiltinLoopAgentRunner`;
    `NewStepwiseAgentRunner` -> `NewBuiltinLoopAgentRunner`;
    `NewStepwiseAgentSessionFlow` -> `NewBuiltinAgentSessionFlow`;
    `EncodeStepwiseMessage` -> `EncodeBuiltinLoopMessage`;
    `DecodeStepwiseMessage` -> `DecodeBuiltinLoopMessage`. Package `stepwise`
    forwards to `builtinloop` and is marked `Deprecated:`.
  - The framework adapters and the agents CLI runtime import the new names.
- The error text for a session message that the built-in agent loop cannot
  read now says "built-in agent loop item" instead of "Stepwise item", and the
  error for a framework run state it cannot read says "built-in loop" instead
  of "Stepwise" (Go and TypeScript cores). The
  Python loggers are now named `dex_agents.builtin_loop.*`, and the Go core's
  panic messages for a nil model client or definition start with
  `builtinloop:` instead of `stepwise:`.
- Breaking, Python and TypeScript cores: a connector tool's default name now
  uses the Connector library's snake case (connectorctl `lowerCamelToSnake`),
  which also names the Python Step factories. An acronym run stays one word.
  Digits alone change nothing. Old -> new, in both languages:
  `listStoryIDs` `list_story_i_d_s` -> `list_story_ids`, `getHTTPResponse`
  `get_h_t_t_p_response` -> `get_http_response`, and `get2FACode`
  `get2_f_a_code` -> `get2_fa_code`. Only capitals are ASCII now: the old Python
  name also split before a non-ASCII capital, so `fetchÜberData` changes in
  Python only, `fetch_über_data` -> `fetchÜber_data`; TypeScript already gave
  `fetchÜber_data`. The owner authorized this break before launch.
  Migration: a call stored under the old name finds no tool after the upgrade
  and ends as a removed tool's call does. To keep a previous name, pass it
  explicitly, such as `connector_tool(op, ref, name="stories_list_story_i_d_s")`
  or `connectorTool(op, ref, { ..., name: "stories_list_story_i_d_s" })`; a
  recorded call then resumes under it. `agents/conformance/connector_tools.json`
  adds `toolNameCases`.
- Breaking, Python and TypeScript cores: a Mutation's `defect` now records
  `outcome_unknown` instead of `failed`, whatever `agent.branches` says, and is
  not retried. The Connector SDK selects `defect` for some attempts that already
  called the provider, and the core cannot tell those apart. A Query's `defect`
  now follows `agent.branches`: listed `rejected` it is a rejection, otherwise
  it still records `failed`. `uncertain` still records `outcome_unknown`. Both
  rules now apply before the required branches, so a `defect` or `uncertain`
  declared required no longer records `succeeded`.
- Breaking, Python and TypeScript cores: a Mutation that uses the
  single-dispatch fence records `outcome_unknown` on every branch but the
  success branch when its fence checkpoints cannot prove that no request
  reached the provider, whatever `agent.branches` says. This is the same check
  the Go Connector SDK makes for agent hosts. Once the fence claims a send in
  an attempt, only its `canResend` marker lowers it, so a later checkpoint of
  the operation cannot. On a retry, an earlier checkpoint that is missing, not a
  fence marker, malformed, or another call's counts as a possible send. The same
  evidence decides a single-attempt write's Retry: `failed` with "not sent; the
  provider asked to retry later" needs a `notSent` or `canResend` marker and no
  possible send. `classify_connector_branch` (`classifyConnectorBranch`) takes
  a new optional `may_have_dispatched` (`mayHaveDispatched`), which the source
  computes. `agents/conformance/connector_tools.json` adds
  `fenceObservationCases`.

### Fixed

- Python and TypeScript Claude Agent SDK adapters: when the Claude Code CLI
  returns an error result, the turn's error now reads "the Claude Code CLI
  returned an error result (subtype ...)". It used to read "reported success"
  for an error result whose subtype is `success`, such as a failed login.

## 0.4.0 (2026-11-10)

### Connector tools

- Python and TypeScript cores: **ConnectorToolSource** offers reviewed Connector
  operations to a Stepwise agent as tools (`dex_agents.connectors`, extra
  `dex-agents[connectors]`; `@superdurable/dex-agents/connectors`, optional peer
  `@superdurable/connectors-sdk`). The application names each operation with
  `connector_tool` (`connectorTool`). Nothing is exposed by default, and an
  operation whose `dex-connector.json` entry lacks `agent.exposed: true` is
  refused when the agent is defined. Tool name, description, and argument schema
  come from the manifest.
- Effect classes: a Query is `READ` in `CallTool`; a Mutation with
  `agent.retry: safe` is `IDEMPOTENT_WRITE` in `CallTool`; any other Mutation is
  `NON_IDEMPOTENT_WRITE` in `CallSingleAttemptTool`. A call's tool Step gets the
  operation's Execute timeout and retry policy, at most three attempts. A
  session `tool_policies` entry changes only approval.
- Approval: `agent.approval: recommended` asks by default; other Mutations run
  without approval. `requires_approval` (`requiresApproval`) overrides it with a
  bool or a per-call predicate over the arguments.
- Arguments are checked against `inputSchema` before decoding. Branches map
  through `agent.branches`: success is `succeeded`, `rejected` and `defect` are
  `failed`, `uncertain`, `unknown`, and unlisted Mutation branches are
  `outcome_unknown`, and unlisted Query branches are `failed`. Results are
  canonical JSON, cut at 64 KiB by default with a note.
- A single-attempt write that asks to retry records `outcome_unknown`, unless its
  single-dispatch fence saved `notSent` or `canResend` in that attempt; then it
  records `failed` with "not sent; the provider asked to retry later".
- Connector writes of one model response run one at a time in the model's
  order. Once one is not `succeeded`, the later ones record `failed` with the
  new `AgentToolCall.skipped` marker.
- Needs Connector SDK 0.9.0 (`dex-connectors-sdk==0.9.0`,
  `@superdurable/connectors-sdk` `0.9.0`), which exports `raise_dex_retry` and
  `throwDexRetry`.
- Every Python and TypeScript framework adapter takes tool sources too; see
  Tool sources in framework adapters.
- Go core: no connector tools. Its **AgentToolCall** carries the four new fields, so a
  record written by another core round-trips.
- See [Connector tools](../docs/content/agent-frameworks.mdx#connectors-as-tools).

### Tool call contract additions

- `DeferredToolExecution.context`: the tool Step's live context, never stored.
- `ToolOutcomeUnknownError`: a tool records `outcome_unknown` and does not retry.
- `DeferredToolCall.ordered`, and `AgentToolCall` fields `ordered_write`,
  `write_queued`, `skipped`, and `truncated_output_bytes`, all defaulting to off.
  Records written by 0.2.0 decode unchanged. Dex Web reads `skipped`, and the
  "the tool did not run" error prefix of older records.
- `ToolExecutionResult.truncated_output_bytes`, `ToolCallStepOptions`, and the
  optional runner method `tool_call_step_options` (`toolCallStepOptions`), which
  `StepwiseAgentRunner` routes to a tool source.
- `RunModel` may move to itself once, to commit skipped writes before the model
  call, so the session graph gains that transition.

### Tool sources in framework adapters

- Every Python and TypeScript framework adapter takes `tool_sources`
  (`toolSources`), such as `ConnectorToolSource`, and
  `tool_source_listing_timeout` (`toolSourceListingTimeoutMs`), default 10
  seconds. Each `RunModel` lists every source and offers its tools with the
  source's name, description, and JSON Schema. A source whose listing fails or
  times out offers no tools in that model call.
- Every call of a source tool, reads included, runs once in its own tool Step
  through the source's `execute_deferred_tool_call`
  (`executeDeferredToolCall`), with the Step's context, heartbeat checkpoint,
  and attempt, so a connector call keeps its Call ID and fence. The source
  builds the call from the final input, so it decides the effect class, the
  approval, and the write order. `tool_call_step_options`
  (`toolCallStepOptions`) gives the tool Step the source's timeout and retry
  policy. A `tool_policies` entry for a connector tool changes only its
  approval.
- Definition fails when two sources share a `source_name` (`sourceName`), or a
  name a source declares repeats an agent tool, a built-in or control tool, or
  another source's tool. The same names listed at run time fail that
  `RunModel` attempt. Mastra resolves agent tools per request, so it checks
  them at `RunModel`.
- The Claude Agent SDK adapters, the TypeScript OpenAI Agents SDK adapter, and
  the Python Step-mode adapters keep a source tool that the turn already called
  but the current listing lacks as replay-only: its recorded call resumes, and
  a new call of it is refused inside the turn, so it never starts a tool Step
  under a new Call ID. A recorded result replays once, only under the tool name
  it was recorded for.
- The framework adapters poll no tool tasks: a source call that starts a task
  records `failed` for a `read` call and `outcome_unknown` otherwise. In the
  TypeScript adapters, a source call that asks a question records `failed`.
- Python and TypeScript Claude Agent SDK: `ClaudeAgentRunner` and
  `claude_agent_session_flow` (`createClaudeAgentSessionFlow`) take the tool
  sources, and the in-process custom tool server offers their tools after the
  custom tools. Source tools are served without MCP annotations,
  so the CLI runs them in the model's order; a later write of one reply is
  refused inside the turn, not recorded as `skipped`. An `allowed_tools`
  (`allowedTools`) entry for a source tool fails the attempt, because its calls
  must defer.
- TypeScript Vercel AI SDK, OpenAI Agents SDK, and Mastra:
  `createVercelAiAgentSessionFlow`, `createOpenAIAgentsSessionFlow`,
  `createMastraAgentSessionFlow`, and `MastraAgentRunner` take `toolSources`.
  The OpenAI Agents SDK adapter offers source tools to every agent of the graph
  as non-strict function tools, and maps names the SDK rewrites back to the
  source's name. Mastra offers them as client tools without `execute`.
- Python OpenAI Agents SDK, Pydantic AI, LangChain and LangGraph, and Google
  ADK: `OpenAIAgentsRunner`, `PydanticAIRunner`, `LangGraphAgentRunner`, and
  `AdkAgentRunner` and their session flow functions take `tool_sources`.
  Pydantic AI passes source tools as an `ExternalToolset`. The OpenAI Agents
  SDK adapter adds a gated function tool for each tool of a source's
  `cached_tools()` when it is built, so a source without `cached_tools` offers
  nothing; `ConnectorToolSource` caches its whole listing. LangGraph's
  `DurableToolCallMiddleware` adds the tools to each `create_agent` model
  request; a custom graph binds the new `get_current_tool_source_tools()` in its
  model node. Google ADK appends one `SourceToolset` to the `tools` of every
  `LlmAgent` of the tree when the runner has sources; it lists nothing outside
  the runner's `RunModel`.
- TypeScript core: `ToolSourceRouter`, `ToolSourceRouterOptions`,
  `ListedSourceTool`, and `defaultToolSourceListingTimeoutMs` are exported from
  the package root, with `ToolSource` and `SourceToolDefinition`. The Stepwise
  runner and the Vercel AI, OpenAI Agents, and Mastra adapters use it.
- Python core: `dex_agents.stepwise` adds `FrameworkToolSourceRouter`,
  `SourceToolListing`, `FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY`, and
  `SOURCE_TOOL_NOT_OFFERED_RESULT` for the Step-mode adapters.
- Stored values: a source call stores its source's name in
  `tool_source_reference` under an adapter key. The Python Step-mode adapters
  use the new key `dex_tool_source`. Keep each `source_name` stable across
  deploys, because stored calls are routed by it.
- See [Connector tools](../docs/content/agent-frameworks.mdx#connectors-as-tools).

### Claude Agent SDK: replay of recorded tool calls

- Python and TypeScript: an approved Claude Code tool, such as `Bash`, resumes
  with its recorded arguments as `updatedInput`: the input the application's
  hooks produced and the approval saw, not the model's original input.
  `can_use_tool` (`canUseTool`) answers a resumed call the same way.
- A recorded call replays only as its own tool, with the same name and kind (a
  custom or source tool, or a Claude Code tool), at most once per segment, and
  only while the stored transcript has no result for it. Another tool, a second
  replay, or a call whose ID an earlier segment already resumed is denied and
  never runs.
- Each tool use ID gets one PreToolUse decision per segment: allow, deny,
  defer, or a replay. A later PreToolUse for that ID is denied. A
  `can_use_tool` ask after a hook allowed the call is denied.
- No stored value changes.

### Changed

- Python and TypeScript Claude Agent SDK: the application's PreToolUse hooks are
  not called for a resumed call that already has a final record, so a later
  deny cannot hide its recorded result. The call passed them when it was
  deferred.
- TypeScript Claude Agent SDK: a call that the CLI deferred but the adapter's
  hook did not defer fails the segment, which then retries, instead of being
  recorded. Python already did this.
- TypeScript Claude Agent SDK: the in-process custom tool server is built with
  `@modelcontextprotocol/sdk`, a new direct dependency at `^1.29.0`, the range
  the Claude Agent SDK asks for. The custom tool listing is unchanged. Argument
  validation errors of inline custom tools now come from that SDK.
- TypeScript Mastra: the new `mastraRunToolNames` names the tools Mastra adds
  itself, such as `agent-researcher`, that may run inside `RunModel`. With
  `toolSources` or `mastraRunToolNames`, a `toolPolicies` key no longer allows
  such a tool by itself. Without either, nothing changes.
- TypeScript Vercel AI SDK: the `toolPolicies` type accepts any string key, so a
  source tool can have a policy. Without `toolSources`, a key that names no
  tool still throws `TypeError`.
- Python LangGraph: a model tool call without a tool call ID fails the
  `RunModel` attempt, because the session records each call by its ID.

## 0.3.0 (2026-11-09)

### Dex SDK 1.38.0

- Every package requires Dex SDK 1.38.0 (`dex-python-sdk==1.38.0`,
  `@superdurable/dex` `1.38.0`, Go `sdk-go` `v1.38.0`) and a Dex Server with
  protocol 4: server-zd v0.1.17 or later, as with 1.35.0.
- Agents CI, the nightly long run, and the publish workflows test against
  `dexcli` 1.38.0.

### The session graph for Dex Web

- Python and TypeScript cores: `render_session_flow_graph` (`renderSessionFlowGraph`)
  and the `dex-agents-session-graph` command render the Flow Definition Graph 2.0
  file of an agent's session Flow, with the application's Flow type and Action
  permission. Dex Web loads it with `--flow-rendering-dir`, draws the session
  Steps, and offers `resolve_interrupt`, `answer_question`, `steer_user_message`,
  and `cancel_turn` as Actions without a condition. Both cores write the same bytes.
- The graph is derived from the Python core's registered Flow and source and
  ships in both packages. The conformance suite adds `session_graph.json`, and
  each core's tests fail when the graph and the session Flow disagree.
- Dex Web needs a release that accepts Actions without a condition and calls no
  RPC for a view without fields.
- Go core: no graph. It registers only part of the session Flow.
- See [Show sessions in Dex Web](../docs/reference/agents/dex-web.mdx).

### Compaction in framework adapters

- The OpenAI Agents SDK (Python and TypeScript), Pydantic AI, LangChain and
  LangGraph, Google ADK, Vercel AI SDK, and Mastra adapters implement the
  compaction methods, so `AgentSessionOptions.compaction` works with them. A
  session compacts with the existing `CompactContext` Step and stores a
  `ContextSummary`, so retention can delete messages behind it. Without a policy
  nothing changes, and a session started on 0.2.0 starts compacting once its
  Workers have one.
- Adapters compact only between turns, before a turn's first model call. A model
  call that resumes a turn after its tool calls never compacts, because the
  OpenAI Agents SDK and LangGraph resume from a run state that holds the input.
- The adapters' session flow functions take `compaction`,
  `retained_message_limit`, and `summary_model` (`summaryModel`). LangGraph
  needs `summary_model`, because a compiled graph does not expose its model. The
  TypeScript OpenAI Agents and Mastra session flow functions now forward
  `compaction` and the message storage options to the core.
- Google ADK: the summary ends with a line that carries the ADK session state and
  the agent that replied last, so the rebuilt session keeps both.
- Claude Agent SDK: unchanged. Claude Code compacts its own transcript, and the
  adapter takes no compaction policy.
- Python and TypeScript cores: new helpers for adapters,
  `estimate_text_tokens`, `estimate_stored_input_tokens`,
  `select_turn_boundary_cutoff`, `describe_stored_message`, and
  `build_context_summary_prompt` (camelCase in TypeScript). The Stepwise summary
  texts move to the core's root module and stay exported from Stepwise.
- `CompactionPolicy.reserved_input_tokens` (`reservedInputTokens`), default 0,
  adds tokens the runner's estimate cannot see.
- `dex_agents.testing` and `@superdurable/dex-agents/testing` add
  `ContextWindowModelScript` and `ContextWindowSessionScenario`.
- No stored value changes. The conformance suite adds the summary prompt texts.
- Go core: unchanged.
- See [Compaction and the storage bound](../docs/reference/agents/session-limits.mdx#compaction).

### Spend limits

- Python and TypeScript cores: the new `ModelSpendLimit` interface is an
  application's spend limit. Pass it as `AgentSessionOptions.spend_limit`
  (`spendLimit`), or on a Stepwise `AgentDefinition`. The session calls
  `check_model_call` before each attempt of `RunModel` and `CompactContext`, and
  `report_model_usage` when the attempt's model call returns. Without one, nothing
  changes. Counters and limits belong to the application.
- A refused call ends the turn. `AgentState.last_turn_error` starts with the new
  `MODEL_CALL_REFUSED_ERROR_PREFIX` (`modelCallRefusedErrorPrefix`), then the reason.
  The session keeps running and checks again on the next message.
- A check that raises or takes longer than `SPEND_LIMIT_CALL_TIMEOUT`, 30 seconds,
  fails closed: it fails the attempt, and the Step retries. A report that raises is
  logged and dropped. `ModelUsageReport.report_id` is stable per attempt.
- Python Claude Agent SDK adapter: `claude_agent_session_flow` takes `spend_limit`.
  A harness is checked once per turn segment, and its usage arrives when the segment
  ends.
- No stored value changes. The conformance suite adds the refusal prefix.
- Go core: unchanged. It does not check spend limits.
- See [Spend limits](../docs/reference/agents/spend-limits.mdx).

### A configurable RunModel limit

- Python and TypeScript cores: `AgentSessionOptions.run_model_limit`
  (`runModelLimit`) sets the method timeout and attempts of `RunModel`, with the
  new `RunModelLimit`. The retry budget moves with them: the method timeout times
  one less than the attempts, and at least twice the method timeout. The default
  stays 15 minutes and 3 attempts in 30 minutes. `CompactContext` and the heartbeat
  timeout do not change.
- Python Claude Agent SDK adapter: `claude_agent_session_flow` takes
  `run_model_limit`. The TypeScript adapter takes it through its session options.
  A turn segment that needs more than 15 minutes can now finish. A failed segment
  reruns whole.
- No stored value changes. A running session gets the limit on its next `RunModel`
  after the Worker restarts.
- Go core: unchanged.

### Sessions are searchable by the Step they wait in

- Python core: `AgentSessionClient.start_session` sets
  `FlowConfig.active_step_search_mode` to the new
  `AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE` (`WITH_WAIT_FOR`), so
  `ActiveStepTypes = "AwaitToolApproval"` and `"AwaitQuestionAnswer"` find a
  session while it waits and drop it when it moves on. A mode set in
  `options.config_override` wins, including `DISABLED`.
- TypeScript core: the new `startAgentSession(client, flow, sessionId, input,
  options)` does the same, with the new `agentSessionActiveStepSearchMode`. Until
  this core supports a Dex SDK with `ActiveStepSearchMode.WITH_WAIT_FOR`, the
  value is `ActiveStepSearchMode.DEFAULT`, which the server treats the same way.
  `AgentSessionTestClient.startSession` calls it.
- `Client.startFlow` and `AsyncClient.start_flow` are unchanged, so a session they
  start stays out of this search. So do sessions started on 0.2.0, until
  `UpdateFlowConfig` turns it on. No stored value changes.
- Go core: unchanged.
- See [Find sessions that need a person](../docs/reference/agents/approvals.mdx#find-sessions).

### A session at the state limit fails, with the reason

- Python and TypeScript cores: when a Step fails on
  `limits.maxFlowStateBytes`, the session ends. `RecordTurnFailure`,
  `RecordToolFailure`, and `RecordToolOutcomeUnknown` recognize the recovery
  error (`ValueTooLarge` with the text `run state of <X> bytes exceeds the
  maximum of <Y> bytes`, or `active run state of ...` from a server that counts
  only active state), set the new `AgentState.status` value `failed` with that
  text in `last_turn_error`, and close the Flow `FAILED` with the same text in
  the same commit. A session no longer goes on to pay for model calls whose
  answers cannot be stored. A value over `values.maxValueBytes` still fails only
  its turn.
- That commit is checked against the same limit, and it can fail too. The Flow
  then closes `FAILED` with the text anyway and `AgentState` keeps its last
  value, which is also what happens for a Step with no failure route, such as
  `AwaitUserMessage`. `AgentSessionClient.get_agent_snapshot` and the new
  `read_agent_snapshot` (`readAgentSnapshot`) read the Flow's close, so they
  report `failed` and the reason for any session that stored a state, for one
  more call to Dex. A session that fails before it stores a state keeps a
  snapshot with no state. The `get_agent_snapshot` RPC alone reads only what was
  stored.
- UI streams: the event SSE generators end once the Flow failed and every event
  was sent, instead of sending keep-alives forever. A TypeScript UI message
  stream that finds the Flow failed ends with an `error` part with the reason
  and `finishReason` `error`. `AgentSessionUIMessages` has a new `failure` field.
- New messages are refused. `AgentSessionClient.send_user_message` and
  `steer_user_message`, and the new `sendAgentUserMessage`, raise the new
  `AgentSessionFailedError`, a `FlowNotActiveOrNotFoundError` with `reason`. A
  direct RPC call still gets `FlowNotActiveOrNotFoundError`.
- Queued messages never run. They stay readable in
  `AgentSnapshot.queued_messages` until retention deletes the session.
- Go core: reads `failed` and keeps its behavior. It does not end a session at
  the limit yet.
- See [Upgrade agent code while sessions wait](../docs/reference/agents/upgrades.mdx)
  for the order of a rolling deploy.

## 0.2.0 (2026-11-05)

### Tool calls that never ran

- All three cores: `RecordToolFailure` and `RecordToolOutcomeUnknown` read the
  recovery error's `attempt_progress`. When it is `NOT_STARTED`, no attempt of
  the tool Step reached a Worker, so the tool never ran. The call then records
  `failed`, not `outcome_unknown`, with an `error` that starts with the new
  `TOOL_CALL_NOT_RUN_ERROR_PREFIX` (`toolCallNotRunErrorPrefix`,
  `ToolCallNotRunErrorPrefix`), `the tool did not run, so it is safe to call
  again`, then `": "` and the recovery detail. The model sees an ordinary
  failed call and can call the tool again. No final status is added.
- `NOT_COMPLETED`, and `UNSPECIFIED` from a server that reports no progress,
  keep the 0.1.0 behavior: a read records `failed` and any other call
  `outcome_unknown`. So do task polls, `CancelToolTask`, and a call whose tool
  already asked a question, because their tool already ran.
- This needs the Dex SDK release that adds `RecoveryErrorInfo.attempt_progress`
  (`attemptProgress`, `AttemptProgress`).

### Dex SDK 1.35.0

- Every package requires Dex SDK 1.35.0 (`dex-python-sdk==1.35.0`,
  `@superdurable/dex` `1.35.0`, Go `sdk-go` `v1.35.0`) and a Dex Server with
  protocol 4: server-zd v0.1.17 or later.
- The testing exports' private engines (`dex_agents.testing`,
  `@superdurable/dex-agents/testing`, Go `agentstest`) run `dexcli dev` with
  a `-zd-config` file that sets `dev.durabilityMode: processCrashSafe`, so
  tests stay fast now that `dex-server-zd` 0.1.18 defaults to `strict`. A
  configuration file you pass replaces it; set `dev.durabilityMode` there
  too.

### MCP tool source

- `dex_agents.mcp` (extra `dex-agents[mcp]`, `mcp>=2.3,<3`) and
  `@superdurable/dex-agents/mcp` (optional peer `@modelcontextprotocol/sdk`
  `^1.32.2`): `McpToolSource` lists the tools of MCP servers configured on the
  Worker, over stdio or Streamable HTTP, and runs each call in its own tool
  Step. `readOnlyHint` makes a tool `read`, `idempotentHint` makes it
  `idempotent_write`, and an explicit `ToolPolicy` wins. A call whose response
  never arrived is re-issued only for `read` and `idempotent_write` tools;
  other tools record `outcome_unknown`. A task of a Streamable HTTP server
  that any connection can poll becomes a tool task polled by `PollToolTask`
  (see Tool tasks); a stdio task, and in TypeScript a session-bound task, is
  polled inside the tool Step with heartbeats and a bounded wait. A retried
  attempt resumes the same task once its checkpoint reached Dex. In Python, a
  task's single form elicitation becomes a session question; its answer,
  decline, or passed deadline goes back with `tasks/update` (`accept`,
  `decline`, `cancel`), once, from a poll's first attempt, and only to the
  request it answers; a retried attempt asks the user again. An identical
  request after the task left `input_required`, or 10 seconds after the
  answer, is asked as a new question. Other `input_required` answers record
  `failed`. `mcp.task_status` is written only
  when a task's status changes. TypeScript requests a 24-hour TTL,
  `taskTtlMs`, for a polled task.
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

### Stepwise tool sources, MCP resources and prompts, and the demo model

- Both cores: `AgentDefinition.tool_sources` (`toolSources`) takes tool
  sources, such as `McpToolSource`, through the new `ToolSource` and
  `SourceToolDefinition` protocols in `dex_agents.stepwise` and
  `@superdurable/dex-agents/stepwise`. In every `RunModel` of a chat or
  executing turn, the runner lists each source's tools and offers them after
  the function tools and before the built-in tools. A call of a source tool
  becomes the source's `deferred_tool_call`, so its policy, effect class,
  approval, idempotency key, and `tool_source_reference` apply, and it runs
  through the source's `execute_deferred_tool_call` in its own tool Step. Each
  source has a unique, stable `source_name` (`sourceName`), and the runner
  stores the source's reference as `{"stepwise_tool_source": <source name>,
  "reference": ...}`, so the tool Step and `PollToolTask` reach the source
  that built the call without a listing and whatever the source order; a
  source name the Worker lacks raises a retryable error. The runner passes the
  definition it offered as `listed_tool` (`listedTool`), so a refresh during
  the model call cannot change it. `McpToolSource` defaults its source name to
  `"mcp"` (`DEFAULT_MCP_SOURCE_NAME`, `defaultMcpSourceName`), which adding
  or removing a server keeps; two `McpToolSource`s in one agent must name at
  least one. `StepwiseAgentRunner` now implements `poll_tool_task`
  (`pollToolTask`) and passes each task to its source; function tools start
  no tasks. A name that no listing offered fails as an unknown tool without an
  approval and without reaching a source. Each listing is
  bounded by `tool_source_listing_timeout` (`toolSourceListingTimeoutMs`, 10
  seconds), and a source that fails or times out offers no tools in that
  model call instead of failing the turn. A listed name that repeats a
  function tool, an enabled built-in tool, or another source's tool fails the
  `RunModel` attempt before any model request, as a built-in name collision
  does; names a source declares with the optional `declared_tool_names()` are
  checked when the definition is built. The optional `cached_tools()` feeds
  the token estimate.
- Both cores: `McpToolSource` caches each server's listing per Worker,
  refreshes it in the background after `listing_ttl` (`listingTtlMs`, 5
  minutes), and leaves a server that failed to list alone for
  `listing_failure_backoff` (`listingFailureBackoffMs`, 30 seconds). One
  listing per server runs at a time.
- Both cores: `McpToolSource(..., resource_and_prompt_tools_enabled=True)`
  (`resourceAndPromptToolsEnabled: true`) also lists the `read` tools
  `mcp_list_resources_and_prompts`, `mcp_read_resource`, and `mcp_get_prompt`,
  which take a configured server's name and reuse the source's transport and
  headers, with the source's connection attempts. Off by default; two sources
  with them are refused when the agent definition is built. The names,
  descriptions, argument schemas, and output JSON (sorted keys, whole numbers
  without `.0`) are the same in both cores. TypeScript needs nothing beyond
  `@modelcontextprotocol/sdk` 1.32.2 for them.
- Both cores: `ScriptedModelClient`, `ScriptedModelReply`, `ScriptedToolCall`,
  `ScriptedModelResponder`, and the scripted provider and API constants are
  exported from `dex_agents.stepwise` and `@superdurable/dex-agents/stepwise`
  as a supported model client for running an agent without a model key, such
  as an application's demo mode. `requests` keeps the latest 100 requests,
  `max_recorded_requests` (`maxRecordedRequests`). The `testing` exports and
  module paths stay and name the same classes.

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
### Steering and turn cancellation

Both cores, with identical Step types, RPC names, wire values, events, and
model-visible text. Nothing changes until an application calls the new RPCs.

- `steer_user_message` (`SteerUserMessageRequest`, with a queued message's
  `message_id` or new `text`) steers the running turn, and
  `delete_queued_message` (`DeleteQueuedMessageRequest`) removes a queued
  message. Both are transactional and lock nothing: a message consumed while
  the RPC runs fails it with `ChannelMessageNotFoundError`, and nothing
  commits. While no turn is active, `steer_user_message` with `text` queues
  the message. `cancel_turn` (`CancelTurnRequest`) stops the running turn with
  no new turn; `true` means the request is recorded, and a turn whose model
  call already committed its final answer still completes. It locks
  `TurnCancellationRequest` and `PendingInterrupts`, so concurrent cancels
  apply in order, and a second cancel of the same turn changes nothing.
- `cancel_turn` cancels the running model call or compaction (Python cancels
  the coroutine, which stops the LiteLLM call; TypeScript aborts
  `cancellationSignal`, which the AI SDK client passes as `abortSignal`), and
  every Step that waits for an approval, an answer, a durable wait, or a
  task's next poll. The new `FinishTurnCancellation` Step decides from
  committed state how the turn ends: through `ApplyTurnControl` when the model
  call was cancelled, or through `ResumeTurn` once every started tool Step
  finishes. A started tool task is abandoned: the new `CancelToolTask` Step
  calls the optional `ToolTaskCanceller.cancel_tool_task`
  (`cancelToolTask`), with `ToolTaskCancellation`, best-effort and records
  `outcome_unknown` with `cancellation_reason` `turn_cancelled` and
  `TOOL_TASK_ABANDONED_ERROR`, even when the cancellation keeps failing. The
  Stepwise runner forwards it to a tool source's optional `cancel_tool_task`,
  and `McpToolSource` sends `tasks/cancel`. A tool call Step that has started
  still runs to the end.
- A steered turn ends at its next safe Step boundary. A model
  call or tool call that has started finishes. Calls that have not started
  record `rejected` with the new `AgentToolCall.cancellation_reason`
  (`steered` or `turn_cancelled`) and the result
  `{"status":"cancelled","reason":"superseded_by_steered_user_message"}` or
  `{"status":"cancelled","reason":"turn_cancelled_by_user"}`. Final statuses do
  not change. The new `ApplyTurnControl` Step ends the turn, stores the
  runner's unfinished-turn messages or a steered or cancelled note, and starts
  a turn with every steered message, in order. That turn is a planning turn
  when any steered message has `plan_mode` or a draft plan exists, as for a
  queued message, and `execute_plan` returns false while a steered message is
  pending. A `write_todos` call settles in `RunModel`'s own commit, so one
  that a model call made while the user steered still replaces the plan.
- New persistence: the `SteeredUserMessages` Channel, the
  `TurnCancellationRequest` Attribute, and the internal `TurnControlRequests`
  Channel and `ToolCallCancellations` ChannelMap. `AgentState` gains
  `accepts_turn_control` (`acceptsTurnControl`), which a turn that 0.2.0
  started carries. `ResumeTurn` now waits on `TurnControlRequests` too and
  locks `PendingInterrupts`.
  `AwaitToolApproval`, `AwaitQuestionAnswer`, and `AwaitTimer` also wait on
  their `ToolCallCancellations` instance. `AwaitUserMessage` wakes on a steer
  or cancellation.
- `get_agent_snapshot` returns `queued_messages` and `steered_messages`
  (`PendingUserMessage` with the Channel message ID) next to
  `queued_message_count`. New events `turn_steered` and `turn_cancelled`;
  `interrupt_resolved.cancellation_reason` is set when steering or
  cancellation removes an interrupt. New constants
  `TURN_STEERED_NOTE_HEADER`, `TURN_CANCELLED_NOTE_HEADER`,
  `TOOL_CALL_CANCELLED_BY_STEERING_RESULT`, and
  `TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT` (camelCase in TypeScript).
- `AgentSessionClient` gains `steer_user_message`, `delete_queued_message`,
  and `cancel_turn`; the TypeScript `AgentSessionTestClient` gains
  `steerUserMessage`, `steerQueuedMessage`, `deleteQueuedMessage`, and
  `cancelTurn`. The TypeScript UI message stream ends a steered or cancelled
  turn with `finish` reason `other`.
- `build_unfinished_turn_messages` (`buildUnfinishedTurnMessages`) is also
  called when a steered or cancelled turn ends. A runner that keeps its items
  in run state may implement it to keep its exact transcript.

### Tool tasks

Both cores, with identical Step types, wire values, events, and error texts
(the missing-poller text names each language's method). Inert until a runner
returns the new outcome.

- `execute_deferred_tool_call` may return `ToolTaskStarted(task_id,
  poll_interval_seconds)` (`{ kind: "tool_task_started", taskId,
  pollIntervalSeconds }`) when a tool started remote work. The tool Step
  records the new non-final status `waiting_for_task` with the new
  `AgentToolCall` fields `task_id` and `task_poll_interval_seconds`, writes the
  new `tool_task_started` event, and moves to the new `PollToolTask` Step.
- `PollToolTask` waits on a whole-second Timer and calls the runner's
  `ToolTaskPoller.poll_tool_task(ToolTaskPoll)` (optional `pollToolTask` in
  TypeScript) once per execution. It returns a `ToolExecutionResult`, a
  `ToolTaskPending(poll_interval_seconds, continuation)`, whose continuation
  the next poll reads as `input_continuation`, or a `ToolInputRequested`,
  which asks through `AwaitQuestionAnswer`. A task's question never ends the call:
  an answer, a decline, and a passed deadline each come back to `PollToolTask`
  at once as a `QuestionAnswer` with the new `resolution` field (`answered`,
  `declined`, `deadline_passed`), handed to the runner once as
  `ToolTaskPoll.pending_answer` while the new record field
  `task_answer_pending` is set. A poll gets three attempts, then the call's
  failure route by effect. Each poll is an `execute_tool` span. `PollToolTask`
  takes no lock, so steering waits for a started task and keeps its
  questions; a call with a task is never recorded `rejected`. `cancel_turn`
  abandons the task (see Steering and turn cancellation).
- `DeferredToolExecution.supports_tool_tasks` (`supportsToolTasks`) tells a
  tool whether the runner polls tasks; the MCP sources poll inside the tool
  Step without it.
- A runner without the poll method that returns `ToolTaskStarted`, a blank
  `task_id`, or an interval that is not a number records `failed` for a `read`
  call and `outcome_unknown` otherwise (`MISSING_TOOL_TASK_POLLER`,
  `BLANK_TOOL_TASK_ID`, `INVALID_TOOL_TASK_POLL_INTERVAL`).
- `DeferredToolOutcome` gains `ToolTaskStarted`.

### Context compaction

Both cores, with identical Step types, persistence names, wire values, events,
and model-visible text (plan section A7). Inert until an application sets a
compaction policy.

- `AgentSessionOptions.compaction` takes a `CompactionPolicy`
  (`context_window_tokens`, `trigger_ratio` 0.85, `retained_ratio` 0.3;
  TypeScript `contextWindowTokens`, `triggerRatio`, `retainedRatio`). It needs
  a runner with the compaction capability, Python `ContextCompactionRunner`
  and TypeScript's optional `estimateInputTokens`, `selectCompactionCutoff`,
  and `summarizeContext`; the Flow constructor refuses it otherwise.
- Before a model call that would cross the threshold, `RunModel` moves to the
  new `CompactContext` Step instead of calling the model. `CompactContext`
  makes one summary call, with `RunModel`'s timeouts, retries, and failure
  route, and writes the new `ContextSummary` Attribute; a crash reruns only the
  summary call. From then on the runner receives only the messages after the
  summary, plus the summary in `AgentTurnContext.context_summary`
  (`contextSummary`). Summarized messages stay stored until retention deletes
  them; see Chunked messages and retention.
- Stepwise implements the capability, with `AgentDefinition.compaction`. Its
  model input starts with `[Dex] Summary of the earlier conversation:` and the
  summary text, it cuts only between model responses whose tool calls all have
  stored results, so a long turn compacts mid-turn without separating a tool
  call from its result, and its summary call streams no text. When the summary
  request plus the configured `max_output_tokens` would exceed
  `ContextSummaryRequest.input_token_budget` (`inputTokenBudget`), the
  policy's window, it leaves out the oldest messages up to a cut that never
  splits a model response, and says so with
  `[Dex] <n> earlier messages did not fit in this request and are left out.`
  (Python `build_context_summary_omission_note`, TypeScript
  `buildContextSummaryOmissionNote`).
- Each `CompactContext` attempt emits a `chat` span for the summary call, as
  `RunModel` does, named from the optional
  `ContextSummaryResult.model_call_metadata` (`modelCallMetadata`).
- The `message_count` of `turn_completed`, `turn_steered`, and
  `turn_cancelled` counts every message the turn appended, also those a
  mid-turn compaction summarized. A steer or cancellation that arrives during
  `CompactContext` applies at the next `RunModel`, before any model call.
- New wire values: `AgentState.status` `compacting_context`; `ContextSummary`;
  `CompactContextInput`; `AgentSnapshot.context_summary`; the
  `compaction_started` and `compaction_finished` events. The browser snapshot
  JSON of both UI stream modules carries `context_summary`, and the AI SDK UI
  message stream keeps the summary call's usage out of the message's steps.
- Python: `AgentSessionStatus` gains `COMPACTING_CONTEXT`, so an exhaustive
  `match` over the status must handle it. TypeScript: `AgentSessionStatus`
  gains `"compacting_context"`. A 0.1.0 client cannot decode a snapshot taken
  during compaction, so upgrade the clients that read snapshots before enabling
  it.
- The 2,000-turn compaction sessions that calibrate the deployment guide run
  in the new `Agents Long Tests Nightly` workflow: Python with
  `DEX_AGENTS_LONG_TESTS=1`, TypeScript with `npm run test:long`. `Agents CI`
  runs an 800-turn guard in each core instead.

### Chunked messages and retention

- Messages are stored as a sequentially chunked AttributeMap in every core.
  `AgentMessages` holds only the newest messages, one instance per message.
  When it holds more than `message_window_size` (`messageWindowSize`,
  `WithMessageWindowSize` in Go; 64) messages, the oldest move into immutable
  instances of the new `AgentMessageChunks` AttributeMap, keyed by their first
  sequence, in the same commit. A chunk holds at most `message_chunk_size`
  (32) messages and `message_chunk_max_bytes` (`messageChunkMaxBytes`; 1 MiB,
  at most 8 MiB) of encoded JSON. A larger message skips the window and goes
  straight into its own chunk when it is stored, so a message too large for
  the Server fails its turn at once and never wedges a later commit. One
  commit moves at most 4 chunks and 8 MiB. The new `AgentMessageIndex`
  Attribute lists every kept chunk's first and last sequence, the window
  start, and the oldest kept message; it changes only when chunks are written
  or deleted. The new `TurnReplySequence` Attribute records where the latest
  turn's replies start.
- `RunModel` no longer loads the whole message map. With compaction, it loads
  the window and only the chunks after the summary; the snapshot and
  `list_agent_messages` load the window and every kept chunk.
- With compaction, retention deletes the oldest chunk while every message in
  it is summarized and at least `retained_message_limit`
  (`retainedMessageLimit`; 1,000; `None`/`null` keeps every message) messages
  are stored after it, so Flow state stays bounded and the newest 1,000
  messages always stay. A deleted message leaves `list_agent_messages` and
  `get_agent_snapshot`. Their last page now starts at the oldest stored
  message, and the new `AgentMessagesPage.deleted_before_sequence`
  (`deletedBeforeSequence`) marks the deleted range. Copy messages out first
  if the application needs the whole transcript. A runner without the
  compaction capability on a session with deleted messages logs a warning on
  each model call. The Go core has no compaction, so it never deletes
  messages.

### Go core

- New module `github.com/superdurable/dex/agents/go/core` (package
  `dexagents`), the Go core on the shared session contract: the session Flow
  and its Steps, persistence, RPCs (`send_user_message`, `resolve_interrupt`,
  `get_agent_snapshot`, `list_agent_messages`), the `AgentEvents` Stream,
  effect classes and tool policies, approvals and approval deadlines, the
  turn-failure note, and the `AgentFrameworkRunner` contract with incremental
  turn messages (`ToolCallsRequested.NewMessages` and
  `UnfinishedTurnMessageBuilder`). It registers every persistence name of the
  contract and decodes every value the other cores write. Built-in tools,
  questions, durable waits, steering, plans, compaction, tool tasks, MCP,
  spans, UI streams, and framework adapters are not planned for Go. A Go
  Worker writes
  `accepts_turn_control` false, so steering RPCs refuse its turns.
- Stepwise mode in `stepwise`: `NewAgentDefinition`, `NewFunctionTool` and
  `NewTypedFunctionTool` with the Python core's argument checks and messages,
  `StepwiseAgentRunner`, and `NewStepwiseAgentSessionFlow`. Model clients on
  the official SDKs: `openaimodel.NewResponsesModelClient` (`openai-go`,
  provider `openai`, API `responses`, the same items as the Python LiteLLM
  client) and `anthropicmodel.NewMessagesModelClient`
  (`anthropic-sdk-go`).
- `agentstest`: a scripted model client whose items match the Python core's,
  a private `dexcli dev` engine, killable Worker processes, and Step history.
- Clients interoperate across languages: Python and TypeScript Clients drive
  sessions on Go Workers, and a Go Client drives one on Python Workers, tested
  against a real engine. Serve each Flow type with Workers of one language: a
  Go Step that finds state from steering, plan mode, or compaction
  fails the session with a reason instead of acting on it. RPC names come from
  `dex.RPCOptions.Name`, which needs a Go SDK release after `sdk-go/v1.32.0`.
- The Anthropic client stores the LiteLLM chat message that the Python core
  stores (`anthropic`, `chat_completions`), so either core replays the other's
  thinking blocks, and it replays the TypeScript core's AI SDK Anthropic items
  with their signatures. An empty response stores no item.

### Fixes

- TypeScript: `send_user_message`, the `AwaitUserMessage` Step that takes its
  messages, and `StartAgentSession`'s initial message check for blank text with
  Python's `str.strip()` whitespace, as the Python core does. Text made only of
  U+001C to U+001F or U+0085 is now blank, and text made only of U+FEFF is now
  a message. 0.1.0 used `String.trim()`.
- MCP server configurations no longer print secrets. In Python,
  `repr` and `str` of `McpStdioServerConfig` and `McpStreamableHttpServerConfig`
  show `env` and `headers` names with every value replaced by `***`. In
  TypeScript, `util.inspect` and `console.log` of an `McpToolSource` show its
  servers the same way. The values sent to the servers are unchanged.
- TypeScript: `send_user_message` is a transactional RPC, as the contract and
  the Python core declare it.
- Python: `ScriptedModelClient` writes non-ASCII text in tool arguments and
  output items unescaped, as the TypeScript client and Python function tools
  do.
- Both cores: the text a model sees for an MCP tool result renders structured
  content and non-text content blocks with sorted keys, `json.dumps` spacing,
  and non-ASCII unescaped, as the MCP resource and prompt tools already did.
  Python wrote them with escaped non-ASCII, and TypeScript wrote them compactly.
- TypeScript: MCP resource and prompt tool output sorts integer-like keys by
  code point, as Python does. It put them first, in numeric order.
- Python: the test engine in `dex_agents.testing` tries three times on new
  free ports when another process takes its port, as the TypeScript one does.

### Conformance suite

- `agents/conformance/` holds golden wire values, the names manifest, the
  model-visible text table, and scripted-turn transcripts that every core
  must reproduce. Both cores run it in their CI suites.

### Compatibility

- **Upgrade rule: stop every 0.1.0 Worker of a Flow type before any 0.2.0
  Worker of it starts. Do not run them side by side.** Once a 0.2.0 Worker
  stores a message in a 0.1.0 session, it moves the session's older messages
  from `AgentMessages` into `AgentMessageChunks`, at most four chunks a Step.
  If a 0.1.0 Worker then runs a Step of that session, its turns fail,
  because it cannot read the moved messages. No stored message is lost.
- Enable the new options and call the new RPCs only after every Worker of the
  Flow type runs 0.2.0. A session started on 0.1.0 continues on 0.2.0.
- Code that read a message with
  `get_attributes(AGENT_MESSAGES.instance(message_sequence_key(n)))` finds only
  the window there; read messages with `list_agent_messages` or
  `get_agent_snapshot`.
- `StepwiseAgentRunner` now implements `ToolTaskPoller`, so its
  `DeferredToolExecution.supports_tool_tasks` (`supportsToolTasks`) is true. A
  subclass that already defined `poll_tool_task` keeps its own. Function tools
  cannot start tasks, so a Stepwise agent without tool sources behaves as in
  0.1.0.
- Call `steer_user_message` and `cancel_turn` only after every Worker runs
  0.2.0 and every turn that was active during the rollout has ended. A Step's
  waits and locks come from the Worker that scheduled it, so such a turn can
  hold a wait that a steer never wakes. The RPCs refuse a turn that an earlier
  Worker started or advanced (`AgentState.accepts_turn_control` is false), and
  a steer while no turn is active is queued, which every Worker's idle wait
  takes. A turn that started on 0.2.0 during a mixed rollout is not refused.
- `ResumeTurn` now locks `PendingInterrupts` in its short Execute, so
  `resolve_interrupt` and `answer_question` can raise `RpcLockConflictError`
  in sessions that never steer. `AgentSessionClient` retries it for 5 seconds;
  direct `invoke_rpc` callers should retry it too.
- Python: `ToolCallStatus` gains the three non-final members, so an exhaustive
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
  status also allows the non-final `awaiting_answer`, `waiting_for_timer`, and
  `waiting_for_task`;
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
