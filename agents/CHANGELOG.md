# Changelog

All twenty-six published Dex agents packages share this changelog and one version line. A
release is tagged `agents/v<version>`. See [README.md](README.md#versions-and-compatibility)
for the Dex SDK and framework versions each release supports.

## 0.6.16 (2026-11-22)

### Connectors as tools in the Go core

- Go `connectortools` exposes `sdkgo.AgentTool` operations via `NewConnectorTool` and `NewConnectorToolSource`. Pass the source to `builtinloop.WithToolSources`. Requires `github.com/superdurable/dex-connectors-library/sdkgo` v0.39.0+. [Ref](../docs/reference/agents/connector-tools.mdx#install)
- `ToolSourceRouter.ExecuteDeferredToolCall` runs Go source calls once per tool Step attempt, before framework tool nodes, middleware, or handlers. Dex blocks a second router call and records the source's outcome and error regardless of the runner's return.
- Go checks `InputSchema` before calls and caps JSON at 64 KiB. Success is **succeeded**, **uncertain** or marked **unknown** is **outcome_unknown**, and other branches are **failed**. Retry fails the attempt after a whole-second delay. [Ref](../docs/reference/agents/connector-tools.mdx#outcomes)
- Go `limited` Mutations pass pinned `MaxSends` or `ToolPolicy.MaxSends` (1–10) via `sdkgo.WithMaxSends` each attempt. Connector writes run serially in reply order, marking later ones **failed** and `skipped` if one is unsuccessful. [Ref](../docs/reference/agents/connector-tools.mdx#bounded-resends)
- `ToolSource`, `ToolSourceRouter`, and `SourceToolListing` list Go tools at runtime. Go adds `DeferredToolCall.Ordered`, `ToolOutcomeUnknownError`, `ToolExecutionResult.TruncatedOutputBytes`, and `ToolCallStepOptionsProvider`. `ResolveToolPolicy` keeps explicit `MaxSends`. No approval display yet.
- Stop or drain all Go Workers at 0.6.15 or earlier for a Flow type before serving it with tool sources. Roll back below 0.6.16 only after queued Connector writes and in-flight Connector calls finish. Older Workers can wait forever or record a write retry **failed** rather than **outcome_unknown**.

### Eino adapter

- New Go module `github.com/superdurable/dex/agents/go/eino` (package
  `dexeino`) runs CloudWeGo Eino ADK `ChatModelAgent`s on the Go core's
  session Flow. Pass the `*adk.ChatModelAgentConfig`, or an
  `AgentConfigBuilder`, to `NewAgentSessionFlow`. See
  [go/eino](go/eino/README.md).
- Each `RunModel` builds a fresh Eino agent over the stored messages and
  stops it after one model call, so the current deploy's instruction and
  tools apply to every model call.
- Each tool call runs in its own Dex tool Step through a one-call
  `compose.ToolsNode` with the agent's tool middlewares and Handler wrappers,
  where `compose.GetToolCallID` and `ToolCallContextFrom` work.
- The tool Step checks each call's arguments, after Eino's `ToolAliases` and
  `ToolArgumentsHandler`, against the tool's current schema with the built-in
  loop's JSON Schema subset and texts, so a stored call whose parameter a
  deploy renamed is refused before any tool code runs.
- Every tool needs a Dex effect: `WithToolEffects`, `WithToolPolicies`,
  `ToolEffectDeclarer`, or `WithDefaultToolEffect`.
- Construction refuses `ModelRetryConfig`, `ModelFailoverConfig`, `Exit`,
  `ReturnDirectly`, `ToolsConfig.ExecuteSequentially`, the summarization,
  automemory, and dream middlewares, and agent tools made with
  `adk.WithFullChatHistoryAsInput`.
- The README lists how every middleware and prebuilt agent that Eino
  v0.9.23 ships runs under Dex, and the integration tests run each one that
  runs in `Generate` and streaming mode. A before-model hook that clears a
  tool result in place changes only that model call's input.
- Eino's plantask calls run in the model's order, one write at a time, as
  their shared lock makes them in one Eino run. A `ToolExecutionError` from a
  tool of an agent tool's or fork skill's sub-agent fails a call whose effect
  is not `read`, so its effect decides the outcome.
- The adapter finds Eino's agent, skill, and plantask tools also inside a
  type that embeds one, such as a `ToolEffectDeclarer` that gives an agent
  tool its effect, so the type keeps the Eino tool's sub-agent failure
  handling, history refusal, and call order.
- A sub-agent that a tool runs, such as an agent tool's, gets no
  `ToolCallContext` in its tools, model, Handlers, or `ToolArgumentsHandler`,
  so its inner calls never share the agent tool's `IdempotencyKey`.
- A stop that a retry would repeat ends the turn as failed with
  `TurnCompleted.TurnError`, with no paid retry: a second model call in one
  Step, a Handler that answers from another model, a turn at
  `MaxIterations`, or an after-model hook that changes the agent state.
- `WithToolSources` offers a tool source's tools, such as
  `connectortools.ConnectorToolSource`, beside the agent's own, and hands
  Eino their input schemas as the source wrote them. Each call runs through
  its source in its tool Step, outside Eino's tool node and middlewares.
- Empty, blank, `/`-containing, and duplicate tool call IDs are replaced,
  and unknown tools are answered as unknown. Model errors reach the core's
  classifier with their HTTP status (`HTTPStatusCode` directly, genai `Code`
  through `ModelAPIError`), so a 429 backs off and a 400 ends the turn.
- It requires `github.com/cloudwego/eino` v0.9.23 or later, tested with
  v0.9.23 and v0.10.0-alpha.35.

### Go core

- Go **RunModel** reads `HTTPStatusCode` int in the error chain, backing off transient statuses and failing other HTTP 4xx at once. These field-only errors have no **Retry-After**, and `Code` is still ignored. [Ref](../docs/reference/agents/workers.mdx#model-api-errors)
- Go **RunModel** fails without retry on roles besides `user`, `assistant`, or `tool` in `TurnCompleted.NewMessages` or `ToolCallsRequested.NewMessages`. It records usage and **last_turn_error**. `MessageRole.IsValid` checks roles. [Ref](../docs/reference/agents/changelog.mdx#v0-6-16)
- Go sessions fail when a Step commit would exceed `limits.maxFlowStateBytes`. If the failure route can commit, it records **failed**, **last_turn_error**, and **turn_failed** and clears the turn. [Ref](../docs/reference/agents/session-limits.mdx#at-the-limit)
- `AgentSessionClient.GetAgentSnapshot` reports **failed** and the Flow reason even if unstored. On failed sessions, `SendUserMessage` returns `AgentSessionFailedError` with the reason, wrapping `*dex.FlowNotActiveOrNotFoundError`. [Ref](../docs/reference/agents/session-limits.mdx#at-the-limit)
- Go `TurnCompleted.TurnError` fails without retry, stores `NewMessages` and `Usage`, sets `AgentState.LastTurnError`, and emits **turn_failed**. Blank text fails with usage recorded, and the next completed turn clears `LastTurnError`. [Ref](../docs/reference/agents/changelog.mdx#v0-6-16)

### Added

- Each agents release tags every module under `agents/go/` on the `agents/v<version>` commit, such as `agents/go/core/v<version>`. Modules must require other agents Go modules at that version. The core now shares the agents `LICENSE`. [Ref](../docs/reference/agents/install.mdx#go)

### Changed

- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk** document **isError** and **ToolExecutionError** for definite failures (**failed**). Other thrown write errors and, since 0.6.15, definite write failures on later attempts record **outcome_unknown**.
- **RunModel** and **CompactContext** back off transient model errors with jitter and **Retry-After**, and fail fast on other 4xx. **RunModel** defaults to 10 attempts (was 3), and 1 disables retries. **ModelRetryPolicy** tunes backoff. [Ref](../docs/reference/agents/workers.mdx#model-api-errors)
- **@superdurable/dex-agents-pi**: a failed pi-ai model call whose error
  text pi-ai reports as retryable, such as a 429 or 503, retries with the same
  backoff.

### Fixed

- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk** show the first deferred call's result in the reply's tool results when a second such call is refused. The refusal asks to call that tool alone if needed and no longer implies the first succeeded. (#2111)
- Go records `*ToolExecutionError` as **outcome_unknown** on a later attempt of a non-**read** call that no tool source built. With that error, first attempts, reads, and tool-source calls still record **failed**. [Ref](../docs/reference/agents/failures.mdx#retry-refusal)

## 0.6.15 (2026-11-18)

### Changed

- Python and TypeScript use only the Connector branch. **uncertain** and marked **unknown** give **outcome_unknown**, success gives **succeeded**, and **defect**, marked **rejected**, and other branches give **failed**. [Ref](../docs/reference/agents/connector-tools.mdx#outcomes)
- A connector Retry always goes to the tool Step's retry policy. A
  single-attempt write that asks to retry now records **outcome_unknown**,
  also when the fence recorded that nothing was sent, instead of **failed**
  with the not-sent text.
- Arguments a Worker refuses before the operation runs record **failed** on
  every attempt, also after an earlier attempt's claim.
- **classify_connector_branch** ignores **may_have_dispatched** and
  **classifyConnectorBranch** ignores **mayHaveDispatched**; both are
  deprecated. **CONNECTOR_NOT_SENT_RETRY_LATER** and
  **connectorNotSentRetryLater** are deprecated and no call records them.
- Python packages require **dex-python-sdk** 1.45.8 and TypeScript packages require and peer-depend on **@superdurable/dex** 1.45.8, including the CLIs. Together they fix **Value has no concrete kind** for TypeScript calls to Python RPCs declared without input. (#2034)

### Fixed

- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk** restore deferred calls to renamed or removed tools. In-flight single attempts stay **outcome_unknown** without rerunning, and a removed **idempotent_write** retry gives **outcome_unknown** instead of approval.
- **dex-agents-google-adk** now resumes **RunModel** with recorded results for paused calls to renamed or removed tools. In-flight single-attempt calls keep **outcome_unknown** without rerunning. The next model call uses the new tool list.
- Python and TypeScript record **outcome_unknown** on a later `ToolExecutionError` for a non-read call. First attempts, reads, and calls built by tool sources still record **failed**. [Ref](../docs/reference/agents/failures.mdx#retry-refusal)
- **dex-agents-openai-agents** and **@superdurable/dex-agents-openai-agents** rebuild a turn's history from recorded results when a renamed or removed tool prevents resuming **RunState**. In-flight single-attempt calls keep **outcome_unknown** without rerunning.

## 0.6.14 (2026-11-16)

### Added

- **StopFlow** with **GracefulComplete** ends sessions as **COMPLETED** after running and queued turns finish. **AwaitUserMessage** waits for it on server-zd 0.1.34+. Upgrade every Worker of the Flow type to 0.6.14+. [Ref](../docs/reference/agents/session-limits.mdx#end-a-session)

### Changed

- The Python agents packages and CLI commands depend on **dex-python-sdk**
  1.45.4, the TypeScript ones depend on, and peer-depend on,
  **@superdurable/dex** 1.45.6, and the Go core requires
  **github.com/superdurable/dex/sdk-go** 1.45.6. These releases add the
  graceful completion wait condition.
- The CLI commands download **dexcli** 1.47.9 when no **dexcli** of at least
  that version is found. Its **dexcli dev** runs server-zd 0.1.34, the first
  server with the graceful completion wait condition.

## 0.6.13 (2026-11-15)

### Added

- **fork_agent_session**, **forkAgentSession**, and **AgentSessionClient.fork_session** fork at idle or between turns via **after_message** (**afterMessage**). **AgentSessionForkingFlow** and **createAgentSessionForkingFlow** need no built agent. [API](python/core/README.md#forking-sessions)
- **fork SESSION_ID [--at N]** and **/fork [N]** fork a session and offer to open it. Sessions from before 0.6.8 keep their task queue. Forks need server-zd clone fork support, available in **dexcli** 1.47.8. [Reference](../docs/reference/agents/cli.mdx#fork)

### Changed

- The Python agents packages and CLI commands depend on **dex-python-sdk**
  1.45.3, and the TypeScript ones depend on, and peer-depend on,
  **@superdurable/dex** 1.45.5. Both releases add clone fork, which session
  forks use.
- The CLI commands download **dexcli** 1.47.8 when no **dexcli** of at least
  that version is found. Its **dexcli dev** runs server-zd 0.1.33, the first
  server with clone fork.

## 0.6.12 (2026-11-15)

### Added

- `dex-agents-session-graph` adds --trigger-binding and `--trigger-bindings <file.json>`. Code can use `resolveSessionTriggerBinding` / `resolve_session_trigger_binding` with `connectorTriggerBindings` / `connector_trigger_bindings`. [Ref](../docs/reference/agents/dex-web.mdx#trigger-bindings)
- Connector Mutations resend only unknown outcomes if manifest and operation agree on `agent.repeatOnUnknown: limited` and `agent.maxSends` (2–10). Unlimited attempts become three or the pinned cap, whichever is greater. [Ref](../docs/reference/agents/connector-tools.mdx#bounded-resends)
- `ToolCallStepOptions.bounded_resend_execute_retry`
  (`boundedResendExecuteRetry`) gives a `non_idempotent_write` call whose tool
  enforces its own send cap more than one attempt in `CallSingleAttemptTool`;
  when the attempts run out, the call records `outcome_unknown`.
- `ToolPolicy.max_sends` (`maxSends`) sets a bounded tool's cap to 1–10. `1` allows one attempt. Passing the cap requires `@superdurable/connectors-sdk` 0.11.0+ or `dex-connectors-sdk` 0.10.0+. [Ref](../docs/reference/agents/connector-tools.mdx#max-sends)
- Run only Workers at 0.6.12 or later for the Flow type before enabling `limited` resends and until bounded calls finish. Earlier Workers can exceed the pinned TypeScript send cap. [Ref](../docs/reference/agents/connector-tools.mdx#bounded-write-upgrade)

### Changed

- Failed, steered, or stopped turn notes cap each result at 2,000 UTF-8 bytes, with an omission marker, and stored records keep the full result. The limit is `TOOL_CALL_NOTE_MAX_OUTPUT_BYTES`, `toolCallNoteMaxOutputBytes`, or `ToolCallNoteMaxOutputBytes`.

### Fixed

- TypeScript CLI commands show known image storage failures and unreadable-file causes, and format size limits with commas. The UI and Worker log use safe storage reasons or a generic message to avoid exposing bucket URLs or credentials.
- `@superdurable/dex-agents-claude-agent-sdk` stores partial transcripts during `RunModel` deferrals. `ClaudeAgentRunner.buildUnfinishedTurnMessages` records the final result of an abandoned call, and `RunModel` supplies unknown outcomes for deferrals ended by older Workers.
- `ConnectorToolSource` keeps a Connector branch `outcome_unknown` when a
  `sendCount` on an earlier attempt's fence marker counts a send that may have
  taken effect, in both cores. It also does so when an earlier attempt's
  claim stands but the operation returned before it read that record.
- `ConnectorToolSource` records `outcome_unknown` when a retry rejects arguments and an earlier attempt claimed or counted a send. First attempts and retries without that evidence record `failed`. [Reference](../docs/reference/agents/connector-tools.mdx#outcomes)
- `@superdurable/dex-agents` reads a capped fence's `sendCount` as a possible earlier send. With `@superdurable/connectors-sdk` 0.11 outside the peer range, 0.6.11 could misclassify an optional branch as `failed`. [Ref](../docs/reference/agents/connector-tools.mdx#outcomes)
- `dex-agents-google-adk` uses `AdkAgentRunner.build_unfinished_turn_messages` to store events and answer paused calls in each `ParallelAgent` branch after steering, cancellation, or failure. Successful calls show `UNFINISHED_TURN_SUCCEEDED_TOOL_RESULT` without raw output.
- `dex-agents-langgraph` uses `LangGraphAgentRunner.build_unfinished_turn_messages` to restore checkpoint messages and add a missing `ToolMessage` for each call of the final `AIMessage` after steering, cancellation, or failure. This includes Deep Agents.
- `@superdurable/dex-agents-mastra` uses `MastraAgentRunner.buildUnfinishedTurnMessages` to preserve messages after steering, cancellation, or failure. Each pending tool invocation receives its recorded `result`.
- `dex-agents-openai-agents` uses `OpenAIAgentsRunner.build_unfinished_turn_messages` to preserve `RunState` items after steering, cancellation, or failure. Any unanswered call gets a `function_call_output` from its final record.
- `@superdurable/dex-agents-openai-agents` uses `OpenAIAgentsRunner.buildUnfinishedTurnMessages` to preserve `RunState` items after steering, cancellation, or failure. Any unanswered call gets a `function_call_result` from its final record.
- `dex-agents-claude-agent-sdk` saves deferred `RunModel` transcripts and records abandoned results with `ClaudeAgentRunner.build_unfinished_turn_messages`. If a Worker lacks that method, the next run records unknown results keyed by assistant entry and tool use ID, matching TypeScript.
- `@superdurable/dex-agents-claude-agent-sdk` keys `buildUnfinishedTurnMessages` and `RunModel` results by assistant entry and tool use ID, so later turns can reuse IDs without losing or duplicating results. Claude Code CLI 2.1.316 still shows a reused ID as "[Tool use interrupted]".
- `dex-agents-pydantic-ai` saves partial `RunModel` messages and uses `PydanticAIRunner.build_unfinished_turn_messages` to fill missing `ToolReturnPart` results. Workers at 0.6.11 or earlier cannot read the new state. Drain or upgrade them together for each Flow type.
- Agent package licenses link to the repository’s current legacy notices location.
- `@superdurable/dex-agents-vercel-ai` saves partial `RunModel` messages. `VercelAiAgentRunner.buildUnfinishedTurnMessages` uses `toolCallOutputOmittedResult` for unread successful `toModelOutput` results. Upgrade all Workers of a Flow type together. 0.6.11 rejects state version 2.

## 0.6.11 (2026-11-15)

### Fixed

- Pin npm sibling Dex Agents peers to the exact release version so pinned commands keep installing after newer releases change the Dex SDK pin.

## 0.6.10 (2026-11-14)

### Changed

- The TypeScript agents packages and CLI commands depend on, and peer-depend
  on, `@superdurable/dex` 1.45.4, so they install alongside
  `@superdurable/connectors-sdk` 0.10.1, which requires that release.

## 0.6.9 (2026-11-14)

### Changed

- `connectors.mcp: auto` starts `dexcli connectors mcp --exit-on-stdin-close`. Only `dex.server: auto` falls back to Dex Web MCP. `DexServerConnection.connectorsOffReason` / `DexServerEndpoint.connectors_off_reason` explains why tools are off. `dexcli` pin is 1.47.7.
- Terminal leases default to 600 seconds, up from 15. Set `sessions.lease_ttl_seconds` to 10–3600 or override it with `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS` (1–3600). Lease reads time out after 5 seconds. [Reference](../docs/reference/agents/cli.mdx#one-terminal-per-session)

### Fixed

- `init` updates only prompted settings in a valid file, and `--yes` uses the existing values. It backs up invalid files as `NAME.yaml.bak`, writes atomically through symlinks, and prompts for shell commands that skip approval in both languages. [Ref](../docs/reference/agents/cli.mdx#first-run)

## 0.6.8 (2026-11-14)

### Added

- New sessions use `AgentHarness.flow_type` (`flowType`) and a per-session queue from `session_task_queue` (`sessionTaskQueue`). The CLI starts pulling that queue when the lease is free and still serves older sessions on the default queue. [Reference](../docs/reference/agents/cli.mdx#resume)
- `acquire_session_terminal_lease` stores the optional `environment` in `SessionLastTerminalEnvironment` on a new acquisition. Renewals, refusals, and releases leave it unchanged. Older Workers ignore the field.

### Changed

- The TypeScript agents packages and CLI commands depend on, and peer-depend
  on, `@superdurable/dex` 1.45.3, so an application can use
  `PullWorkerOptions.taskQueue`, `StartFlowOptions.taskQueue`, and
  `FlowState.taskQueue` from that release.
- The CLI commands download dexcli 1.47.5 when **DEXCLI** names none.
- Terminal UI session switches now send the new `flow_type` and takeover notices without `previous_session_id`. The CLI falls back to plain chat unless `dexcli agents-tui --describe` reports `launcher_session_switch`.
- The Python agents packages and CLI commands depend on `dex-python-sdk`
  1.45.2, so an application can use `PullWorkerOptions.task_queue`,
  `StartFlowOptions.task_queue`, and `FlowState.task_queue` from that release.

### Fixed

- `dex-agents-cli-core`: a chat message sent before the new session's first
  Step ran is queued and answered, as in `@superdurable/dex-agents-cli-core`.
  The chat no longer ends with `[session failed] it has not started`.

## 0.6.7 (2026-11-13)

### Fixed

- `@superdurable/dex-agents` accepts optional peer `@superdurable/connectors-sdk` versions `^0.9.0 || ^0.10.0`, up from exactly 0.9.0. Apps and adapters can install compatible Connector SDK patches or 0.10.x without npm overrides.

## 0.6.6 (2026-11-13)

### Added

- Python `ClaudeAgentRunner` polls `ToolTaskStarted` through `poll_tool_task` and cancels it through `cancel_tool_task` on `cancel_turn`, recording `outcome_unknown`. In-flight `McpToolSource` tasks stay in their original tool Step and resend with the same idempotency key if lost.
- `@superdurable/dex-agents-claude-agent-sdk` adds `pollToolTask`, `cancelToolTask`, and `ToolInputRequested` support. Cancellation records `outcome_unknown`. `dex-agents-claude-sdk` gains this too. Step-polled tasks stay there and resend with the same idempotency key if lost.

### Changed

- `McpToolSource` returns `ToolTaskStarted` on retry and sets `will_resume_tool_task_without_sending` only with `mcp_task_polled_by_session`. Other tasks keep polling in the tool Step and resend with the same idempotency key if the server loses the task.

### Fixed

- `ClaudeAgentRunner` makes `executeDeferredToolCall` return `Promise<DeferredToolOutcome>`. Update overrides and check for no `kind` before `.output`. Infer the Flow or use `AgentSessionFlow<ClaudeAgentRunner>`. For any adapter, use `AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>`.

## 0.6.5 (2026-11-13)

### Fixed

- `dex-agents`: `ConnectorStepContext` now exposes `flow_started_at` and
  `first_attempt_at` from the tool Step's context. A connector operation that
  reads either one, such as a Gmail send, no longer fails inside an agent tool
  Step.
- `@superdurable/dex-agents`: `ConnectorStepContext` now exposes
  `flowStartedAt` and `firstAttemptAt` from the tool Step's context. A
  connector operation that reads either one, such as a Gmail send, no longer
  fails inside an agent tool Step.

## 0.6.4 (2026-11-13)

### Added

- `mcp_servers` blocks expansion of `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE` and redacts header secrets of 8+ characters in logs, errors, and output. Workers enforce approval and one attempt for `trust_tool_annotations: false`. [Ref](../docs/reference/agents/cli.mdx#mcp-servers)
- `@superdurable/dex-agents-pi-cli` and `dex-agents-deepagents-cli`: writes of
  `mcp_servers` tools wait for approval, as connector writes do.

## 0.6.3 (2026-11-13)

### Added

- `SessionTerminalLease` and `acquire_session_terminal_lease` / `release_session_terminal_lease` let clients take a free or expired lease, renew their own, or release it. First upgrade all Workers of the Flow type to 0.6.3. [Lease RPCs](typescript/core/README.md#terminal-lease)
- Each CLI holds a 15-second terminal lease by default, renews it every 5 seconds, and releases it on exit. A second CLI waits to take over. Override with `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS`. [Reference](../docs/reference/agents/cli.mdx#one-terminal-per-session)
- Terminal UI contract 1: the launch document's optional
  `capabilities.launcher_session_switch` and the terminal UI's
  `switch_session` message hand `/sessions` and `/new` back to the command,
  which waits for the session's lease and launches the terminal UI again.

### Changed

- The TypeScript agents packages and CLI commands depend on, and peer-depend
  on, `@superdurable/dex` 1.45.1, so an application can use
  `Client.getFlowState` from that release.
- The agents CLI commands download dexcli 1.47.2, whose terminal UI hands
  **/sessions** and **/new** back to the command, so a switch also waits until
  no other terminal has the chosen session open.

## 0.6.2 (2026-11-12)

### Added

- Only `dex.auth` enables remote authentication with `DEX_API_KEY`, `DEX_CREDENTIAL`, or a token file. Use `dex.tls` to set `ca_file` and `server_name` for TLS. [Ref](../docs/reference/agents/cli.mdx#remote-server)
- `EffectiveConfig.dex` adds `auth` and `tls`, both optional in TypeScript. `resolve_dex_connection_settings` / `resolveDexConnectionSettings` omit secrets. New errors are `insecure_token_url`, `auth_needs_server_url`, and `tls_needs_https_server`.
- Terminal UI contract 1: the launch document's `dex.auth` and `dex.tls`
  carry the command's settings instead of `none` and the defaults. The
  terminal UI gets the one credential variable that `dex.auth` names.
- Shell commands, browsers, downloads, and `dexcli dev` lose `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE` even without `dex.auth`. Only the Worker host and terminal UI get the needed variable. `removeDexSecretVariables` / `remove_dex_secret_variables` filter them.
- `AgentUserMessage`, `SteerUserMessageRequest`, and `AgentMessage` add `sender_name` (`senderName`, `SenderName`). Invalid names or names with `message_id` are rejected. `AgentMessage.text` stays as sent. [Ref](../docs/reference/agents/dex-web.mdx#sender-names)
- The session graph declares the optional `sender_name` input of the
  `steer_user_message` Action.
- `describe_stored_message` (`describeStoredMessage`), which builds the text
  of a context summary prompt, shows a named user message as
  `user: <name>: <text>`.
- `reasoning_effort` (`reasoningEffort`) applies from the next turn. Harnesses declare `supported_reasoning_efforts` (`supportedReasoningEfforts`) and may suggest models. An unsupported value fails with `unsupported_reasoning_effort`. [Ref](../docs/reference/agents/cli.mdx#model-and-effort)
- `resolve_turn_config` and `resolveTurn` accept supported reasoning values and return the chosen effort. `TurnConfigResolver` takes the values at construction. All additions are optional. The UI reserves `default`, and suggested model IDs exclude spaces and controls.
- Terminal UI contract 1: the launch document carries the optional
  `display.models` and `capabilities.reasoning_effort`, which dexcli 1.47.0
  reads for `/model` and `/effort`. Older dexcli releases ignore them.
- `dex-agents-claude-sdk` maps `reasoning_effort` to `effort` with `low`, `medium`, `high`, `xhigh`, and `max`. Built-in, OpenAI, Pydantic AI, Vercel AI, and Pi support the first three. All commands suggest models for `/model`. [Reference](../docs/reference/agents/cli.mdx#model-and-effort)

### Changed

- The agents CLI commands download dexcli 1.47.0, whose terminal UI lists the
  suggested models for `/model` and the reasoning effort values for `/effort`.
- The agents CLI commands download dexcli 1.47.1, whose terminal UI shows the
  declared name of a message sent from Dex Web as "Name: text".

## 0.6.1 (2026-11-12)

### Changed

- The agents CLI commands download dexcli 1.46.1. A plain drag now selects
  text in the terminal UI, Up/Down and the mouse wheel scroll the messages,
  and Dex Web addresses show as links.

### Fixed

- The Python terminal UI exits normally after a failed turn without printing `The chat closed: dexcli closed the channel.` The exit code remains 1. A malformed line or a closed channel with `dexcli` still running still shows the warning.
- The terminal UI of the Python Agents CLI commands now prints
  `The chat closed unexpectedly (dexcli exit 143).` when `dexcli` exits 143
  by itself, such as after a SIGTERM sent to `dexcli` alone, as the
  TypeScript commands do. The command still exits 1.

## 0.6.0 (2026-11-12)

### Breaking

- `dex-agents-deepagents` runs writes and shell commands without approval by default. Use `approval.always` for `write_file`, `edit_file`, `delete`, `execute`, or `task`. `local_tools.shell.allow` has no effect. Use absolute `file_path`. Replace `list_directory`, `search_files`, and `run_shell_command` with native tools. Existing recorded calls still resume. [Ref](../docs/reference/agents/cli.mdx#harness-tools)
- `dex_agents_deepagents_cli`: `build_deep_agent` takes the workspace as a
  second argument, and `write_todos_tool` is removed; `write_todos` comes from
  LangChain's `TodoListMiddleware`.
- `dex-agents-pi` (TypeScript): `write`, `edit`, and `bash` no longer wait for
  approval by default, as in Pi. Name a tool in `approval.always` to make it
  wait. `local_tools.shell.allow` has no effect on this command.
- `dex-agents-pi`: the file tools and `bash` no longer go through the CLI
  core's workspace file layer. They use Pi's own local operations and reach
  any path you can, including absolute paths, `~`, links, and hard links.
  `grep` and `find` run `rg` and `fd` and respect `.gitignore`; Pi downloads
  a missing one to `~/.pi/agent/bin` unless `PI_OFFLINE` is set.
- `dex-agents-pi`: a `timeout` from the model follows Pi's rules and is no
  longer capped at 600 seconds; `pi.bash_timeout_seconds` still applies,
  120 by default, when the model passes none. A non-zero exit or a timeout
  kill records `failed` with the output instead of a result. A cut output
  names Pi's temporary file, which the command no longer deletes.
- `dex-agents-pi`: without `workspace` in the file, the workspace is the
  directory the command starts in, and `init` writes no `workspace`. A file
  that an earlier `init` wrote keeps its `workspace: ~/dex-agent-workspace`.
  A session started without a file resumes only with that line in the file.
- `@superdurable/dex-agents-pi-cli`: `WorkspaceGrep`,
  `defaultGrepMatchTimeoutMs`, and `WorkspaceGrepArguments` are removed.
- TypeScript OpenAI Agents adapter and the TypeScript **dex-agents-openai-sdk**:
  a turn that waits on a call of a source tool whose name has `-`, such as one
  waiting for approval, while its Worker upgrades fails when it resumes. Its
  saved run state names the tool with `_`, the name the model saw before this
  release, and the SDK no longer finds that tool. New turns are not affected.

### Added

- The Dex Agents CLI commands (`dex-agents-cli-core` and
  `@superdurable/dex-agents-cli-core`) read `DEX_AGENTS_OPEN_BROWSER`. Set it
  to `0` or `false`, and a command at a terminal no longer opens the Dex Web
  Connectors page in a web browser. It still prints the page's URL.
- `ToolExecutionResult` adds `is_finished_by_resumed_turn` (`isFinishedByResumedTurn`) for approval-only Steps. Dex records `succeeded` and `tool_call_started`, and the resumed `RunModel` writes `tool_call_finished`.
- Go Clients use `remotesession`, `RemoteSessionFlow` and `RemoteSessionClient` with Python or TypeScript Workers for `AgentEvents`, `answer_question`, `cancel_turn`, `steer_user_message`, `delete_queued_message`, `execute_plan`, and `send_user_message` (`plan_mode`, `images`). Go Workers refuse it.
- `DeferredToolCall.approval_display` (`approvalDisplay`) supplies `AgentInterrupt.display` and `interrupt_raised` display data. Targets truncate at 1 KiB, summaries at 2 KiB, and previews over 16 KiB are dropped. Missing display shows arguments. Pre-0.6.0 Workers drop it on rewrite.
- `ConnectorToolSource` shows the operation, connection, and up to five short arguments for approval via `build_connector_tool_approval_display` (`buildConnectorToolApprovalDisplay`). Credential redaction is best-effort and display-only. Stored arguments retain their full values.
- `write_file` and `edit_file` show workspace-checked diffs, omitting credential files, redacted content, refused paths, files or content over 64 KiB, and diffs over 200 lines. `run_shell_command` shows the working directory and a redacted command. Stored arguments stay complete.
- `AgentHarness.defaultWorkspaceLocation` can use `launch_directory` when no workspace is set, so `init` omits `workspace`. `resolveConfig` takes an optional fourth argument. `ConnectorsMcpToolSource.deferredToolCall` and `executeDeferredToolCall` accept an alternate name for `approval.always`.
- `@superdurable/dex-agents-pi-cli`: `PrefixedConnectorToolSource` and
  `collidingConnectorToolPrefix`, which offer the connector tools beside Pi's
  tools.
- `AgentMessage.origin` distinguishes session-written `plan_execution`, `turn_failure_note`, `turn_steered_note`, and `turn_cancelled_note` from user input. Readers keep unknown values. Workers before 0.6.0 drop the field when archiving.
- `AgentSessionOptions.summary_attributes_indexed` (`summaryAttributesIndexed`) adds indexed `AgentSessionTitle`, `AgentSessionLastActivityAt`, and `AgentSessionTurnCount`. It defaults to off. [Reference](../docs/reference/agents/approvals.mdx#list-sessions)
- Python and TypeScript adapter factories now accept `summary_attributes_indexed` or `summaryAttributesIndexed` for indexed session summaries. [Reference](../docs/reference/agents/approvals.mdx#list-sessions)
- Anyone who can search the namespace can read indexed titles in the user's words. With indexing enabled, Worker startup fails with `FAILED_PRECONDITION` if PostgreSQL keys `text1`, `datetime1`, or `int1` have the wrong type or all three of a type are used by other names.
- `list_agent_sessions` / `listAgentSessions` read one page with optional title search in one `SearchFlows` call, with no Worker. Sort loaded sessions with `sort_agent_sessions_by_last_activity` / `sortAgentSessionsByLastActivity`. [Ref](../docs/reference/agents/approvals.mdx#list-sessions)
- Sessions started without indexing lack a searchable title. At their next indexed turn, older sessions gain activity and count. Only a first turn writes a title. Turning indexing off or using older or Go Workers keeps stored values. [Ref](../docs/reference/agents/approvals.mdx#list-sessions)
- `sessions.summary` defaults to `auto` and indexes only with `dex.server: auto`. `on` and `off` apply everywhere. Harnesses must honor `HarnessContext.is_session_summary_indexed` / `HarnessContext.isSessionSummaryIndexed`. `sessions` adds latest turn time, count, and title.
- The Python CLI commands (`dex-agents-cli-core`) accept `--plain`, which
  keeps the chat in plain text lines instead of the `dexcli` terminal UI.
- The TypeScript CLI commands (`@superdurable/dex-agents-cli-core`) take
  `--plain`, which keeps the chat in plain text instead of the `dexcli`
  terminal UI. Pi always keeps the plain text chat, through the new
  `AgentHarness.terminalInterface` declaration (`"plain"`).

### Changed

- `dex-agents-claude-sdk` uses `strictMcpConfig` (`strict_mcp_config`) and `disableClaudeAiConnectors` to block MCP servers and connectors from Claude Code configuration or login. The CLI keeps built-in, browser, and Dex Connector tools, and application adapters keep their defaults.
- **dex-agents-builtin** enables plan mode with `write_todos` as its only planning tool. In the terminal UI, `/plan` drafts and `/run` approves and runs with all tools. Plain chat has no plan commands. [Ref](../docs/reference/agents/cli.mdx#terminal-ui)
- **dex-agents-builtin**: `write_todos` is now a reserved tool name, so a
  connector or local tool with that name fails the turn. Rename the
  conflicting tool, or use another harness.
- The Python terminal UI now shows Worker host startup notices and `dexcli dev` messages as launch notices. These include unavailable Connector tools and the local server log path.
- `dexcli dev` started for `dex.server: auto` now stops with the CLI on exit, failure, SIGTERM, or SIGHUP. The CLI removes `~/.dex/agents/dexcli-dev.json` and signals the running server's process group, escalating after 20 seconds or another signal. [Ref](../docs/reference/agents/cli.mdx#dex-server)
- CLI exit leaves reused `dexcli dev` running. Start your own or set `dex.server` to share it. If the reused server stops, the CLI exits 1. A crash or SIGKILL can leave a started server running for later reuse. [Reference](../docs/reference/agents/cli.mdx#dex-server)
- On Windows, TypeScript stops its `dexcli dev` tree with `taskkill /T` and then `taskkill /T /F`, while Python sends CTRL_BREAK_EVENT first. Python removes its `os.kill(pid, 0)` check because it could stop the server. [Reference](../docs/reference/agents/cli.mdx#dex-server)
- TypeScript CLI core: during the chat, the Dex SDK Worker's task stream
  reconnect messages go only to `~/.dex/agents/<harness>.log`, so a lost Dex
  server no longer prints stack traces into the chat. Other library warnings
  show their first line on the terminal and their full text in the log file.
- TypeScript CLI core: closing the terminal no longer ends a command on Linux
  with an uncaught `setRawMode EIO` error before it handles SIGHUP. The
  command now stops the `dexcli dev` it started and exits with 129.
- `dex-agents-deepagents` uses native tools. Connector writes need approval, file tools refuse paths outside the workspace, and `execute` runs with your permissions. `task` needs approval when `approval.always` names any native tool. Large results and messages stay in a private 0700 directory.
- LangGraph's `should_defer_tool_calls_before_middleware` defaults to `True`. Root tools with async wrappers run middleware per Step attempt. Set `should_defer_tool_calls_before_middleware=False` for prior ordering. In-flight 0.5.x turns that opted in may run one wrapper in `RunModel`.
- Python LangGraph adapter: a tool Step that runs a tool node's wrappers no
  longer applies the node's `handle_tool_errors`, so an exception in the tool
  body, such as a `ValidationError`, fails the attempt and a write records
  `outcome_unknown`, as it does without wrappers.
- Python LangGraph adapter: an `InjectedStore` parameter of a tool receives
  the compiled graph's store in its tool Step.
- TypeScript agents packages now include Node 26 (`^26.0.0`) in **engines.node**, removing `EBADENGINE` warnings. The Pi packages still require `^22.19.0` as their lower bound.
- `dex-agents-pi` runs native tools in separate Steps. Reads can retry. `write`, `edit`, and `bash` never run twice, and `pi.tool_progress` streams shell output. Even idempotent Connector writes need approval. [Ref](../docs/reference/agents/cli.mdx#harness-tools)
- Python and TypeScript CLI cores: the commands download `dexcli` 1.46.0,
  which has the terminal UI, instead of 1.45.0 when they find no `dexcli` of
  at least 1.46.0. A `dexcli` on `PATH` older than 1.46.0 is no longer used.
- `run` defaults to `dexcli agents-tui` at a supported terminal. Use `--plain` for plain chat. It is automatic on Windows, in `dex-agents-pi`, or without a usable terminal. A missing or failed UI prints `Plain chat:`. [Ref](../docs/reference/agents/cli.mdx#terminal-ui)

### Fixed

- Python and TypeScript Claude Agent SDK adapters show the user's rejection reason for a Claude Code tool such as `Write` or `Bash` without a hook-error prefix. Other rejection text stays as recorded, and the call remains `rejected`.
- Approved Claude Code calls emit `tool_call_started` in the Step. With `is_finished_by_resumed_turn` (`isFinishedByResumedTurn`), `tool_call_finished` records `failed`, `succeeded`, or `outcome_unknown` if no result arrived. A retried `RunModel` runs the tool again.
- `dex-agents-claude-sdk` sends Claude Code stderr to the log with terminal controls such as `ESC[?25h` removed. Failed log writes drop the line so it cannot affect the terminal.
- Claude Code logins now work after the first turn. Resumed segments set `CLAUDE_SECURESTORAGE_CONFIG_DIR` to the effective `CLAUDE_CONFIG_DIR`, or `""` if unset, while honoring a value already set by the application or Worker.
- Ctrl-C during `init` or first-run setup now exits both CLI cores with code 130 and prints `Cancelled; no configuration written.` No configuration file is written. [Reference](../docs/reference/agents/cli.mdx#first-run)
- Ctrl-D at a configuration question now cancels the same way and exits with
  code 1. Before, the Python commands took the default answer and went on.
- An unreachable Dex server now stops the CLI with its address and a fix in one line. Reachable servers returning `UNAVAILABLE` show their details and a retry hint. [Reference](../docs/reference/agents/cli.mdx#configuration)
- Python and TypeScript CLI cores: a configuration error on a field that takes
  one of several forms, such as `dex.server`, names the forms it takes.
- Python CLI core: when `dexcli` has no terminal UI, the `Plain chat:` line
  names the `dexcli` version the terminal UI needs, 1.46.0 or later, and says
  to point `DEXCLI` at one. A `dexcli` that speaks only newer contracts says
  to upgrade the command.
- Python and TypeScript CLI cores: a `DEXCLI` that names no executable file
  says to unset `DEXCLI` or point it at `dexcli`. The Python commands now check
  it too, in plain and terminal UI mode alike, instead of failing later or
  falling back to the plain chat.
- The Python shell tool stops processes left in a command's group when it exits, sending SIGTERM and then SIGKILL 0.5 seconds later. Processes still holding output no longer delay the call. [Reference](../docs/reference/agents/cli.mdx#local-tools)
- A process that left the command's group, for example with `setsid`, and
  still holds its output no longer delays the result by more than 1 s after
  the group is cleaned. It is not stopped.
- The Python shell tool reports `killed: timed out after N seconds` as a result and `exit code 128+N` for signals. It always includes `--- stdout ---` and `--- stderr ---`, cutting each stream at 64 KiB while reading. [Reference](../docs/reference/agents/cli.mdx#local-tools)
- A cancelled shell tool call now finishes stopping the command's group,
  reaping the shell, and closing its pipes before the cancellation goes on.
  One thread per call owns the shell, so this cleanup also finishes when the
  event loop closes or the interpreter exits.
- The shell tool now gives what a command leaves in its group up to 0.5 s
  after SIGTERM, even when the command's output already ended, and stops the
  group even when its output cannot be read. A command that leaves nothing
  behind gets no signal and no wait.
- The CLI prints one short notice when Connector tools become unavailable or recover. Fixed-list harnesses show a restart hint. Full listing errors go only to `~/.dex/agents/<harness>.log`. [Connectors](cli/typescript/core/README.md#connectors)
- Python and TypeScript CLI cores: a connectors MCP server that answers HTTP
  401 or 403, also only to `tools/list`, gets a line saying it denied access
  and to check its access settings and `connectors.mcp`, instead of a line
  saying each turn tries again.
- Python CLI core: a command whose `~/.dex/agents` directory or
  `<harness>.log` file cannot be written prints `Logging is off` with the
  reason and starts without a log file, instead of exiting with an error.
- **ConnectorsMcpToolSource** adds `startupAvailability`, `writeLogLine`, and `describeUnavailable` in TypeScript, and `describe_unavailable` and `startup_error` in Python. Python raises `ConnectorsMcpAccessDeniedError` on 401/403. TypeScript listings accept `AbortSignal` and time out at 15s.
- Python and TypeScript cores: **McpToolListingError** carries the HTTP
  status a Streamable HTTP server answered to the failed listing, as
  `httpStatus` in TypeScript and `http_status` in Python.
- TypeScript core: **McpToolSource** takes a `logWarning` option that receives
  its warnings, such as a server left out of the tool listing, instead of
  `console.warn`. A `logWarning` that throws falls back to `console.warn`, so
  it never fails `listTools()` or a tool call.
- `sessions` keeps paging until it finds 250 sessions, reaches the end, or makes 20 requests. Short pages no longer end the list early, and it reports when more sessions remain. [Reference](../docs/reference/agents/cli.mdx#resume)
- `runAgentCli` waits up to 2 seconds for stdout and stderr to drain before returning, throwing, or exiting on SIGTERM, SIGHUP, or SIGINT. Piped `sessions` lists keep their closing `Resume one with` line.
- If MCP `initialize` succeeds but `tools/list` fails, the CLI prints one notice, logs the error, starts, and retries listings each turn. An explicit `connectors.mcp` URL that fails initialization stops startup. [Connectors](cli/typescript/core/README.md#connectors)
- **PrivateDexEngine** no longer downloads or runs Connectors MCP. It sets `DEX_CONNECTORS_MCP_BINARY` to a missing file and isolates `HOME` under the test state directory so the user's `~/.dex` stays untouched.
- With `should_defer_tool_calls_before_middleware`, LangGraph runs root tool node wrappers on each Step attempt and records non-message state. `dex-agents-langgraph` requires `langgraph` 1.2, `langgraph-prebuilt` 1.1, and `langchain-core` 1.7.
- Python LangGraph adapter: a tool's `ToolRuntime` in its tool Step has the
  compiled graph's `store` instead of `None`. It still has no `context`.
- The TypeScript OpenAI Agents adapter and **dex-agents-openai-sdk** keep hyphens in tool source names, so names that differ only by `-` and `_` can coexist. Characters the OpenAI API rejects still map to `_`.
- The shell tool of the TypeScript CLI commands stops what a command left in
  its process group when the command ends, such as a process started with
  `&`, and answers at once. Before, such a call waited for its timeout and
  then killed the group.
- The Python CLI core (`dex-agents-cli-core`) builds a new session ID from the
  UTC start time, as the TypeScript CLI core does, instead of the local time.
  Both now give the same shape, such as `builtin-20261111-203123-3efeb4`.

## 0.5.0 (2026-11-11)

### Dex SDK 1.45.0

- All agents packages pin Dex SDK 1.45.0 (`dex-python-sdk==1.45.0`, `@superdurable/dex` `1.45.0`, Go `sdk-go` `v1.45.0`), up from 1.38.0. The release supports **Join** and Worker-side Flow definition uploads, and npm adapters pin the same SDK peer.
- Agents packages pin the SDK exactly. Since 1.45, Step types with "-" cannot start SubFlows. Review 1.43 and 1.45 breaking changes before upgrading. [Python](../docs/reference/python/changelog.mdx), [TypeScript](../docs/reference/typescript/changelog.mdx), [Go](../docs/reference/go/changelog.mdx)
- Dex SDK 1.45.0 still needs a Dex Server with protocol 4: server-zd v0.1.17
  or later.
- Agents CI, the nightly long run, and the publish workflows test against
  `dexcli` 1.45.0. The Python and TypeScript CLI cores download `dexcli`
  1.45.0 when none is installed.

### Dex MCP conventions

- **McpToolSource** uses `io.superdurable.dex/effect` over annotations, and `io.superdurable.dex/approval: recommended` requests approval. A policy cannot lower a declared effect. Writes run in model order. [Ref](../docs/reference/agents/changelog.mdx#v0-5-0)
- New `McpToolSource.merge_tool_policies` (`mergeToolPolicies`) gives a
  framework adapter the application's tool policies merged by the same rule.
  Use it instead of spreading your policies after `tool_policies()`
  (`toolPolicies()`), which would let them lower a declared effect.
- A tool result's `_meta` key `io.superdurable.dex/outcome` (or a JSON-RPC
  error's `data._meta`) set to `unknown` records `outcome_unknown` and never
  sends the call again; `rejected` records `failed`. Either wins over `isError`.
- MCP exports `TOOL_EFFECT_META_KEY`, `TOOL_APPROVAL_META_KEY`, and `TOOL_OUTCOME_META_KEY` (TypeScript: `mcpToolEffectMetaKey`, `mcpToolApprovalMetaKey`, `mcpToolOutcomeMetaKey`). `McpTool` adds `dex_effect` / `dexEffect` and `is_approval_recommended` / `isApprovalRecommended`.
- Both cores: `poll_tool_task` (`pollToolTask`) may raise
  `ToolOutcomeUnknownError`; the session records `outcome_unknown` and stops
  polling. It used to fail the poll attempt.
- `agents/conformance/mcp_dex_conventions.json` fixes the conventions for every
  core.
- `McpToolSource.execute_deferred_tool_call` accepts `request_meta` for `tools/call`, with the idempotency key winning on a clash. `McpToolSource.will_resume_tool_task_without_sending` reports whether this call can only resume an already-started task.

- **McpToolSource.executeDeferredToolCall** accepts `executeDeferredToolCall(call, { requestMeta })` through **McpToolCallOptions**. The added `_meta` entries go to `tools/call`, and the idempotency key wins on a clash.
- **McpToolSource.willResumeToolTaskWithoutSending(call)** is `true` only when `executeDeferredToolCall` returns an already-started, session-polled task. A task polled inside the tool Step can send `tools/call` again if the server lost it.

### Showing sessions in Dex Web

- Pass the result of **session_flow_definition_document** (`sessionFlowDefinitionDocument`) in `WorkerOptions(flow_definitions=...)` (`flowDefinitions`) for uploads on server-zd 0.1.26+. Older servers use **render_session_flow_graph**. [Ref](../docs/reference/agents/dex-web.mdx#upload-the-graph)
- Session graph documents use a fixed `generated_at` from `SESSION_FLOW_GRAPH_GENERATED_AT`. Workers replace the stored graph when the template advances. For Action permission-only changes, upload once with `force`. [Reference](../docs/reference/agents/dex-web.mdx#definition-authority)
- The CLI uploads its suffixed session graph at startup, so sessions appear in Dex Web with `dexcli dev` 1.45.0. Session Actions require `dex-agents-cli.operate`. Upload results go to `~/.dex/agents/<harness>.log`. [Reference](../docs/reference/agents/cli.mdx#dex-web)
- Go core: no session graph yet.

### Ending or continuing a built-in loop turn

- The built-in loop adds **AgentDefinition.decide_turn_continuation** (`decideTurnContinuation`). After tools, "end" skips the next model call. **ContinueTurn(message)** or `{ action: "continue", message }` adds user text and continues. "default" keeps prior behavior.
- The ninth consecutive continuation attempt ends the turn and sets `last_turn_error` to **TURN_CONTINUATION_LIMIT_ERROR**. Tool calls reset the count. A thrown or invalid hook result fails `RunModel` and follows its retry policy. [Ref](../docs/reference/agents/changelog.mdx#v0-5-0)
- Runner contract additions: the **TurnContinued** outcome
  (`kind: "turn_continued"`) makes the session store its messages and run
  `RunModel` again, and **TurnCompleted.turn_error** (`turnError`) ends a turn
  as failed without a retry. Existing runners need no change.
- Built-in loop run state gains the key `consecutive_continuations`, written
  only after a continued answer. Earlier Workers ignore it, and sessions
  stored before this release continue unchanged.
- TypeScript core: **convertAgentSnapshotToUIMessages** shows a user message
  between two model calls of one turn, such as a continuation message, as the
  start of the next model call's step instead of a user message, as the UI
  stream does.
- Go core: not supported yet.

### Added

- `dex-agents-cli-core` 0.5.0 adds a shared Python **AgentHarness** CLI with first-run YAML setup, checksum-verified `dexcli` downloads, per-turn tools, inline approvals, and Ctrl-C to cancel a turn. See [cli/python/core](cli/python/core/README.md).
- **dex-agents-claude-sdk** 0.5.0 uses Claude Code tools. Enabled groups vary per turn. Files stay in the workspace. Writes and shell require approval except allowlisted shell commands. The Step rechecks calls. `ANTHROPIC_API_KEY` passes through. [CLI](cli/python/claude-sdk/README.md)
- **dex-agents-pi** uses Pi tools and provider API key variables only. `pi.bash_timeout_seconds` defaults to 120s (max 600s). `pi.load_project_context` loads local `AGENTS.md` by default, trusting files and links above the workspace only if user- or root-owned. [Pi](cli/typescript/pi/README.md)
- **WorkspaceFiles** exposes **readFileWithVersion**, **replaceFileIfUnchanged**, **statExisting**, and **readDirectoryEntries** to TypeScript harnesses. A refusal throws **WorkspaceFileRefusedError** before modifying files. See [core](cli/typescript/core/README.md#the-harness-interface).
- TypeScript CLI core: `read_file`, `edit_file`, and `search_files` open
  files without blocking, so a FIFO in the workspace fails at once as not a
  regular file, or is skipped by a search, instead of hanging the call, as in
  the Python CLI core.
- Python CLI 0.5.0 adds **dex-agents-openai-sdk**, **dex-agents-pydantic-ai**, **dex-agents-deepagents**, **dex-agents-google-adk**, and **dex-agents-builtin**. Provider keys come only from standard env vars. Rename `langgraph` config to `deepagents`. See [cli/python](cli/python).
- TS adds **dex-agents-openai-sdk**, **dex-agents-vercel-ai**, **dex-agents-mastra**, **dex-agents-builtin**. Keys use env. [OpenAI](cli/typescript/openai-sdk/README.md) [Vercel](cli/typescript/vercel-ai/README.md) [Mastra](cli/typescript/mastra/README.md) [Loop](cli/typescript/builtin/README.md)
- Set **AgentHarness.isToolSetFixedAtBuild** to have the CLI list Connector tools before building the Flow. If MCP is unavailable at startup, the CLI prompts for a restart. See [core](cli/typescript/core/README.md#connectors).
- **claude_code_tool_permission** (`claudeCodeToolPermission`) returns **ToolPolicy** or **ClaudeCodeToolDenial** for each call, such as allowlisted `Bash`. No result keeps session policy. [Per-call decisions](python/claude-agent-sdk/README.md#per-call-decisions-for-claude-code-tools)
- `@superdurable/dex-agents-pi` 0.5.0 adds Pi sessions and peers on `@earendil-works/pi-ai@^1.5.1`. It exports createPiAiModelClient, piAgentFunctionTool, and **piDurableFunctionTool(tool, { effect })**. A Pi AgentTool needs an effect or declared retry safety. [Pi](typescript/pi/README.md)
- **AgentSessionStartInput** adds optional `start_environment` (`startEnvironment`, `StartEnvironment`) through `AgentSessionClient.start_session` and `startAgentSession`. CLIs store host, user ID and name, config identity, and workspace in Flow input. Workers before 0.5.0 ignore it.
- **ToolCallContext.effect** reports the effective tool effect after policies. Let `non_idempotent_write` and `unclassified` errors propagate to record `outcome_unknown`. Treat omitted TypeScript effects and Python's `UNCLASSIFIED` default as writes.
- **prepare_arguments** (`prepareArguments`) synchronously and deterministically converts input before validation. Conversion errors fail the call as invalid arguments, and stored calls and approvals keep the original input.
- **ToolExecutionResult** and **AgentToolCall** add `runner_tool_state` (`runnerToolState`, `RunnerToolState`) to keep a successful call's opaque framework state apart from its output. It stays out of model inputs, browser snapshots, and trace attributes.
- LangGraph runs **create_deep_agent** with **DeltaChannel**, **ToolRuntime**, **InjectedState**, and **Command** updates. New options are **excluded_tool_names**, **select_turn_chat_model**, and **should_defer_tool_calls_before_middleware**. [Guide](python/langgraph/README.md#a-deep-agents-agent)
- The **dex-agents-deepagents** command uses these options instead of its own
  state schema, subagent middleware replacement, and model middleware. Its
  behavior is unchanged.
- Pi `prepareArguments` converts inputs before validation. Errors follow **ToolCallContext.effect**: `read` and `idempotent_write` fail, and `non_idempotent_write`, `unclassified`, or unknown effects record `outcome_unknown`. See [typescript/pi](typescript/pi/README.md).
- **createPiAiModelClient** sends **ModelRequest.imageBytes** and fails if bytes are missing. **piAiEstimatedImageInputTokens** counts 1600 per image. **piAgentFunctionTool** and **piDurableFunctionTool** upload images with a store, using placeholders if omitted. [Pi](typescript/pi/README.md)

### Images

- Python and TypeScript user messages and tool results can carry images. The session stores an **AgentImageReference**, and **MediaStore** stores bytes by SHA-256 so repeat uploads reuse the object. [Reference](../docs/reference/agents/changelog.mdx#v0-5-0)
- **AgentUserMessage.images**, **AgentMessage.images**, **AgentToolCall.images**, **ToolExecutionResult.images**, and **FrameworkMessage.images** default to empty. Upgrade all Workers of the Flow type to 0.5.0 before sending images. [Reference](../docs/reference/agents/changelog.mdx#v0-5-0)
- **S3MediaStore** (`dex_agents.s3`, extra `dex-agents[s3]`;
  `@superdurable/dex-agents/s3`, optional peer `@aws-sdk/client-s3`) keeps
  images in AWS S3 or an S3-compatible bucket, with credentials from the
  standard AWS chain. **InMemoryMediaStore** is for tests.
- **LocalDirectoryMediaStore** stores images with modes `0700` and `0600` on one POSIX host, verifies reads, and refuses symlinks, special files, and extra hard links. A same-user process with write access to the store's root can race the TypeScript checks.
- PNG, JPEG, GIF, and WebP are checked. **MAX_IMAGE_BYTES** (`maxImageBytes`) is 3,750,000 raw or 5,000,000 base64. **MAX_IMAGES_PER_MESSAGE** (`maxImagesPerMessage`) is 20 per result or message. **MAX_IMAGE_URI_LENGTH** (`maxImageUriLength`) is 1024 UTF-8 bytes. **ImageValidationError** reports it.
- **verify_image_bytes** (`verifyImageBytes`) checks hash, size, and type. **S3MediaStore** uses conditional puts, rejects size mismatches, and reads at most one byte past `size_bytes`. It defaults to the AWS credential chain and accepts explicit credentials for local buckets.
- The session checks the images of every `FrameworkMessage` a runner returns
  before it stores any. Keep each object for as long as sessions can replay
  it; a missing object fails the turn with **MediaStoreError**.
- `send_user_message` returns `false` for invalid images, or for images when
  the runner does not implement **ImageUserMessageBuilder**
  (`buildUserMessageWithImages`). The built-in loop implements it when its
  session Flow has a media store; see below.
- Framework adapters accept user and tool images with optional **media_store** (`mediaStore`). They store references and fetch bytes immediately before model calls. Adapters without a store refuse images. [Reference](../docs/reference/agents/changelog.mdx#v0-5-0)
- **McpToolSource** uploads successful `image` parts via **media_store** (`mediaStore`) and returns `ToolExecutionResult.images` with text placeholders. It rejects more than 20 images, noncanonical base64, or unsupported image bytes. Without a store it keeps the JSON text.
- Failed MCP results (`isError`, `rejected`, or `unknown`) now show `[image <n> omitted]` for each image and exclude raw bytes from the recorded error, even without a media store.
- Runners read bytes with **load_images** (`loadImages`) and may choose them
  with **collect_turn_images** (`collectTurnImages`).
- Go core: no image input. Its values carry `images`, so a value written by
  another core round-trips, and `send_user_message` returns `false` for a
  message with images.
- Shared cases: `agents/conformance/images.json`.
- CLI **media** stores **/image <path>** attachments and Connector images. `media.store` defaults to `auto` at `~/.dex/agents/media` or takes `s3://<bucket>[/<prefix>]`. Credentials come only from the environment or AWS chain. Session settings cannot change the store. [Images](cli/README.md#images)
- **dex-agents-pi** sends **/image** attachments and up to 20 images per tool result to the model via the media store. Refused or excess images become `[image omitted: <mime type>]` without failing the call. [Images](cli/typescript/pi/README.md#images).
- Built-in loop function tools return **FunctionToolResult** or `functionToolResult({ output, images })`. Upload via **ToolCallContext.media_store** with `put_image` (`putImage`). Images without a store fail without retry. [Images](python/core/README.md#images-from-function-tools)
- **builtin_agent_session_flow** and `createBuiltinAgentSessionFlow` accept **media_store** (`mediaStore`). Before each model call, the runner loads bytes into **ModelRequest.image_bytes** (`imageBytes`). Without a store, user images are refused and tool images stay hidden from the model.
- **LiteLLMModelClient** sends images as base64 `data:` URLs and estimates **ESTIMATED_IMAGE_INPUT_TOKENS** per image. Gemini and Vertex AI accept user images but omit tool result images, which get **TOOL_RESULT_IMAGES_OMITTED_TEXT**.
- **createAiSdkModelClient** sends images as AI SDK `file` parts in user
  messages and in tool result `content` output. `estimateInputTokens` counts
  **estimatedImageInputTokens** per image.

### Changed

- The built-in loop moves to `dex_agents.builtin_loop`, `@superdurable/dex-agents/builtin-loop`, and Go `builtinloop`. Use `BuiltinLoopAgentRunner` and renamed factories. Deprecated names work for one release. Stored sessions resume. [Ref](../docs/reference/agents/changelog.mdx#v0-5-0)
- Built-in loop decoding errors now name the built-in loop. Python loggers move to `dex_agents.builtin_loop.*`, and Go nil model or definition panics start with `builtinloop:`.
- CLI Flow type suffixes change from 12 hex digits to six lowercase base36 characters. New CLIs cannot resume or list sessions under the old suffix. Python adds `namespace_session_flow_type`, `current_session_environment_identity`, and `SessionEnvironmentIdentity`, matching TypeScript naming.
- Connector default names keep acronyms together and split on ASCII capitals only. Old calls cannot find renamed tools. Set the old name explicitly in connector_tool or connectorTool to resume them. [Ref](../docs/reference/agents/connector-tools.mdx#connector-tools-offered)
- Mutation `defect` records `outcome_unknown` without retry regardless of `agent.branches`. Query `defect` still follows `agent.branches`. Required `defect` or `uncertain` no longer records `succeeded`. [Ref](../docs/reference/agents/changelog.mdx#v0-5-0)
- Fenced Mutations record `outcome_unknown` on non-success branches if a send may have occurred. Single-attempt Retry records `failed` only with `notSent` or `canResend` and no possible send. `classify_connector_branch` adds `may_have_dispatched`. [Ref](../docs/reference/agents/changelog.mdx#v0-5-0)

### Fixed

- Claude Agent SDK adapters discard Claude Code `last-prompt` entries, including those stored in 0.4.0, so a resumed segment keeps the prior response and resumes the correct deferred call.
- Python and TypeScript Claude Agent SDK adapters: when the Claude Code CLI
  returns an error result, the turn's error now reads "the Claude Code CLI
  returned an error result (subtype ...)". It used to read "reported success"
  for an error result whose subtype is `success`, such as a failed login.

## 0.4.0 (2026-11-10)

### Connector tools

- **ConnectorToolSource** exposes only operations explicitly selected with `connector_tool` or `connectorTool` and marked `agent.exposed: true`. Nothing is exposed by default. [Reference](../docs/reference/agents/connector-tools.mdx#connector-tools-offered)
- Queries are `READ`. Mutations with `agent.retry: safe` are `IDEMPOTENT_WRITE`, others `NON_IDEMPOTENT_WRITE`. Steps use operation timeouts and retry policy, up to three attempts. `tool_policies` changes approval only. [Ref](../docs/reference/agents/connector-tools.mdx#step-options)
- Approval: `agent.approval: recommended` asks by default; other Mutations run
  without approval. `requires_approval` (`requiresApproval`) overrides it with a
  bool or a per-call predicate over the arguments.
- Connector tools check `inputSchema` before decoding. With `agent.branches`, success is `succeeded`, and `rejected` and `defect` are `failed`. `uncertain`, `unknown`, and unlisted Mutation branches are `outcome_unknown`. Unlisted Query branches are `failed`. Canonical JSON defaults to a 64 KiB cap.
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

- Python and TypeScript adapters accept `tool_sources` (`toolSources`) and `tool_source_listing_timeout` (`toolSourceListingTimeoutMs`), default 10 seconds. Each `RunModel` lists tools and omits a source if listing fails or times out.
- Source tool Steps use `execute_deferred_tool_call` (`executeDeferredToolCall`) and `tool_call_step_options` (`toolCallStepOptions`). Sources set effect, approval, and write order. Connector calls keep their Call ID and fence, and `tool_policies` changes only approval.
- Duplicate `source_name` (`sourceName`) or tool names fail definition. Collisions discovered while listing fail the `RunModel` attempt, including for Mastra's request-time tools.
- Claude Agent SDK, TypeScript OpenAI Agents SDK, and Python Step-mode adapters resume recorded calls to now-missing source tools. Results are served once under the original name. New calls of that tool in the same turn are refused.
- The framework adapters poll no tool tasks: a source call that starts a task
  records `failed` for a `read` call and `outcome_unknown` otherwise. In the
  TypeScript adapters, a source call that asks a question records `failed`.
- `ClaudeAgentRunner` and `claude_agent_session_flow` (`createClaudeAgentSessionFlow`) accept tool sources. Calls defer in model order and later writes in a response are refused. Adding a source tool to `allowed_tools` (`allowedTools`) fails the attempt.
- `createVercelAiAgentSessionFlow`, `createOpenAIAgentsSessionFlow`, `createMastraAgentSessionFlow`, and `MastraAgentRunner` accept `toolSources`. OpenAI exposes non-strict tools to each agent and restores source names, while Mastra exposes client tools without `execute`.
- Python OpenAI, Pydantic AI, LangGraph, and ADK accept `tool_sources`. OpenAI uses `cached_tools()`, Pydantic AI uses `ExternalToolset`, custom LangGraph binds `get_current_tool_source_tools()`, and ADK adds `SourceToolset` to each `LlmAgent`.
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
- A Claude tool call resumes at most once per segment under its recorded name and kind while the transcript has no result for it. A mismatched tool or a repeat, including a call already resumed in an earlier segment, is refused without running.
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

- `render_session_flow_graph` (`renderSessionFlowGraph`) and `dex-agents-session-graph` render for `--flow-rendering-dir` with a permission for `resolve_interrupt`, `answer_question`, `steer_user_message`, and `cancel_turn`. [Ref](../docs/reference/agents/dex-web.mdx#what-dex-web-shows)
- The graph is derived from the Python core's registered Flow and source and
  ships in both packages. The conformance suite adds `session_graph.json`, and
  each core's tests fail when the graph and the session Flow disagree.
- Dex Web needs a release that accepts Actions without a condition and calls no
  RPC for a view without fields.
- Go core: no graph. It registers only part of the session Flow.
- See [Show sessions in Dex Web](../docs/reference/agents/dex-web.mdx).

### Compaction in framework adapters

- OpenAI Agents SDK, Pydantic AI, LangChain and LangGraph, Google ADK, Vercel AI SDK, and Mastra support `AgentSessionOptions.compaction`. Existing 0.2.0 sessions compact once Workers enable a policy. [Ref](../docs/reference/agents/session-limits.mdx#compaction)
- Adapters compact only between turns, before a turn's first model call. A model
  call that resumes a turn after its tool calls never compacts, because the
  OpenAI Agents SDK and LangGraph resume from a run state that holds the input.
- Adapter Flow factories accept `compaction`, `retained_message_limit`, and `summary_model` (`summaryModel`). LangGraph requires the summary model. TypeScript OpenAI Agents and Mastra now pass through compaction and storage options. [Reference](../docs/reference/agents/session-limits.mdx#compaction)
- Google ADK: the summary ends with a line that carries the ADK session state and
  the agent that replied last, so the rebuilt session keeps both.
- Claude Agent SDK: unchanged. Claude Code compacts its own transcript, and the
  adapter takes no compaction policy.
- Adapters can use `estimate_text_tokens`, `estimate_stored_input_tokens`, `select_turn_boundary_cutoff`, `describe_stored_message`, and `build_context_summary_prompt` (camelCase in TypeScript). Summary text constants move to the core root and remain exported by Stepwise.
- `CompactionPolicy.reserved_input_tokens` (`reservedInputTokens`), default 0,
  adds tokens the runner's estimate cannot see.
- `dex_agents.testing` and `@superdurable/dex-agents/testing` add
  `ContextWindowModelScript` and `ContextWindowSessionScenario`.
- No stored value changes. The conformance suite adds the summary prompt texts.
- Go core: unchanged.
- See [Compaction and the storage bound](../docs/reference/agents/session-limits.mdx#compaction).

### Spend limits

- `ModelSpendLimit` can be set on `AgentSessionOptions.spend_limit` (`spendLimit`) or a Stepwise `AgentDefinition`. `check_model_call` runs before each `RunModel` or `CompactContext` attempt, and `report_model_usage` runs when the call returns.
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

- `AgentSessionOptions.run_model_limit` (`runModelLimit`) sets `RunModel` timeout and attempts. The retry budget is the timeout times (attempts minus one), with a floor of two timeouts. Defaults stay 15 minutes per attempt, three in 30 minutes.
- Python Claude Agent SDK adapter: `claude_agent_session_flow` takes
  `run_model_limit`. The TypeScript adapter takes it through its session options.
  A turn segment that needs more than 15 minutes can now finish. A failed segment
  reruns whole.
- No stored value changes. A running session gets the limit on its next `RunModel`
  after the Worker restarts.
- Go core: unchanged.

### Sessions are searchable by the Step they wait in

- `AgentSessionClient.start_session` defaults `FlowConfig.active_step_search_mode` to `AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE` (`WITH_WAIT_FOR`) to find AwaitToolApproval and AwaitQuestionAnswer. `options.config_override`, including `DISABLED`, wins.
- `startAgentSession(client, flow, sessionId, input, options)` uses `agentSessionActiveStepSearchMode`. It is `ActiveStepSearchMode.DEFAULT` until the SDK supports `ActiveStepSearchMode.WITH_WAIT_FOR`, which the server treats the same way. `AgentSessionTestClient.startSession` also uses it.
- `Client.startFlow` and `AsyncClient.start_flow` are unchanged, so a session they
  start stays out of this search. So do sessions started on 0.2.0, until
  `UpdateFlowConfig` turns it on. No stored value changes.
- Go core: unchanged.
- See [Find sessions that need a person](../docs/reference/agents/approvals.mdx#find-sessions).

### A session at the state limit fails, with the reason

- A Step failure on `limits.maxFlowStateBytes` now closes the Flow as `FAILED` and records `failed` and `last_turn_error` if state can be written. A value over `values.maxValueBytes` still fails only its turn. [Ref](../docs/reference/agents/session-limits.mdx#at-the-limit)
- `AgentSessionClient.get_agent_snapshot` and `read_agent_snapshot` (`readAgentSnapshot`) read the Flow close to report `failed` and the reason despite stale state. A pre-state failure leaves a snapshot without state. The bare `get_agent_snapshot` RPC reads stored data only.
- Agent SSE streams now end after sending all events from a failed Flow. The TypeScript UI stream emits an `error` part with the reason and `finishReason` `error`. `AgentSessionUIMessages` adds `failure`.
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

- All three cores record `failed` when tool Step recovery reports `attempt_progress` as `NOT_STARTED`. `TOOL_CALL_NOT_RUN_ERROR_PREFIX` tells the model the call never ran and is safe to repeat. [Reference](../docs/reference/agents/failures.mdx#never-run-outcome-unknown)
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
- Private test engines set `dev.durabilityMode: processCrashSafe` in their `-zd-config` to keep tests fast after `dex-server-zd` 0.1.18 defaults to `strict`. Set the mode yourself when supplying a custom config.

### MCP tool source

- `McpToolSource` runs stdio and Streamable HTTP tools in Steps. `ToolPolicy` overrides `readOnlyHint` and `idempotentHint`. Only `read` and `idempotent_write` resend after lost responses. Python extra needs `mcp>=2.3,<3`, and the optional TS peer is `@modelcontextprotocol/sdk` `^1.32.2`.
- Both cores: `DeferredToolCall.tool_source_reference` (stored on
  `AgentToolCall` as `tool_source_reference`) and
  `DeferredToolExecution.heartbeat_checkpoint` / `save_heartbeat_checkpoint`.
- TypeScript: `toolEffectFromMcpToolAnnotations` maps `idempotentHint` to
  `idempotent_write` whatever `destructiveHint` says, as the Python adapters
  do.

### UI streams

- `@superdurable/dex-agents/ui-stream` adds resumable AI SDK `v1` streams for `useChat` with `reset-step` and `resolve_interrupt`, SSE resume via `Last-Event-ID`, and snapshots as `UIMessage` objects or wire JSON.
- `dex_agents.ui_stream`: Server-Sent Event frames resumed with
  `Last-Event-ID`, and snapshot JSON, for async and synchronous Clients.
- The testing exports' private engines accept `dex-server-zd` configuration
  overrides: `serverZdConfigPath` in TypeScript and `server_zd_config` in
  Python.
- `convertAgentSnapshotToJson` and `agent_snapshot_to_json` omit `framework_item`, `tool_source_reference`, `trace_context`, `input_continuation`, and `input_answers` from browser snapshots. Both cores produce matching JSON.
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

- `dex-agents` adds optional `otel` for `opentelemetry-api`. Set `AgentSessionOptions.tracing` with `AgentTracingOptions`. Runners may pass `ModelCallMetadata` as `model_call_metadata`. [Reference](../docs/reference/agents/tracing.mdx#options)
- `@superdurable/dex-agents` adds optional peer `@opentelemetry/api@^1.3.0`. Set `AgentSessionOptions.tracing` with `AgentTracingOptions`. Runners may return `modelCallMetadata` in either outcome. [Reference](../docs/reference/agents/tracing.mdx#options)
- `AgentTraceContext` stores the activation's valid `traceparent`, including unsampled activations. `AgentToolCall` gains `trace_context` to link tool spans to their activation root.
- A `chat` span includes the model only with `ModelCallMetadata`. Adapters use provider fields and map known IDs to `gen_ai.provider.name`. For several calls they keep common fields, omitting response IDs and finish reasons. [Ref](../docs/reference/agents/tracing.mdx#spans)
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

- `dex_agents.stepwise` adds `AgentDefinition`, `@function_tool`, `ModelClient`, `StepwiseAgentRunner`, and `stepwise_agent_session_flow`. After `max_model_calls_per_turn`, one last call offers no tools. [Limit](python/core/README.md#built-in-agent-loop)
- `dex_agents.litellm.LiteLLMModelClient` ships via `dex-agents[litellm]` (`litellm>=1.104,<1.106`). OpenAI uses Responses with `store=False` and encrypted reasoning, while others use streaming Chat Completions. Reasoning and signatures are preserved without retries inside the client.
- `dex-agents`: `ScriptedModelClient` in `dex_agents.testing`, and
  `AgentTestStack.start_worker(python_executable=...)` to run a Worker on
  another environment, such as an earlier release.
- `@superdurable/dex-agents/stepwise`: Stepwise mode:
  `createStepwiseAgentSessionFlow`, `StepwiseAgentRunner`, `functionTool`, and
  the `ModelClient` interface.
- `createAiSdkModelClient` in `@superdurable/dex-agents/ai-sdk` supports AI SDK `LanguageModel` via optional peer `ai@^7.0.159`. Each attempt makes one `streamText` call with `maxRetries` 0 and tools without `execute`. Reasoning and tool call parts retain provider metadata.
- `@superdurable/dex-agents/testing`: `ScriptedModelClient` for Stepwise tests
  without a model key.

### Stepwise tool sources, MCP resources and prompts, and the demo model

- `AgentDefinition.tool_sources` (`toolSources`) uses `ToolSource` and `SourceToolDefinition`. Sources need a stable `source_name` (`sourceName`) and supply each call's policy and idempotency key. Listings time out at 10 seconds by default. Failed sources are omitted and duplicate names fail.
- `McpToolSource` caches listings per Worker, refreshes in the background after `listing_ttl` (`listingTtlMs`, default 5 minutes), and backs off failures via `listing_failure_backoff` (`listingFailureBackoffMs`, default 30 seconds). One listing runs per server.
- `McpToolSource(..., resource_and_prompt_tools_enabled=True)` (`resourceAndPromptToolsEnabled: true`) adds `mcp_list_resources_and_prompts`, `mcp_read_resource`, and `mcp_get_prompt`. Enable it on only one source. Default is off. TypeScript works with `@modelcontextprotocol/sdk` 1.32.2.
- `dex_agents.stepwise` and `@superdurable/dex-agents/stepwise` export `ScriptedModelClient`, `ScriptedModelReply`, `ScriptedToolCall`, and `ScriptedModelResponder`. `requests` keeps 100 by default (`max_recorded_requests` / `maxRecordedRequests`). Existing `testing` imports work.

### Questions, durable waits, and incremental turn messages

Both cores, with identical Step types, RPC names, wire values, events, and
model-visible text. Everything is inert until an application enables it.

- `AgentSessionOptions.built_in_tools` (`builtInTools`) enables `ask_user` and `durable_wait`. Their `read` Steps need no approval and reject invalid arguments immediately. Optional `ask_user` arguments accept `null` as absent. [Reference](../docs/content/agent-frameworks/built-in-agent-loop.mdx)
- `ask_user` and `ToolInputRequested` raise a `question` interrupt. Submit the answer with `answer_question`. The answer becomes the tool result after `AwaitQuestionAnswer` resumes. Calls can ask in parallel, and `interrupt_deadline` applies.
- `durable_wait` waits on a whole-second Timer in the new `AwaitTimer` Step.
  `AgentSessionOptions.max_durable_wait` (`maxDurableWaitMs`) defaults to 365
  days and must be whole seconds.
- `ToolCallsRequested.new_messages` (`newMessages`) saves incremental turn items. Runners that set it implement `build_unfinished_turn_messages` (`buildUnfinishedTurnMessages`) for `RecordTurnFailure`. Without the method, a failure ends the turn in `RunModel` after one model call.
- `AgentToolCall` adds `awaiting_answer` and `waiting_for_timer`, plus input and wait data. `AgentInterrupt` adds `question`, `choices`, and `allows_free_text`, and `AgentInterruptResolution.answer` carries the answer. New values are `AnswerQuestionRequest`, `QuestionAnswer`, and `timer_started`.
- Both cores write RFC 3339 times with milliseconds and `Z`, such as
  `2026-11-01T12:00:00.123Z`. Every reader in both cores, 0.1.0 included,
  accepts the earlier `+00:00` form too.

### Plans and plan mode

Both cores, with identical Step types, persistence names, RPC names, wire
values, events, and model-visible text. Everything is inert until an
application enables it.

- `write_todos` replaces `AgentPlan` and emits `plan_updated` without a tool Step. An empty list clears the plan. Two calls in one response both fail. Approval emits the event again when the draft becomes `active`. [Ref](../docs/content/agent-frameworks/built-in-agent-loop.mdx#durable-plans)
- `AgentSessionOptions.plan_mode_enabled` (`planModeEnabled`) enables `AgentUserMessage.plan_mode` (`planMode`). Runners declare `supports_plan_mode` (`supportsPlanMode`) and planning allows only `write_todos`. [Reference](../docs/content/agent-frameworks/built-in-agent-loop.mdx#durable-plans)
- `execute_plan` approves a plan revision, queues it behind user messages, then starts an `executing` turn and emits `plan_execution_started`. A stale revision or duplicate pending approval returns false. [Reference](../docs/content/agent-frameworks/built-in-agent-loop.mdx#durable-plans)
- `AgentDefinition.planning_tool_choice` (`planningToolChoice`) defaults to `auto` and allows text. `required` forces `write_todos` on the first call if no draft exists. Python rejects it if `supports_forced_tool_choice` is false. `InstructionContext` adds `interaction_mode` and `plan`.
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

- `steer_user_message` steers with a queued ID or new text, which queues when idle. `delete_queued_message` removes queued messages. Consumed IDs raise `ChannelMessageNotFoundError`. A true `cancel_turn` records the request, and a committed answer still completes.
- `cancel_turn` stops model calls, compaction, and waits while started tool Steps finish. It records remote tasks as `outcome_unknown` with `turn_cancelled` even if `cancel_tool_task` (`cancelToolTask`) fails, and `McpToolSource` sends `tasks/cancel`.
- Steering ends at a safe Step boundary. Started calls finish, and unstarted calls record `rejected` with `AgentToolCall.cancellation_reason`. A new turn takes all steers in order and plans if any has `plan_mode` or a draft exists. Pending steers block `execute_plan`.
- `AgentState` adds `accepts_turn_control` (`acceptsTurnControl`) for new 0.2.0 turns. `SteeredUserMessages` and `TurnCancellationRequest` store requests. `ResumeTurn` waits on `TurnControlRequests`, while approval, question, and timer Steps wait on `ToolCallCancellations`.
- `get_agent_snapshot` adds `queued_messages` and `steered_messages` with message IDs next to `queued_message_count`. New events `turn_steered` and `turn_cancelled` report turn endings, and `interrupt_resolved.cancellation_reason` explains removed interrupts.
- Python `AgentSessionClient` adds `steer_user_message`, `delete_queued_message`, and `cancel_turn`. TS `AgentSessionTestClient` adds `steerUserMessage`, `steerQueuedMessage`, `deleteQueuedMessage`, and `cancelTurn`. The TS UI stream emits `finish` reason `other` on steer or cancel.
- `build_unfinished_turn_messages` (`buildUnfinishedTurnMessages`) is also
  called when a steered or cancelled turn ends. A runner that keeps its items
  in run state may implement it to keep its exact transcript.

### Tool tasks

Both cores, with identical Step types, wire values, events, and error texts
(the missing-poller text names each language's method). Inert until a runner
returns the new outcome.

- `execute_deferred_tool_call` can return `ToolTaskStarted(task_id, poll_interval_seconds)` for remote work. The call records `waiting_for_task`, stores `task_id` and `task_poll_interval_seconds`, emits `tool_task_started`, and continues in `PollToolTask`.
- `PollToolTask` calls `ToolTaskPoller.poll_tool_task(ToolTaskPoll)` after a Timer. `QuestionAnswer` resolution reaches the next poll once via `ToolTaskPoll.pending_answer`. Polls get three attempts. Steering waits for started tasks, and `cancel_turn` abandons them.
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

- `AgentSessionOptions.compaction` takes `CompactionPolicy`, with `trigger_ratio` 0.85 and `retained_ratio` 0.3 by default. Flow construction rejects it unless the runner supports compaction. [Reference](../docs/reference/agents/session-limits.mdx#compaction)
- `CompactContext` runs before `RunModel` would exceed the threshold, with the same retry policy. Runners receive `AgentTurnContext.context_summary` (`contextSummary`) and only newer messages. Retention later removes older ones. [Ref](../docs/reference/agents/session-limits.mdx#compaction)
- `AgentDefinition.compaction` summarizes mid-turn without splitting replies from tool results. If the request plus `max_output_tokens` exceeds `ContextSummaryRequest.input_token_budget`, the summary request omits the oldest messages at a safe boundary and reports the omission.
- Each `CompactContext` attempt emits a `chat` span for the summary call, as
  `RunModel` does, named from the optional
  `ContextSummaryResult.model_call_metadata` (`modelCallMetadata`).
- The `message_count` of `turn_completed`, `turn_steered`, and
  `turn_cancelled` counts every message the turn appended, also those a
  mid-turn compaction summarized. A steer or cancellation that arrives during
  `CompactContext` applies at the next `RunModel`, before any model call.
- Compaction adds `compacting_context`, `ContextSummary`, `CompactContextInput`, `AgentSnapshot.context_summary`, and `compaction_started` / `compaction_finished`. Browser snapshots expose `context_summary`, and the AI SDK UI stream excludes summary-call usage from message steps.
- `AgentSessionStatus` adds `COMPACTING_CONTEXT` in Python and `"compacting_context"` in TypeScript. Update exhaustive Python matches and upgrade 0.1.0 snapshot readers before enabling compaction.
- The 2,000-turn compaction sessions that calibrate the deployment guide run
  in the new `Agents Long Tests Nightly` workflow: Python with
  `DEX_AGENTS_LONG_TESTS=1`, TypeScript with `npm run test:long`. `Agents CI`
  runs an 800-turn guard in each core instead.

### Chunked messages and retention

- `AgentMessages` keeps the newest 64 (`message_window_size`). `AgentMessageChunks` defaults to 32 messages (`message_chunk_size`) and 1 MiB (`message_chunk_max_bytes`, max 8 MiB). `AgentMessageIndex` tracks chunks. Oversize messages get their own chunk.
- `RunModel` no longer loads the whole message map. With compaction, it loads
  the window and only the chunks after the summary; the snapshot and
  `list_agent_messages` load the window and every kept chunk.
- Retention deletes fully summarized chunks when `retained_message_limit` newer messages follow (default 1,000, `None`/`null` keeps all). Copy transcripts first. `AgentMessagesPage.deleted_before_sequence` marks deletion. [Ref](../docs/reference/agents/session-limits.mdx#message-retention)

### Go core

- `github.com/superdurable/dex/agents/go/core` (`dexagents`) adds the session Flow, RPCs, `AgentEvents`, and `AgentFrameworkRunner`. Go reads shared state and writes `accepts_turn_control` false, so steering RPCs refuse its turns. [Go core](go/core/README.md)
- Go `stepwise` adds `NewAgentDefinition`, `NewFunctionTool`, `NewTypedFunctionTool`, `StepwiseAgentRunner`, and `NewStepwiseAgentSessionFlow`. Official SDK clients are `openaimodel.NewResponsesModelClient` and `anthropicmodel.NewMessagesModelClient`.
- `agentstest`: a scripted model client whose items match the Python core's,
  a private `dexcli dev` engine, killable Worker processes, and Step history.
- Python and TypeScript Clients drive Go Workers, and Go Clients drive Python Workers. Serve each Flow type in one language because Go fails sessions with steering, planning, or compaction state, and `dex.RPCOptions.Name` requires a Go SDK newer than `sdk-go/v1.32.0`.
- The Anthropic client stores the LiteLLM chat message that the Python core
  stores (`anthropic`, `chat_completions`), so either core replays the other's
  thinking blocks, and it replays the TypeScript core's AI SDK Anthropic items
  with their signatures. An empty response stores no item.

### Fixes

- TypeScript `send_user_message`, `AwaitUserMessage`, and `StartAgentSession` follow Python `str.strip()` whitespace. U+001C to U+001F and U+0085 alone count as blank, and U+FEFF alone counts as a message.
- MCP configuration representations redact every `env` and `headers` value as `***`. This covers Python `repr` and `str` of both server config types, and TypeScript `util.inspect` and `console.log` of `McpToolSource`. Servers still receive the values.
- TypeScript: `send_user_message` is a transactional RPC, as the contract and
  the Python core declare it.
- Python: `ScriptedModelClient` writes non-ASCII text in tool arguments and
  output items unescaped, as the TypeScript client and Python function tools
  do.
- MCP structured and non-text results use sorted JSON keys, `json.dumps` spacing, and unescaped non-ASCII in both cores.
- TypeScript: MCP resource and prompt tool output sorts integer-like keys by
  code point, as Python does. It put them first, in numeric order.
- Python: the test engine in `dex_agents.testing` tries three times on new
  free ports when another process takes its port, as the TypeScript one does.

### Conformance suite

- `agents/conformance/` holds golden wire values, the names manifest, the
  model-visible text table, and scripted-turn transcripts that every core
  must reproduce. Both cores run it in their CI suites.

### Compatibility

- **Upgrade rule: stop every 0.1.0 Worker of a Flow type before any 0.2.0 Worker of it starts. Do not run them side by side.** 0.2.0 moves older messages to `AgentMessageChunks`, which 0.1.0 cannot read. [Reference](../docs/reference/agents/upgrades.mdx)
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
- Use `steer_user_message` and `cancel_turn` after all Workers run 0.2.0 and turns active during rollout end. They refuse turns with `AgentState.accepts_turn_control` false. A steer sent while idle queues on either version.
- `ResumeTurn` now locks `PendingInterrupts` in its short Execute, so
  `resolve_interrupt` and `answer_question` can raise `RpcLockConflictError`
  in sessions that never steer. `AgentSessionClient` retries it for 5 seconds;
  direct `invoke_rpc` callers should retry it too.
- Python `ToolCallStatus` adds three non-final members. Update exhaustive matches and `assert_never`. Bare `AgentFrameworkRunner` means `AgentFrameworkRunner[ToolExecutionResult]`. Question-capable runners and `AgentSessionFlow.runner` use `AgentFrameworkRunner[DeferredToolOutcome]`.
- `AgentSnapshot.toolCalls` and tool records from `Client.getAttributes` use `AgentToolCallRecord`. Widen `AgentToolCall` assignments for `awaiting_answer`, `waiting_for_timer`, and `waiting_for_task`. Question-capable runners use `AgentFrameworkRunner<DeferredToolOutcome>`.
- TypeScript Claude Agent SDK counts subagent calls in `AgentUsage.modelCalls`. The cumulative `AgentUsage` counts only main-conversation calls from turns that ran on 0.1.0 and includes subagent calls from later turns.
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
