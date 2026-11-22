# Changelog

All twenty-six published Dex agents packages share this changelog and one version line. A
release is tagged `agents/v<version>`. See [README.md](README.md#versions-and-compatibility)
for the Dex SDK and framework versions each release supports.

## 0.6.15 (2026-11-18)

### Changed

- Python and TypeScript Connector tools now classify outcomes by branch alone. A Mutation's **defect** and unlisted branches record **failed**, as does **rejected** after an earlier attempt. [Reference](/reference/agents/connector-tools#outcomes)
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
- Python agents packages and CLI commands require **dex-python-sdk** 1.45.8. TypeScript packages and CLI commands require and peer-depend on **@superdurable/dex** 1.45.8, which sends null for an unset RPC input so Python Workers can serve the call. (#2034)

### Fixed

- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk** resume deferred calls to renamed or removed tools with recorded results. In-flight single-attempt calls stay **outcome_unknown** without rerunning, as do retries of a removed **idempotent_write**.
- **dex-agents-google-adk** now resumes **RunModel** with recorded results for paused calls to renamed or removed tools. In-flight single-attempt calls keep **outcome_unknown** without rerunning. The next model call uses the new tool list.
- Python and TypeScript record **outcome_unknown** when `ToolExecutionError` refuses a write retry, including **idempotent_write** after a deploy renames the tool or changes its schema. [Reference](/reference/agents/failures#retry-refusal)
- **dex-agents-openai-agents** and **@superdurable/dex-agents-openai-agents** rebuild a turn's history from recorded results when a renamed or removed tool prevents resuming **RunState**. In-flight single-attempt calls keep **outcome_unknown** without rerunning.

## 0.6.14 (2026-11-16)

### Added

- **StopFlow** with **GracefulComplete** ends Python, TypeScript, and Go agent sessions as **COMPLETED** after running and queued turns finish. Upgrade to server-zd 0.1.34+ and all Workers of the session's Flow type to 0.6.14+. [Reference](/reference/agents/session-limits#end-a-session)

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

- **fork_agent_session**, **forkAgentSession**, and **AgentSessionClient.fork_session** fork a session between turns. **AgentSessionForkingFlow** and **createAgentSessionForkingFlow** let clients fork without building the agent. [Reference](/reference/agents/cli#fork)
- **fork SESSION_ID [--at N]** and **/fork [N]** fork a session and offer to open it. Sessions from before 0.6.8 keep their task queue. Forks need server-zd clone fork support, available in **dexcli** 1.47.8. [Reference](/reference/agents/cli#fork)

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

- `dex-agents-session-graph` adds Connector Trigger bindings via `--trigger-binding <connectionName>=<packageName>@<packageVersion>:<triggerName>[#<factoryName>]:<bindingName>` or `--trigger-bindings <file.json>`. [Reference](/reference/agents/dex-web#trigger-bindings)
- Connector Mutations resend after unknown outcomes when the manifest and operation agree on `agent.repeatOnUnknown: limited` and `agent.maxSends` of 2–10. Calls pin that cap and default to three attempts if attempts are unlimited. [Reference](/reference/agents/connector-tools#bounded-resends)
- `ToolCallStepOptions.bounded_resend_execute_retry`
  (`boundedResendExecuteRetry`) gives a `non_idempotent_write` call whose tool
  enforces its own send cap more than one attempt in `CallSingleAttemptTool`;
  when the attempts run out, the call records `outcome_unknown`.
- `ToolPolicy.max_sends` (`maxSends`) overrides a Connector tool's send cap with 1–10. Use `1` for one attempt, or `@superdurable/connectors-sdk` 0.11.0+ or `dex-connectors-sdk` 0.10.0+ to enable resends. [Reference](/reference/agents/connector-tools#max-sends)
- Upgrade all Workers of a session Flow type to 0.6.12 before enabling `limited` resends and keep them upgraded while calls are open. Older Workers can exceed the pinned TypeScript send cap. [Reference](/reference/agents/connector-tools#bounded-write-upgrade)

### Changed

- Python, TypeScript, and Go cap tool results in failed, steered, or stopped turn notes at 2,000 UTF-8 bytes per call, cut at a character boundary with an omission marker. Stored tool records keep the full output.

### Fixed

- TypeScript CLI commands show known image storage failures and unreadable-file causes, and format size limits with commas. The UI and Worker log use safe storage reasons or a generic message to avoid exposing bucket URLs or credentials.
- `@superdurable/dex-agents-claude-agent-sdk` stores partial transcripts during `RunModel` deferrals. `ClaudeAgentRunner.buildUnfinishedTurnMessages` records the final result of an abandoned call, and `RunModel` supplies unknown outcomes for deferrals ended by older Workers.
- `ConnectorToolSource` keeps a Connector branch `outcome_unknown` when a
  `sendCount` on an earlier attempt's fence marker counts a send that may have
  taken effect, in both cores. It also does so when an earlier attempt's
  claim stands but the operation returned before it read that record.
- `ConnectorToolSource` records `outcome_unknown` when a retry rejects arguments and an earlier attempt claimed or counted a send. First attempts and retries without that evidence record `failed`. [Reference](/reference/agents/connector-tools#outcomes)
- `@superdurable/dex-agents` recognizes a capped fence's `sendCount` when classifying a retry. In 0.6.11, `@superdurable/connectors-sdk` 0.11 was outside the peer range and an optional branch could record `failed`. [Reference](/reference/agents/connector-tools#outcomes)
- `dex-agents-google-adk` uses `AdkAgentRunner.build_unfinished_turn_messages` to store events and answer paused calls in each `ParallelAgent` branch after steering, cancellation, or failure. Successful calls show `UNFINISHED_TURN_SUCCEEDED_TOOL_RESULT` without raw output.
- `dex-agents-langgraph` uses `LangGraphAgentRunner.build_unfinished_turn_messages` to restore checkpoint messages and add a missing `ToolMessage` for each call of the final `AIMessage` after steering, cancellation, or failure. This includes Deep Agents.
- `@superdurable/dex-agents-mastra` uses `MastraAgentRunner.buildUnfinishedTurnMessages` to preserve messages after steering, cancellation, or failure. Each pending tool invocation receives its recorded `result`.
- `dex-agents-openai-agents` uses `OpenAIAgentsRunner.build_unfinished_turn_messages` to preserve `RunState` items after steering, cancellation, or failure. Any unanswered call gets a `function_call_output` from its final record.
- `@superdurable/dex-agents-openai-agents` uses `OpenAIAgentsRunner.buildUnfinishedTurnMessages` to preserve `RunState` items after steering, cancellation, or failure. Any unanswered call gets a `function_call_result` from its final record.
- `dex-agents-claude-agent-sdk` stores deferred `RunModel` transcripts and records abandoned tool results with `ClaudeAgentRunner.build_unfinished_turn_messages`. Deferrals ended by 0.6.11 get unknown results keyed by assistant entry and tool use ID, matching TypeScript Workers.
- `@superdurable/dex-agents-claude-agent-sdk` keys `buildUnfinishedTurnMessages` and `RunModel` results by assistant entry and tool use ID, so later turns can reuse IDs without losing or duplicating results. Claude Code CLI 2.1.316 still shows a reused ID as "[Tool use interrupted]".
- `dex-agents-pydantic-ai` saves partial `RunModel` messages and uses `PydanticAIRunner.build_unfinished_turn_messages` to fill missing `ToolReturnPart` results. Workers at 0.6.11 or earlier cannot read the new state. Drain or upgrade them together for each Flow type.
- Agent package licenses link to the repository’s current legacy notices location.
- `@superdurable/dex-agents-vercel-ai` saves partial `RunModel` messages and fills missing tool results, hiding raw `toModelOutput` values with `toolCallOutputOmittedResult`. Version 2 state fails on 0.6.11. Upgrade all Workers of each Flow type together.

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

- `connectors.mcp: auto` starts `dexcli connectors mcp --exit-on-stdin-close` for local connections, with a 120-second timeout and 1 MiB ready-line limit. Startup failures fall back to Dex Web MCP only with `dex.server: auto`. Requires `dexcli` 1.47.7. [Reference](/reference/agents/cli#connectors)
- Terminal leases default to 600 seconds, up from 15. Set `sessions.lease_ttl_seconds` to 10–3600 or override it with `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS` (1–3600). Lease reads time out after 5 seconds. [Reference](/reference/agents/cli#one-terminal-per-session)

### Fixed

- `init` updates only prompted settings in a valid file, and `--yes` uses the existing values. It backs up invalid files as `NAME.yaml.bak`, writes atomically through symlinks, and prompts for shell commands that skip approval in both languages. [Reference](/reference/agents/cli#first-run)

## 0.6.8 (2026-11-14)

### Added

- New sessions use `AgentHarness.flow_type` (`flowType`) and a per-session queue from `session_task_queue` (`sessionTaskQueue`). The CLI starts pulling that queue when the lease is free and still serves older sessions on the default queue. [Reference](/reference/agents/cli#resume)
- `acquire_session_terminal_lease` stores the optional `environment` in `SessionLastTerminalEnvironment` on a new acquisition. Renewals, refusals, and releases leave it unchanged. Older Workers ignore the field.

### Changed

- The TypeScript agents packages and CLI commands depend on, and peer-depend
  on, `@superdurable/dex` 1.45.3, so an application can use
  `PullWorkerOptions.taskQueue`, `StartFlowOptions.taskQueue`, and
  `FlowState.taskQueue` from that release.
- The CLI commands download dexcli 1.47.5 when **DEXCLI** names none.
- Terminal UI session switches now send the new `flow_type` and takeover notices without `previous_session_id`. The CLI falls back to plain chat unless `dexcli agents-tui --describe` reports `launcher_session_switch`. [Reference](/reference/agents/cli#one-terminal-per-session)
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
- `ClaudeAgentRunner` and `dex-agents-claude-sdk` now poll and cancel `ToolTaskStarted` through `pollToolTask` and `cancelToolTask`, with cancellation recording `outcome_unknown`. `ToolInputRequested` waits for the user's answer. MCP tasks already polling stay in their tool Step.

### Changed

- `McpToolSource` only hands a retried `CallTool` task back to the session as `ToolTaskStarted` when the heartbeat has `mcp_task_polled_by_session`. Other tasks keep polling in the tool Step and resend with the same idempotency key if the server loses them.

### Fixed

- `executeDeferredToolCall` now returns `Promise<DeferredToolOutcome>`. Update overrides and check for no `kind` before `.output`. Infer or use `AgentSessionFlow<ClaudeAgentRunner>` for `createClaudeAgentSessionFlow`, or `AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>` for any adapter.

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

- `mcp_servers` configures remote MCP. Header expansion blocks `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE`; values ≥8 characters are redacted. Workers reject unsent calls missing required approval or single-attempt policy. [Reference](/reference/agents/cli#mcp-servers)
- `@superdurable/dex-agents-pi-cli` and `dex-agents-deepagents-cli`: writes of
  `mcp_servers` tools wait for approval, as connector writes do.

## 0.6.3 (2026-11-13)

### Added

- `SessionTerminalLease` and `acquire_session_terminal_lease` / `release_session_terminal_lease` let clients acquire a free or expired lease, renew their own, or release it. Upgrade all Workers of the Flow type to 0.6.3 first. [Reference](/reference/agents/cli#one-terminal-per-session)
- Each CLI holds a 15-second terminal lease by default, renews it every 5 seconds, and releases it on exit. A second CLI waits to take over. Override with `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS`. [Reference](/reference/agents/cli#one-terminal-per-session)
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

- `dex.auth` and `dex.tls` enable authenticated TLS connections for remote commands. Authentication is enabled only by `dex.auth`, with credentials from `DEX_API_KEY`, `DEX_CREDENTIAL`, or a token file. [Reference](/reference/agents/cli#remote-server)
- `resolve_dex_connection_settings` and `resolveDexConnectionSettings` return `EffectiveConfig.dex` connection settings without secrets. New validation codes are `insecure_token_url`, `auth_needs_server_url`, and `tls_needs_https_server`. [Reference](/reference/agents/cli#remote-server)
- Terminal UI contract 1: the launch document's `dex.auth` and `dex.tls`
  carry the command's settings instead of `none` and the defaults. The
  terminal UI gets the one credential variable that `dex.auth` names.
- CLI child processes lose `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE` even without `dex.auth`. Only the Worker host and terminal UI get the credential variable required by the selected mode. [Reference](/reference/agents/cli#remote-server)
- `sender_name` (`senderName`, `SenderName`) adds a self-declared display name without changing `AgentMessage.text`. `send_user_message` and `steer_user_message` reject invalid names and names sent with `message_id`. [Reference](/reference/agents/dex-web#sender-names)
- The session graph declares the optional `sender_name` input of the
  `steer_user_message` Action.
- `describe_stored_message` (`describeStoredMessage`), which builds the text
  of a context summary prompt, shows a named user message as
  `user: <name>: <text>`.
- `reasoning_effort` applies to every model call from the next turn. Harnesses declare `supported_reasoning_efforts` (`supportedReasoningEfforts`) and may list `suggested_models` (`suggestedModels`). Unsupported values fail the turn. [Reference](/reference/agents/cli#model-and-effort)
- `resolve_turn_config`, `resolveTurn`, and `TurnConfigResolver` accept optional supported reasoning values and return the selected effort. The UI reserves `default`; suggested model IDs exclude spaces and control characters. [Reference](/reference/agents/cli#model-and-effort)
- Terminal UI contract 1: the launch document carries the optional
  `display.models` and `capabilities.reasoning_effort`, which dexcli 1.47.0
  reads for `/model` and `/effort`. Older dexcli releases ignore them.
- `dex-agents-claude-sdk` maps `reasoning_effort` to `effort` with `low`, `medium`, `high`, `xhigh`, and `max`. Built-in, OpenAI, Pydantic AI, Vercel AI, and Pi support the first three. All commands suggest models for `/model`. [Reference](/reference/agents/cli#model-and-effort)

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

- `dex-agents-deepagents` uses native tools, with writes and shell commands running without approval by default. Add `write_file`, `edit_file`, `delete`, `execute`, or `task` to `approval.always`; `local_tools.shell.allow` is ignored. Use absolute `file_path` for `read_file`, `write_file`, and `edit_file`. Replace `list_directory`, `search_files`, and `run_shell_command` with native tools. [Reference](/reference/agents/cli#harness-tools)
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
- Go Clients can call Python and TypeScript sessions with `remotesession`, `RemoteSessionFlow`, and `RemoteSessionClient`, including the `AgentEvents` Stream and session RPCs. The Flow is client-only and Go Workers refuse it.
- `AgentInterruptDisplay` adds a target, summary, and preview to approvals. `DeferredToolCall.approval_display` (`approvalDisplay`) supplies them, with limits of 1 KiB, 2 KiB, and 16 KiB. Missing display falls back to arguments. Workers before 0.6.0 drop it on rewrite.
- `ConnectorToolSource` shows the operation, connection, and up to five short arguments for approval via `build_connector_tool_approval_display` (`buildConnectorToolApprovalDisplay`). Credential redaction is best-effort and display-only. Stored arguments retain their full values.
- `write_file` and `edit_file` approvals show a workspace-checked diff. Omit it for credential files, redacted content, refused paths, over 64 KiB, or over 200 lines. `run_shell_command` shows the working directory and a redacted command while stored arguments stay complete.
- `AgentHarness.defaultWorkspaceLocation` lets TypeScript harnesses use `launch_directory` when no workspace is set, so `init` omits `workspace`. `ConnectorsMcpToolSource.deferredToolCall` and `executeDeferredToolCall` can match an alternate tool name in `approval.always`.
- `@superdurable/dex-agents-pi-cli`: `PrefixedConnectorToolSource` and
  `collidingConnectorToolPrefix`, which offer the connector tools beside Pi's
  tools.
- `AgentMessage.origin` distinguishes session-written `plan_execution`, `turn_failure_note`, `turn_steered_note`, and `turn_cancelled_note` from user input. Readers keep unknown values. Workers before 0.6.0 drop the field when archiving.
- `AgentSessionOptions.summary_attributes_indexed` (`summaryAttributesIndexed`) adds indexed `AgentSessionTitle`, `AgentSessionLastActivityAt`, and `AgentSessionTurnCount`. It defaults to off. [Reference](/reference/agents/approvals#list-sessions)
- Python and TypeScript adapter factories now accept `summary_attributes_indexed` or `summaryAttributesIndexed` for indexed session summaries. [Reference](/reference/agents/approvals#list-sessions)
- Indexed titles expose the user's own words to anyone who can search the namespace. PostgreSQL index conflicts on `text1`, `datetime1`, or `int1` can prevent Worker startup with `FAILED_PRECONDITION`. [Reference](/reference/agents/approvals#list-sessions)
- `list_agent_sessions` / `listAgentSessions` page through sessions with optional title search in one `SearchFlows` call, without a Worker. Use `sort_agent_sessions_by_last_activity` / `sortAgentSessionsByLastActivity` for loaded sessions. [Reference](/reference/agents/approvals#list-sessions)
- Earlier sessions gain activity time and turn count on their next turn with indexing enabled; only sessions without prior turns can gain a title. Older and Go Workers leave summary values as-is. Turning indexing off keeps existing values. [Reference](/reference/agents/approvals#list-sessions)
- `sessions.summary` defaults to `auto`, indexing only with `dex.server: auto`; `on` and `off` apply everywhere. Harnesses must honor it. `sessions` lists up to 250 sessions by latest activity, with turn time, count, and title. [Reference](/reference/agents/cli#configuration)
- The Python CLI commands (`dex-agents-cli-core`) accept `--plain`, which
  keeps the chat in plain text lines instead of the `dexcli` terminal UI.
- The TypeScript CLI commands (`@superdurable/dex-agents-cli-core`) take
  `--plain`, which keeps the chat in plain text instead of the `dexcli`
  terminal UI. Pi always keeps the plain text chat, through the new
  `AgentHarness.terminalInterface` declaration (`"plain"`).

### Changed

- `dex-agents-claude-sdk` uses `strictMcpConfig` (`strict_mcp_config`) and `disableClaudeAiConnectors` to block MCP servers and connectors from Claude Code configuration or login. The CLI uses built-in, browser, and Dex connector tools; application adapters keep their defaults.
- **dex-agents-builtin** enables plan mode with `write_todos`. In the terminal UI, `/plan` drafts with that tool and `/run` approves and runs with every tool. The session API supports planning; the plain chat has no plan commands. [Reference](/reference/agents/cli#terminal-ui)
- **dex-agents-builtin**: `write_todos` is now a reserved tool name, so a
  connector or local tool with that name fails the turn. Rename the
  conflicting tool, or use another harness.
- The Python terminal UI displays Worker host startup notices and `dexcli dev` details, such as unavailable connectors and the local server's log path. [Reference](/reference/agents/cli#terminal-ui)
- `dexcli dev` started for `dex.server: auto` now stops with the CLI on exit, failure, SIGTERM, or SIGHUP. The CLI removes `~/.dex/agents/dexcli-dev.json` and signals the running server's process group, escalating after 20 seconds or another signal. [Reference](/reference/agents/cli#dex-server)
- CLI exit leaves reused `dexcli dev` running. Start your own or set `dex.server` to share it. If the reused server stops, the CLI exits 1. A crash or SIGKILL can leave a started server running for later reuse. [Reference](/reference/agents/cli#dex-server)
- On Windows, TypeScript stops its `dexcli dev` tree with `taskkill /T` and then `taskkill /T /F`; Python sends CTRL_BREAK_EVENT first. Python stops using `os.kill(pid, 0)` for a check that could kill the server. [Reference](/reference/agents/cli#dex-server)
- TypeScript CLI core: during the chat, the Dex SDK Worker's task stream
  reconnect messages go only to `~/.dex/agents/<harness>.log`, so a lost Dex
  server no longer prints stack traces into the chat. Other library warnings
  show their first line on the terminal and their full text in the log file.
- TypeScript CLI core: closing the terminal no longer ends a command on Linux
  with an uncaught `setRawMode EIO` error before it handles SIGHUP. The
  command now stops the `dexcli dev` it started and exits with 129.
- `dex-agents-deepagents` uses native tools, `write_todos`, and `task`. Connector writes need approval. File tools block paths outside the workspace; `execute` runs with your permissions. `task` runs in one Step without its own approvals. [Reference](/reference/agents/cli#harness-tools)
- LangGraph's `should_defer_tool_calls_before_middleware` defaults to `True`, running wrapped root tools' middleware in each Step attempt. Set `should_defer_tool_calls_before_middleware=False` for prior ordering. In-flight 0.5.x turns that opted in may run one earlier wrapper in `RunModel`.
- Python LangGraph adapter: a tool Step that runs a tool node's wrappers no
  longer applies the node's `handle_tool_errors`, so an exception in the tool
  body, such as a `ValidationError`, fails the attempt and a write records
  `outcome_unknown`, as it does without wrappers.
- Python LangGraph adapter: an `InjectedStore` parameter of a tool receives
  the compiled graph's store in its tool Step.
- TypeScript agents packages now include Node 26 (`^26.0.0`) in **engines.node**, removing `EBADENGINE` warnings. The Pi packages still require `^22.19.0` as their lower bound.
- `dex-agents-pi` keeps native `read`, `grep`, `find`, `ls`, `write`, `edit`, and `bash` in separate Steps. Reads retry; writes and shell run once. `pi.tool_progress` streams shell output. Connector writes always require approval. [Reference](/reference/agents/cli#harness-tools)
- Python and TypeScript CLI cores: the commands download `dexcli` 1.46.0,
  which has the terminal UI, instead of 1.45.0 when they find no `dexcli` of
  at least 1.46.0. A `dexcli` on `PATH` older than 1.46.0 is no longer used.
- `run` defaults to `dexcli agents-tui` at a supported terminal. Use `--plain` for plain chat, also selected without a terminal, with unset or `dumb` `TERM`, on Windows, in `dex-agents-pi`, or when the terminal UI cannot start. [Reference](/reference/agents/cli#terminal-ui)

### Fixed

- Python and TypeScript Claude Agent SDK adapters show the user's rejection reason for a Claude Code tool such as `Write` or `Bash` without a hook-error prefix. Other rejection text stays as recorded, and the call remains `rejected`.
- Approved Claude Code calls emit `tool_call_started` from the tool Step and `tool_call_finished` from the resumed turn, recording `outcome_unknown` if it ends without a result. A retried `RunModel` can run the tool and emit its result again. [Reference](/reference/agents/changelog#v0-6-0)
- `dex-agents-claude-sdk` sends Claude Code stderr to the log with terminal controls such as `ESC[?25h` removed. Failed log writes drop the line so it cannot affect the terminal.
- Claude Code logins now work after the first turn. Resumed segments set `CLAUDE_SECURESTORAGE_CONFIG_DIR` to the effective `CLAUDE_CONFIG_DIR`, or `""` if unset, while honoring a value already set by the application or Worker.
- Ctrl-C during `init` or first-run setup now exits both CLI cores with code 130 and prints `Cancelled; no configuration written.` No configuration file is written. [Reference](/reference/agents/cli#first-run)
- Ctrl-D at a configuration question now cancels the same way and exits with
  code 1. Before, the Python commands took the default answer and went on.
- An unreachable Dex server now stops the CLI with its address and a fix in one line. Reachable servers returning `UNAVAILABLE` show their details and a retry hint. [Reference](/reference/agents/cli#remote-server)
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
- The Python shell tool stops processes left in a command's group when it exits, sending SIGTERM and then SIGKILL 0.5 seconds later. Processes still holding output no longer delay the call. [Reference](/reference/agents/cli#local-tools)
- A process that left the command's group, for example with `setsid`, and
  still holds its output no longer delays the result by more than 1 s after
  the group is cleaned. It is not stopped.
- The Python shell tool reports `killed: timed out after N seconds` as a result and `exit code 128+N` for signals. It always includes `--- stdout ---` and `--- stderr ---`, cutting each stream at 64 KiB while reading. [Reference](/reference/agents/cli#local-tools)
- A cancelled shell tool call now finishes stopping the command's group,
  reaping the shell, and closing its pipes before the cancellation goes on.
  One thread per call owns the shell, so this cleanup also finishes when the
  event loop closes or the interpreter exits.
- The shell tool now gives what a command leaves in its group up to 0.5 s
  after SIGTERM, even when the command's output already ended, and stops the
  group even when its output cannot be read. A command that leaves nothing
  behind gets no signal and no wait.
- The CLI prints one short notice when connector tools become unavailable or recover. Harnesses with a fixed tool list show a restart hint. Full listing errors go only to `~/.dex/agents/<harness>.log`. [Reference](/reference/agents/cli#connectors)
- Python and TypeScript CLI cores: a connectors MCP server that answers HTTP
  401 or 403, also only to `tools/list`, gets a line saying it denied access
  and to check its access settings and `connectors.mcp`, instead of a line
  saying each turn tries again.
- Python CLI core: a command whose `~/.dex/agents` directory or
  `<harness>.log` file cannot be written prints `Logging is off` with the
  reason and starts without a log file, instead of exiting with an error.
- **ConnectorsMcpToolSource** supports startup availability, logging, and unavailable-message hooks. Python raises `ConnectorsMcpAccessDeniedError` on 401/403. TypeScript `readIdentities` and `listConnectorToolIdentities` take an `AbortSignal` and time out each request at 15 seconds.
- Python and TypeScript cores: **McpToolListingError** carries the HTTP
  status a Streamable HTTP server answered to the failed listing, as
  `httpStatus` in TypeScript and `http_status` in Python.
- TypeScript core: **McpToolSource** takes a `logWarning` option that receives
  its warnings, such as a server left out of the tool listing, instead of
  `console.warn`. A `logWarning` that throws falls back to `console.warn`, so
  it never fails `listTools()` or a tool call.
- `sessions` keeps paging until it finds 250 sessions, reaches the end, or makes 20 requests. Short pages no longer end the list early, and it reports when more sessions remain. [Reference](/reference/agents/cli#resume)
- `runAgentCli` waits up to 2 seconds for stdout and stderr to drain before returning, throwing, or exiting on SIGTERM, SIGHUP, or SIGINT. Piped `sessions` lists keep their closing `Resume one with` line.
- The CLI starts and retries listings each turn if MCP `initialize` succeeds but `tools/list` fails. It prints one notice and logs the error. An explicit `connectors.mcp` URL that fails initialization still stops startup. [Reference](/reference/agents/cli#connectors)
- **PrivateDexEngine** no longer downloads or runs Connectors MCP. It sets `DEX_CONNECTORS_MCP_BINARY` to a missing file and isolates `HOME` under the test state directory so the user's `~/.dex` stays untouched.
- `should_defer_tool_calls_before_middleware` runs LangGraph tool middleware on every Step attempt and records non-message state changes. `dex-agents-langgraph` requires `langgraph` 1.2, `langgraph-prebuilt` 1.1, and `langchain-core` 1.7.
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

- All agents packages pin Dex SDK 1.45.0 (`dex-python-sdk==1.45.0`, `@superdurable/dex` `1.45.0`, Go `sdk-go` `v1.45.0`), up from 1.38.0. This supports **Join** and Worker-side Flow definition uploads; npm adapters pin the same SDK peer.
- Dex SDK stays pinned exactly. Review SDK 1.43 and 1.45 breaking changes before upgrading. [Python](../docs/reference/python/changelog.mdx), [TypeScript](../docs/reference/typescript/changelog.mdx), [Go](../docs/reference/go/changelog.mdx)
- Dex SDK 1.45.0 still needs a Dex Server with protocol 4: server-zd v0.1.17
  or later.
- Agents CI, the nightly long run, and the publish workflows test against
  `dexcli` 1.45.0. The Python and TypeScript CLI cores download `dexcli`
  1.45.0 when none is installed.

### Dex MCP conventions

- **McpToolSource** honors Dex `_meta` keys `io.superdurable.dex/effect` and `io.superdurable.dex/approval: recommended` over annotations. Policies can only raise effect or approval, and writes run in model order. [Reference](/reference/agents/changelog#v0-5-0)
- New `McpToolSource.merge_tool_policies` (`mergeToolPolicies`) gives a
  framework adapter the application's tool policies merged by the same rule.
  Use it instead of spreading your policies after `tool_policies()`
  (`toolPolicies()`), which would let them lower a declared effect.
- A tool result's `_meta` key `io.superdurable.dex/outcome` (or a JSON-RPC
  error's `data._meta`) set to `unknown` records `outcome_unknown` and never
  sends the call again; `rejected` records `failed`. Either wins over `isError`.
- `dex_agents.mcp` and `@superdurable/dex-agents/mcp` export Dex MCP metadata keys. `McpTool` adds `dex_effect` / `dexEffect` and `is_approval_recommended` / `isApprovalRecommended`.
- Both cores: `poll_tool_task` (`pollToolTask`) may raise
  `ToolOutcomeUnknownError`; the session records `outcome_unknown` and stops
  polling. It used to fail the poll attempt.
- `agents/conformance/mcp_dex_conventions.json` fixes the conventions for every
  core.
- `McpToolSource.execute_deferred_tool_call` accepts `request_meta` for `tools/call`; the idempotency key wins on a clash. `McpToolSource.will_resume_tool_task_without_sending` reports whether this call can only resume an already-started task.

- **McpToolSource.executeDeferredToolCall** accepts `executeDeferredToolCall(call, { requestMeta })` to add `_meta` to `tools/call`, such as expected tool identity. The idempotency key wins on a clash.
- **McpToolSource.willResumeToolTaskWithoutSending(call)** is `true` only when `executeDeferredToolCall` returns an already-started, session-polled task. A task polled inside the tool Step can send `tools/call` again if the server lost it.

### Showing sessions in Dex Web

- **session_flow_definition_document** (`sessionFlowDefinitionDocument`) lets Workers upload a session graph at startup for Dex Web. Requires server-zd 0.1.26+; use **render_session_flow_graph** on older servers. [Reference](/reference/agents/dex-web#upload-the-graph)
- Session graph documents use a fixed `generated_at` from `SESSION_FLOW_GRAPH_GENERATED_AT`. Workers replace the stored graph when the template advances. For Action permission-only changes, upload once with `force`. [Reference](/reference/agents/dex-web#definition-authority)
- The CLI uploads its suffixed session graph at startup, so sessions appear in Dex Web with `dexcli dev` 1.45.0. Session Actions require `dex-agents-cli.operate`. Upload results go to `~/.dex/agents/<harness>.log`. [Reference](/reference/agents/cli#dex-web)
- Go core: no session graph yet.

### Ending or continuing a built-in loop turn

- **AgentDefinition.decide_turn_continuation** (`decideTurnContinuation`) takes **TurnContinuationContext** and returns `"end"`, `"default"`, or **ContinueTurn(message)**. A continuation adds user text and calls the model in the same turn. [Reference](/reference/agents/changelog#v0-5-0)
- After eight consecutive continuations, the loop stores the answer and sets `last_turn_error` to **TURN_CONTINUATION_LIMIT_ERROR**. Tools reset the count. A thrown or invalid hook result fails the `RunModel` attempt for retry. [Reference](/reference/agents/changelog#v0-5-0)
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
- **dex-agents-claude-sdk** 0.5.0 uses Claude Code's own tools in both languages. Files stay in the workspace; writes and shell commands need approval except allowed shell commands, and approved calls are checked again. See [cli/python/claude-sdk](cli/python/claude-sdk/README.md).
- **dex-agents-pi** 0.5.0 runs Pi tools in the workspace. `pi.bash_timeout_seconds` defaults to 120 seconds, max 600. `pi.load_project_context` loads per-directory `AGENTS.md` by default; above the workspace, files and links must belong to you or root. [Pi](cli/typescript/pi/README.md)
- **WorkspaceFiles** exposes **readFileWithVersion**, **replaceFileIfUnchanged**, **statExisting**, and **readDirectoryEntries** to TypeScript harnesses. A refusal throws **WorkspaceFileRefusedError** before modifying files. See [core](cli/typescript/core/README.md#the-harness-interface).
- TypeScript CLI core: `read_file`, `edit_file`, and `search_files` open
  files without blocking, so a FIFO in the workspace fails at once as not a
  regular file, or is skipped by a search, instead of hanging the call, as in
  the Python CLI core.
- Python CLI 0.5.0 adds **dex-agents-openai-sdk**, **dex-agents-pydantic-ai**, **dex-agents-deepagents**, **dex-agents-google-adk**, and **dex-agents-builtin**. Provider keys come only from standard env vars. Rename `langgraph` config to `deepagents`. See [cli/python](cli/python).
- TypeScript CLI 0.5.0 adds OpenAI, Vercel AI, Mastra, and built-in agents with shared tools and per-turn settings. [OpenAI](cli/typescript/openai-sdk/README.md), [Vercel](cli/typescript/vercel-ai/README.md), [Mastra](cli/typescript/mastra/README.md), [Built-in](cli/typescript/builtin/README.md).
- **AgentHarness.isToolSetFixedAtBuild** lists connector tools before building the Flow. If MCP is unavailable at startup, the CLI prompts the user to restart to recover those tools. See [core](cli/typescript/core/README.md#connectors).
- **claude_code_tool_permission** (`claudeCodeToolPermission`) returns **ToolPolicy**, **ClaudeCodeToolDenial**, or defaults per call, including allowed `Bash` commands. See [Per-call decisions](python/claude-agent-sdk/README.md#per-call-decisions-for-claude-code-tools).
- `@superdurable/dex-agents-pi` 0.5.0 runs Pi sessions and peers on `@earendil-works/pi-ai@^1.5.1`. **piAgentFunctionTool(tool, { effect, requiresApproval })** requires an effect or a declared safety setting. See [typescript/pi](typescript/pi/README.md).
- **AgentSessionStartInput** adds optional `start_environment` (`startEnvironment`, `StartEnvironment`) through `AgentSessionClient.start_session` and `startAgentSession`. CLIs store host, user ID and name, config identity, and workspace in Flow input. Workers before 0.5.0 ignore it.
- **ToolCallContext.effect** exposes the effective built-in tool policy. Let write errors propagate to record `outcome_unknown`. Hand-built contexts default to `UNCLASSIFIED` in Python and may omit it in TypeScript; treat both cases as writes.
- **prepare_arguments** (`prepareArguments`) converts decoded tool input before validation. Conversion must be synchronous and deterministic. Errors fail the call as invalid arguments; stored calls and approvals keep the original input.
- **ToolExecutionResult** and **AgentToolCall** add `runner_tool_state` (`runnerToolState`, `RunnerToolState`) to keep a successful call's opaque framework state apart from its output. It stays out of model inputs, browser snapshots, and trace attributes.
- LangGraph runs **create_deep_agent** and stores tool state in **runner_tool_state**. Options include **excluded_tool_names**, **select_turn_chat_model**, and **should_defer_tool_calls_before_middleware**. See [Deep Agents](python/langgraph/README.md#a-deep-agents-agent).
- The **dex-agents-deepagents** command uses these options instead of its own
  state schema, subagent middleware replacement, and model middleware. Its
  behavior is unchanged.
- Pi `prepareArguments` converts inputs before validation. Errors follow **ToolCallContext.effect**: `read` and `idempotent_write` fail, and `non_idempotent_write`, `unclassified`, or unknown effects record `outcome_unknown`. See [typescript/pi](typescript/pi/README.md).
- **createPiAiModelClient** sends user and tool images from **ModelRequest.imageBytes**, fails if bytes are missing, and uses placeholders with unsupported models. **piAiEstimatedImageInputTokens** counts 1600 per image. Pi tool images need a media store. [Pi](typescript/pi/README.md)

### Images

- Python and TypeScript user messages and tool results can carry images. The session stores an **AgentImageReference**; **MediaStore** stores the bytes by SHA-256 so repeat uploads reuse the object. [Reference](/reference/agents/changelog#v0-5-0)
- **AgentUserMessage.images**, **AgentMessage.images**, **AgentToolCall.images**, **ToolExecutionResult.images**, and **FrameworkMessage.images** default to empty. Upgrade all Workers of the Flow type to 0.5.0 before sending images. [Reference](/reference/agents/changelog#v0-5-0)
- **S3MediaStore** (`dex_agents.s3`, extra `dex-agents[s3]`;
  `@superdurable/dex-agents/s3`, optional peer `@aws-sdk/client-s3`) keeps
  images in AWS S3 or an S3-compatible bucket, with credentials from the
  standard AWS chain. **InMemoryMediaStore** is for tests.
- **LocalDirectoryMediaStore** stores images by hash on one POSIX host, using modes `0700` and `0600`. It refuses symlinks, special files, and extra hard links, and verifies reads. In TypeScript a same-user process with root-directory write access can race those checks.
- PNG, JPEG, GIF, and WebP are checked from bytes. Limits are **MAX_IMAGE_BYTES** (3,750,000 raw or 5,000,000 base64), **MAX_IMAGES_PER_MESSAGE** (20 per message or result), and **MAX_IMAGE_URI_LENGTH** (1024 UTF-8 bytes). **ImageValidationError** names the violation.
- **verify_image_bytes** (`verifyImageBytes`) checks hash, size, and type. **S3MediaStore** uses conditional puts, rejects size mismatches, and reads at most one byte past `size_bytes`. It defaults to the AWS credential chain and accepts explicit credentials for local buckets.
- The session checks the images of every `FrameworkMessage` a runner returns
  before it stores any. Keep each object for as long as sessions can replay
  it; a missing object fails the turn with **MediaStoreError**.
- `send_user_message` returns `false` for invalid images, or for images when
  the runner does not implement **ImageUserMessageBuilder**
  (`buildUserMessageWithImages`). The built-in loop implements it when its
  session Flow has a media store; see below.
- Framework adapters accept user and tool images with optional **media_store** (`mediaStore`). They store references and fetch bytes immediately before model calls. Adapters without a store refuse images. [Reference](/reference/agents/changelog#v0-5-0)
- **McpToolSource** uploads successful `image` parts via **media_store** (`mediaStore`) and returns `ToolExecutionResult.images` with text placeholders. It rejects more than 20 images, noncanonical base64, or unsupported image bytes. Without a store it keeps the JSON text.
- Failed MCP results (`isError`, `rejected`, or `unknown`) now show `[image <n> omitted]` for each image and exclude raw bytes from the recorded error, even without a media store.
- Runners read bytes with **load_images** (`loadImages`) and may choose them
  with **collect_turn_images** (`collectTurnImages`).
- Go core: no image input. Its values carry `images`, so a value written by
  another core round-trips, and `send_user_message` returns `false` for a
  message with images.
- Shared cases: `agents/conformance/images.json`.
- CLI **media** config stores **/image <path>** attachments and connector tool images. `media.store` defaults to `auto` at `~/.dex/agents/media` or accepts `s3://<bucket>[/<prefix>]` with `media.endpoint`. Use environment or AWS credentials, never the YAML file. [Images](cli/README.md#images).
- **dex-agents-pi** sends **/image** attachments and up to 20 images per tool result to the model via the media store. Refused or excess images become `[image omitted: <mime type>]` without failing the call. [Images](cli/typescript/pi/README.md#images).
- Built-in function tools return images with **FunctionToolResult** or `functionToolResult({ output, images })`. Upload bytes via **ToolCallContext.media_store** (`mediaStore`). Returning images without a store fails the call once without retry.
- **builtin_agent_session_flow** and **BuiltinLoopAgentRunner** accept **media_store** (`mediaStore`). Stored items keep references and load **ModelRequest.image_bytes** (`imageBytes`) before model calls. Read failures retry through `RunModel`; summaries remain text-only.
- **LiteLLMModelClient** sends images as base64 `data:` URLs and estimates **ESTIMATED_IMAGE_INPUT_TOKENS** per image. Gemini and Vertex AI accept user images but omit tool result images, which get **TOOL_RESULT_IMAGES_OMITTED_TEXT**.
- **createAiSdkModelClient** sends images as AI SDK `file` parts in user
  messages and in tool result `content` output. `estimateInputTokens` counts
  **estimatedImageInputTokens** per image.

### Changed

- The built-in loop moves to `dex_agents.builtin_loop`, `@superdurable/dex-agents/builtin-loop`, and Go `builtinloop`. Switch to `BuiltinLoopAgentRunner` and the renamed factories. Old names work for one release; sessions still resume. [Migration](/reference/agents/changelog#v0-5-0)
- Built-in loop decoding errors now name the built-in loop. Python loggers move to `dex_agents.builtin_loop.*`, and Go nil model or definition panics start with `builtinloop:`.
- CLI Flow type suffixes change from 12 hex digits to six lowercase base36 characters. The new CLI cannot resume or list sessions under the old suffix. Python adds `namespace_session_flow_type`, matching TypeScript `namespaceSessionFlowType`.
- Connector default names now keep acronyms together and treat only ASCII capitals as word breaks. Old recorded calls need their original name passed explicitly to `connector_tool` or `connectorTool`.
- Mutation `defect` now records `outcome_unknown` without retry. Query `defect` follows `agent.branches`. A required `defect` or `uncertain` no longer records `succeeded`. [Reference](/reference/agents/changelog#v0-5-0)
- Fenced Mutations record `outcome_unknown` for non-success unless checkpoints prove no send. A single-attempt Retry needs `notSent` or `canResend` plus no possible send to record `failed`. [Reference](/reference/agents/changelog#v0-5-0)

### Fixed

- Claude Agent SDK adapters discard Claude Code `last-prompt` entries, including those stored in 0.4.0, so a resumed segment keeps the prior response and resumes the correct deferred call.
- Python and TypeScript Claude Agent SDK adapters: when the Claude Code CLI
  returns an error result, the turn's error now reads "the Claude Code CLI
  returned an error result (subtype ...)". It used to read "reported success"
  for an error result whose subtype is `success`, such as a failed login.

## 0.4.0 (2026-11-10)

### Connector tools

- **ConnectorToolSource** exposes only operations explicitly selected with `connector_tool` or `connectorTool` and marked `agent.exposed: true`. Nothing is exposed by default. [Reference](/reference/agents/connector-tools#connector-tools-offered)
- Connector Queries are `READ`, safe Mutations are `IDEMPOTENT_WRITE`, and other Mutations are `NON_IDEMPOTENT_WRITE`. Steps use operation timeouts and at most three attempts; `tool_policies` changes only approval. [Reference](/reference/agents/connector-tools#step-options)
- Approval: `agent.approval: recommended` asks by default; other Mutations run
  without approval. `requires_approval` (`requiresApproval`) overrides it with a
  bool or a per-call predicate over the arguments.
- Connector tools validate `inputSchema` before decoding and use `agent.branches` for outcomes. Unlisted Mutation branches record `outcome_unknown`; unlisted Query branches record `failed`. Results default to 64 KiB. [Reference](/agent-frameworks/connectors-as-tools#connector-tools-outcomes)
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
- Source calls run in their own tool Step via `execute_deferred_tool_call` (`executeDeferredToolCall`), with the same Call ID and fence. The source sets effect, approval, and write order; `tool_call_step_options` (`toolCallStepOptions`) sets timeout and retry.
- Duplicate `source_name` (`sourceName`) or tool names fail definition. Collisions found while listing fail the `RunModel` attempt; Mastra also checks its request-time agent tools there.
- Claude Agent SDK, TypeScript OpenAI Agents SDK, and Python Step-mode adapters resume a source tool missing from the current list with its recorded result. The tool cannot start a new call in that turn, and the result is served once under its original name.
- The framework adapters poll no tool tasks: a source call that starts a task
  records `failed` for a `read` call and `outcome_unknown` otherwise. In the
  TypeScript adapters, a source call that asks a question records `failed`.
- `ClaudeAgentRunner` and `claude_agent_session_flow` (`createClaudeAgentSessionFlow`) accept tool sources. Calls defer in model order and later writes in a response are refused. Adding a source tool to `allowed_tools` (`allowedTools`) fails the attempt.
- `createVercelAiAgentSessionFlow`, `createOpenAIAgentsSessionFlow`, `createMastraAgentSessionFlow`, and `MastraAgentRunner` accept `toolSources`. OpenAI exposes non-strict tools to every agent and restores source names; Mastra exposes client tools without `execute`.
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
- A Claude tool call resumes once per segment, under the same name and kind, only while it has no recorded result. Calls already resumed in an earlier segment and mismatched calls are refused without running.
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

- `render_session_flow_graph` (`renderSessionFlowGraph`) and `dex-agents-session-graph` render the session's Flow Definition Graph 2.0 with its Flow type and Action permission. Load it in Dex Web via `--flow-rendering-dir`. [Reference](/reference/agents/dex-web#render-the-graph)
- The graph is derived from the Python core's registered Flow and source and
  ships in both packages. The conformance suite adds `session_graph.json`, and
  each core's tests fail when the graph and the session Flow disagree.
- Dex Web needs a release that accepts Actions without a condition and calls no
  RPC for a view without fields.
- Go core: no graph. It registers only part of the session Flow.
- See [Show sessions in Dex Web](../docs/reference/agents/dex-web.mdx).

### Compaction in framework adapters

- OpenAI Agents SDK, Pydantic AI, LangGraph, ADK, Vercel AI SDK, and Mastra now support `AgentSessionOptions.compaction`. Existing 0.2.0 sessions compact once their Workers enable a policy. [Reference](/reference/agents/session-limits#compaction)
- Adapters compact only between turns, before a turn's first model call. A model
  call that resumes a turn after its tool calls never compacts, because the
  OpenAI Agents SDK and LangGraph resume from a run state that holds the input.
- Adapter Flow factories accept `compaction`, `retained_message_limit`, and `summary_model` (`summaryModel`). LangGraph requires the summary model. TypeScript OpenAI Agents and Mastra now pass through compaction and storage options. [Reference](/reference/agents/session-limits#compaction)
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
