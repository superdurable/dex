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
  `ModelCallMetadata`. The adapters do not return it yet; wiring each adapter
  is a follow-up.

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
