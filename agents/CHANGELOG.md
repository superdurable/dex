# Changelog

All twenty-six published Dex agents packages share this changelog and one version line. A
release is tagged `agents/v<version>`. See [README.md](README.md#versions-and-compatibility)
for the Dex SDK and framework versions each release supports.

## 0.6.15 (2026-11-18)

### Changed

- Python and TypeScript Connector tools classify outcomes by branch alone. **uncertain** and **unknown** record **outcome_unknown**, success records **succeeded**, and other branches, including **defect** and **rejected**, record **failed**. [Reference](/reference/agents/connector-tools#outcomes)
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
- Python packages and CLI commands require **dex-python-sdk** 1.45.8. TypeScript packages and CLI commands require and peer-depend on **@superdurable/dex** 1.45.8, which sends null for unset RPC input and fixes **Value has no concrete kind** when calling a Python Worker. (#2034)

### Fixed

- **dex-agents-claude-agent-sdk** and **@superdurable/dex-agents-claude-agent-sdk** resume deferred calls to renamed or removed tools with recorded results. In-flight single-attempt calls keep **outcome_unknown** without rerunning, and retries of removed **idempotent_write** tools record it too.
- **dex-agents-google-adk** now resumes **RunModel** with recorded results for paused calls to renamed or removed tools. In-flight single-attempt calls keep **outcome_unknown** without rerunning. The next model call uses the new tool list.
- Python and TypeScript record **outcome_unknown** when `ToolExecutionError` refuses a write retry, including **idempotent_write** after a deploy renames the tool or changes its schema. [Reference](/reference/agents/failures#retry-refusal)
- **dex-agents-openai-agents** and **@superdurable/dex-agents-openai-agents** rebuild a turn's history from recorded results when a renamed or removed tool prevents resuming **RunState**. In-flight single-attempt calls keep **outcome_unknown** without rerunning.

## 0.6.14 (2026-11-16)

### Added

- **StopFlow** with **GracefulComplete** ends Python, TypeScript, and Go sessions as **COMPLETED** after running and queued turns finish. Requires server-zd 0.1.34+ and all Workers of that Flow type at 0.6.14+. [Reference](/reference/agents/session-limits#end-a-session)

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

- **fork_agent_session**, **forkAgentSession**, and **AgentSessionClient.fork_session** copy a session between turns, optionally at **after_message** (**afterMessage**). **AgentSessionForkingFlow** and **createAgentSessionForkingFlow** need no agent. [Reference](/reference/agents/cli#fork)
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
- Connector Mutations resend after unknown outcomes if the manifest and operation agree on `agent.repeatOnUnknown: limited` and `agent.maxSends` (2–10). Attempts default to three if unlimited and always meet the pinned cap. [Reference](/reference/agents/connector-tools#bounded-resends)
- `ToolCallStepOptions.bounded_resend_execute_retry`
  (`boundedResendExecuteRetry`) gives a `non_idempotent_write` call whose tool
  enforces its own send cap more than one attempt in `CallSingleAttemptTool`;
  when the attempts run out, the call records `outcome_unknown`.
- `ToolPolicy.max_sends` (`maxSends`) overrides the send cap with 1–10, with `1` limiting the call to one attempt. Resends require `@superdurable/connectors-sdk` 0.11.0+ or `dex-connectors-sdk` 0.10.0+. [Reference](/reference/agents/connector-tools#max-sends)
- Upgrade all Workers of a session Flow type to 0.6.12 before enabling `limited` resends and keep them upgraded while calls are open. Older Workers can exceed the pinned TypeScript send cap. [Reference](/reference/agents/connector-tools#bounded-write-upgrade)

### Changed

- Failed, steered, or stopped turn notes cap each result at 2,000 UTF-8 bytes, with an omission marker, and stored records keep the full result. The limit is `TOOL_CALL_NOTE_MAX_OUTPUT_BYTES`, `toolCallNoteMaxOutputBytes`, or `ToolCallNoteMaxOutputBytes`.

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
- `@superdurable/dex-agents-vercel-ai` saves partial `RunModel` messages and fills missing tool results with `toolCallOutputOmittedResult` for unread outputs from successful `toModelOutput` tools. State version 2 fails on 0.6.11, so upgrade all Workers of each Flow type together.

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
- `ClaudeAgentRunner` and `dex-agents-claude-sdk` use `pollToolTask` and `cancelToolTask` for `ToolTaskStarted` and record `outcome_unknown` on cancellation. `ToolInputRequested` waits for the user's answer, and MCP tasks already polling stay in their tool Step with the same idempotency key.

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

- `mcp_servers` enables remote MCP. Header expansion blocks `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE`. Best-effort redaction covers secrets of 8+ characters and Workers refuse calls missing required approval or single-attempt policy. [Reference](/reference/agents/cli#mcp-servers)
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
- `resolve_dex_connection_settings` and `resolveDexConnectionSettings` resolve remote settings without secrets. Validation adds `insecure_token_url`, `auth_needs_server_url`, and `tls_needs_https_server`. [Reference](/reference/agents/cli#remote-server)
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
- `resolve_turn_config`, `resolveTurn`, and `TurnConfigResolver` accept supported reasoning values and return the chosen effort. The UI reserves `default`, and suggested model IDs exclude spaces and control characters. [Reference](/reference/agents/cli#model-and-effort)
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

- `dex-agents-deepagents` runs writes and shell commands without approval by default. Add `write_file`, `edit_file`, `delete`, `execute`, or `task` to `approval.always`, since `local_tools.shell.allow` is ignored. Use absolute `file_path` for `read_file`, `write_file`, and `edit_file`, and replace `list_directory`, `search_files`, or `run_shell_command` with the native tools. [Reference](/reference/agents/cli#harness-tools)
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
- At the next indexed turn, older sessions gain activity time and turn count, and they gain a title only if it is their first turn. Turning off indexing or using earlier or Go Workers preserves stored values. [Reference](/reference/agents/approvals#list-sessions)
- `sessions.summary` defaults to `auto`, indexing with `dex.server: auto`, and `on` and `off` apply everywhere. Harnesses must honor it, and `sessions` lists up to 250 by latest activity with turn time, count, and title. [Reference](/reference/agents/cli#configuration)
- The Python CLI commands (`dex-agents-cli-core`) accept `--plain`, which
  keeps the chat in plain text lines instead of the `dexcli` terminal UI.
- The TypeScript CLI commands (`@superdurable/dex-agents-cli-core`) take
  `--plain`, which keeps the chat in plain text instead of the `dexcli`
  terminal UI. Pi always keeps the plain text chat, through the new
  `AgentHarness.terminalInterface` declaration (`"plain"`).

### Changed

- `dex-agents-claude-sdk` uses `strictMcpConfig` (`strict_mcp_config`) and `disableClaudeAiConnectors` to block MCP servers and connectors from Claude Code configuration or login. The CLI keeps built-in, browser, and Dex Connector tools, and application adapters keep their defaults.
- **dex-agents-builtin** enables plan mode with `write_todos`. In the terminal UI, `/plan` drafts with that tool and `/run` approves and runs with every tool. [Reference](/reference/agents/cli#terminal-ui)
- **dex-agents-builtin**: `write_todos` is now a reserved tool name, so a
  connector or local tool with that name fails the turn. Rename the
  conflicting tool, or use another harness.
- The Python terminal UI displays Worker host startup notices and `dexcli dev` details, such as unavailable connectors and the local server's log path. [Reference](/reference/agents/cli#terminal-ui)
- `dexcli dev` started for `dex.server: auto` now stops with the CLI on exit, failure, SIGTERM, or SIGHUP. The CLI removes `~/.dex/agents/dexcli-dev.json` and signals the running server's process group, escalating after 20 seconds or another signal. [Reference](/reference/agents/cli#dex-server)
- CLI exit leaves reused `dexcli dev` running. Start your own or set `dex.server` to share it. If the reused server stops, the CLI exits 1. A crash or SIGKILL can leave a started server running for later reuse. [Reference](/reference/agents/cli#dex-server)
- On Windows, TypeScript stops its `dexcli dev` tree with `taskkill /T` and then `taskkill /T /F`, while Python sends CTRL_BREAK_EVENT first. Python removes its `os.kill(pid, 0)` check because it could stop the server. [Reference](/reference/agents/cli#dex-server)
- TypeScript CLI core: during the chat, the Dex SDK Worker's task stream
  reconnect messages go only to `~/.dex/agents/<harness>.log`, so a lost Dex
  server no longer prints stack traces into the chat. Other library warnings
  show their first line on the terminal and their full text in the log file.
- TypeScript CLI core: closing the terminal no longer ends a command on Linux
  with an uncaught `setRawMode EIO` error before it handles SIGHUP. The
  command now stops the `dexcli dev` it started and exits with 129.
- `dex-agents-deepagents` uses native tools, `write_todos`, and `task`. Connector writes need approval, file tools block outside paths, `execute` runs with your permissions, and `task` runs in one Step without its own approvals. [Reference](/reference/agents/cli#harness-tools)
- LangGraph's `should_defer_tool_calls_before_middleware` defaults to `True`, running wrapped root tools' middleware in each Step attempt. Set `should_defer_tool_calls_before_middleware=False` for prior ordering. In-flight 0.5.x turns that opted in may run one earlier wrapper in `RunModel`.
- Python LangGraph adapter: a tool Step that runs a tool node's wrappers no
  longer applies the node's `handle_tool_errors`, so an exception in the tool
  body, such as a `ValidationError`, fails the attempt and a write records
  `outcome_unknown`, as it does without wrappers.
- Python LangGraph adapter: an `InjectedStore` parameter of a tool receives
  the compiled graph's store in its tool Step.
- TypeScript agents packages now include Node 26 (`^26.0.0`) in **engines.node**, removing `EBADENGINE` warnings. The Pi packages still require `^22.19.0` as their lower bound.
- `dex-agents-pi` runs `read`, `grep`, `find`, `ls`, `write`, `edit`, and `bash` in separate Steps. Reads can retry, writes and shell run once, `pi.tool_progress` streams shell output, and Connector writes need approval. [Reference](/reference/agents/cli#harness-tools)
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

- All agents packages pin Dex SDK 1.45.0 (`dex-python-sdk==1.45.0`, `@superdurable/dex` `1.45.0`, Go `sdk-go` `v1.45.0`), up from 1.38.0. The release supports **Join** and Worker-side Flow definition uploads, and npm adapters pin the same SDK peer.
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
- `McpToolSource.execute_deferred_tool_call` accepts `request_meta` for `tools/call`, with the idempotency key winning on a clash. `McpToolSource.will_resume_tool_task_without_sending` reports whether this call can only resume an already-started task.

- **McpToolSource.executeDeferredToolCall** accepts `executeDeferredToolCall(call, { requestMeta })` to add `_meta` to `tools/call`, such as expected tool identity. The idempotency key wins on a clash.
- **McpToolSource.willResumeToolTaskWithoutSending(call)** is `true` only when `executeDeferredToolCall` returns an already-started, session-polled task. A task polled inside the tool Step can send `tools/call` again if the server lost it.

### Showing sessions in Dex Web

- **session_flow_definition_document** (`sessionFlowDefinitionDocument`) enables Worker uploads of session graphs on server-zd 0.1.26+. Use **render_session_flow_graph** with older servers. [Reference](/reference/agents/dex-web#upload-the-graph)
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
- **dex-agents-claude-sdk** 0.5.0 uses Claude Code tools in both languages. Files stay in the workspace, writes and shell calls require approval except allowlisted shell commands, and the Step rechecks approved calls. [Claude CLI](cli/python/claude-sdk/README.md)
- **dex-agents-pi** 0.5.0 uses Pi tools in the workspace. `pi.bash_timeout_seconds` defaults to 120 seconds (max 600). `pi.load_project_context` loads `AGENTS.md` by default and requires files and links above the workspace to belong to you or root. [Pi](cli/typescript/pi/README.md)
- **WorkspaceFiles** exposes **readFileWithVersion**, **replaceFileIfUnchanged**, **statExisting**, and **readDirectoryEntries** to TypeScript harnesses. A refusal throws **WorkspaceFileRefusedError** before modifying files. See [core](cli/typescript/core/README.md#the-harness-interface).
- TypeScript CLI core: `read_file`, `edit_file`, and `search_files` open
  files without blocking, so a FIFO in the workspace fails at once as not a
  regular file, or is skipped by a search, instead of hanging the call, as in
  the Python CLI core.
- Python CLI 0.5.0 adds **dex-agents-openai-sdk**, **dex-agents-pydantic-ai**, **dex-agents-deepagents**, **dex-agents-google-adk**, and **dex-agents-builtin**. Provider keys come only from standard env vars. Rename `langgraph` config to `deepagents`. See [cli/python](cli/python).
- TypeScript CLI 0.5.0 adds **dex-agents-openai-sdk**, **dex-agents-vercel-ai**, **dex-agents-mastra**, and **dex-agents-builtin**. [SDK](cli/typescript/openai-sdk/README.md) [AI](cli/typescript/vercel-ai/README.md) [Mastra](cli/typescript/mastra/README.md) [Loop](cli/typescript/builtin/README.md).
- **AgentHarness.isToolSetFixedAtBuild** lists connector tools before building the Flow. If MCP is unavailable at startup, the CLI prompts the user to restart to recover those tools. See [core](cli/typescript/core/README.md#connectors).
- **claude_code_tool_permission** (`claudeCodeToolPermission`) returns **ToolPolicy**, **ClaudeCodeToolDenial**, or defaults per call, including allowed `Bash` commands. See [Per-call decisions](python/claude-agent-sdk/README.md#per-call-decisions-for-claude-code-tools).
- `@superdurable/dex-agents-pi` 0.5.0 runs Pi sessions and peers on `@earendil-works/pi-ai@^1.5.1`. **piAgentFunctionTool(tool, { effect, requiresApproval })** requires an effect or a declared safety setting. See [typescript/pi](typescript/pi/README.md).
- **AgentSessionStartInput** adds optional `start_environment` (`startEnvironment`, `StartEnvironment`) through `AgentSessionClient.start_session` and `startAgentSession`. CLIs store host, user ID and name, config identity, and workspace in Flow input. Workers before 0.5.0 ignore it.
- **ToolCallContext.effect** exposes the effective built-in tool policy, so write errors can propagate to record `outcome_unknown`. Treat hand-built contexts as writes when they default to `UNCLASSIFIED` in Python or omit the field in TypeScript.
- **prepare_arguments** (`prepareArguments`) synchronously and deterministically converts input before validation. Conversion errors fail the call as invalid arguments, and stored calls and approvals keep the original input.
- **ToolExecutionResult** and **AgentToolCall** add `runner_tool_state` (`runnerToolState`, `RunnerToolState`) to keep a successful call's opaque framework state apart from its output. It stays out of model inputs, browser snapshots, and trace attributes.
- LangGraph runs **create_deep_agent** and stores tool state in **runner_tool_state**. Options include **excluded_tool_names**, **select_turn_chat_model**, and **should_defer_tool_calls_before_middleware**. See [Deep Agents](python/langgraph/README.md#a-deep-agents-agent).
- The **dex-agents-deepagents** command uses these options instead of its own
  state schema, subagent middleware replacement, and model middleware. Its
  behavior is unchanged.
- Pi `prepareArguments` converts inputs before validation. Errors follow **ToolCallContext.effect**: `read` and `idempotent_write` fail, and `non_idempotent_write`, `unclassified`, or unknown effects record `outcome_unknown`. See [typescript/pi](typescript/pi/README.md).
- **createPiAiModelClient** sends user and tool images from **ModelRequest.imageBytes**, fails if bytes are missing, and uses placeholders with unsupported models. **piAiEstimatedImageInputTokens** counts 1600 per image. Pi tool images need a media store. [Pi](typescript/pi/README.md)

### Images

- Python and TypeScript user messages and tool results can carry images. The session stores an **AgentImageReference**, and **MediaStore** stores bytes by SHA-256 so repeat uploads reuse the object. [Reference](/reference/agents/changelog#v0-5-0)
- **AgentUserMessage.images**, **AgentMessage.images**, **AgentToolCall.images**, **ToolExecutionResult.images**, and **FrameworkMessage.images** default to empty. Upgrade all Workers of the Flow type to 0.5.0 before sending images. [Reference](/reference/agents/changelog#v0-5-0)
- **S3MediaStore** (`dex_agents.s3`, extra `dex-agents[s3]`;
  `@superdurable/dex-agents/s3`, optional peer `@aws-sdk/client-s3`) keeps
  images in AWS S3 or an S3-compatible bucket, with credentials from the
  standard AWS chain. **InMemoryMediaStore** is for tests.
- **LocalDirectoryMediaStore** stores images with modes `0700` and `0600` on one POSIX host, verifies reads, and refuses symlinks, special files, and extra hard links. A same-user process with write access to the store's root can race the TypeScript checks.
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
- **builtin_agent_session_flow** and **BuiltinLoopAgentRunner** accept **media_store** (`mediaStore`) to load **ModelRequest.image_bytes** (`imageBytes`) from stored references before model calls. Reads retry through `RunModel`, and summaries stay text-only.
- **LiteLLMModelClient** sends images as base64 `data:` URLs and estimates **ESTIMATED_IMAGE_INPUT_TOKENS** per image. Gemini and Vertex AI accept user images but omit tool result images, which get **TOOL_RESULT_IMAGES_OMITTED_TEXT**.
- **createAiSdkModelClient** sends images as AI SDK `file` parts in user
  messages and in tool result `content` output. `estimateInputTokens` counts
  **estimatedImageInputTokens** per image.

### Changed

- The built-in loop moves to `dex_agents.builtin_loop`, `@superdurable/dex-agents/builtin-loop`, and Go `builtinloop`. Use `BuiltinLoopAgentRunner` and the renamed factories. Old names work for one release and existing sessions resume. [Migration](/reference/agents/changelog#v0-5-0)
- Built-in loop decoding errors now name the built-in loop. Python loggers move to `dex_agents.builtin_loop.*`, and Go nil model or definition panics start with `builtinloop:`.
- CLI Flow type suffixes change from 12 hex digits to six lowercase base36 characters. The new CLI cannot resume or list sessions under the old suffix. Python adds `namespace_session_flow_type`, matching TypeScript `namespaceSessionFlowType`.
- Connector default names now keep acronyms together and treat only ASCII capitals as word breaks. Set the tool's name explicitly to resume old recorded calls. [Reference](/reference/agents/connector-tools#connector-tools-offered)
- Mutation `defect` now records `outcome_unknown` without retry. Query `defect` follows `agent.branches`. A required `defect` or `uncertain` no longer records `succeeded`. [Reference](/reference/agents/changelog#v0-5-0)
- Fenced Mutation non-success is `outcome_unknown` unless no send is proven. A single-attempt Retry needs `notSent` or `canResend` to record `failed`. `classify_connector_branch` (`classifyConnectorBranch`) adds `may_have_dispatched` (`mayHaveDispatched`). [Ref](/reference/agents/changelog#v0-5-0)

### Fixed

- Claude Agent SDK adapters discard Claude Code `last-prompt` entries, including those stored in 0.4.0, so a resumed segment keeps the prior response and resumes the correct deferred call.
- Python and TypeScript Claude Agent SDK adapters: when the Claude Code CLI
  returns an error result, the turn's error now reads "the Claude Code CLI
  returned an error result (subtype ...)". It used to read "reported success"
  for an error result whose subtype is `success`, such as a failed login.

## 0.4.0 (2026-11-10)

### Connector tools

- **ConnectorToolSource** exposes only operations explicitly selected with `connector_tool` or `connectorTool` and marked `agent.exposed: true`. Nothing is exposed by default. [Reference](/reference/agents/connector-tools#connector-tools-offered)
- Connector Queries are `READ`, safe Mutations are `IDEMPOTENT_WRITE`, and other Mutations are `NON_IDEMPOTENT_WRITE`. Steps use operation timeouts with at most three attempts, and `tool_policies` only changes approval. [Reference](/reference/agents/connector-tools#step-options)
- Approval: `agent.approval: recommended` asks by default; other Mutations run
  without approval. `requires_approval` (`requiresApproval`) overrides it with a
  bool or a per-call predicate over the arguments.
- Connector tools validate `inputSchema` before decoding and use `agent.branches` for outcomes, including `outcome_unknown` for unlisted Mutations and `failed` for unlisted Queries. Results default to a 64 KiB cap. [Reference](/agent-frameworks/connectors-as-tools#connector-tools-outcomes)
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
- Sources run calls in their own Step via `execute_deferred_tool_call` (`executeDeferredToolCall`) with the same Call ID and fence. The source sets effect, approval, and write order, plus timeout and retry via `tool_call_step_options` (`toolCallStepOptions`).
- Duplicate `source_name` (`sourceName`) or tool names fail definition. Collisions discovered while listing fail the `RunModel` attempt, including for Mastra's request-time tools.
- Claude Agent SDK, TypeScript OpenAI Agents SDK, and Python Step-mode adapters resume a source tool missing from the current list with its recorded result. The tool cannot start a new call in that turn, and the result is served once under its original name.
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
- Adapters can use `estimate_text_tokens`, `estimate_stored_input_tokens`, `select_turn_boundary_cutoff`, `describe_stored_message`, and `build_context_summary_prompt` (camelCase in TypeScript). Summary text constants move to the core root and remain exported by Stepwise.
- `CompactionPolicy.reserved_input_tokens` (`reservedInputTokens`), default 0,
  adds tokens the runner's estimate cannot see.
- `dex_agents.testing` and `@superdurable/dex-agents/testing` add
  `ContextWindowModelScript` and `ContextWindowSessionScenario`.
- No stored value changes. The conformance suite adds the summary prompt texts.
- Go core: unchanged.
- See [Compaction and the storage bound](../docs/reference/agents/session-limits.mdx#compaction).

### Spend limits

- `ModelSpendLimit` lets apps cap model spend through `AgentSessionOptions.spend_limit` (`spendLimit`). The session runs `check_model_call` before model and compaction attempts, then calls `report_model_usage` on return. [Reference](/reference/agents/spend-limits)
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

- `AgentSessionOptions.run_model_limit` (`runModelLimit`) sets `RunModel` timeout and attempts. Retry budget is at least two method timeouts or timeout times attempts minus one. Defaults stay 15 minutes per attempt, three in 30 minutes. [Reference](/reference/agents/workers#long-model-calls)
- Python Claude Agent SDK adapter: `claude_agent_session_flow` takes
  `run_model_limit`. The TypeScript adapter takes it through its session options.
  A turn segment that needs more than 15 minutes can now finish. A failed segment
  reruns whole.
- No stored value changes. A running session gets the limit on its next `RunModel`
  after the Worker restarts.
- Go core: unchanged.

### Sessions are searchable by the Step they wait in

- `AgentSessionClient.start_session` defaults `FlowConfig.active_step_search_mode` to `AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE` (`WITH_WAIT_FOR`), making waiting sessions searchable. `options.config_override` wins. [Reference](/reference/agents/approvals#find-sessions)
- `startAgentSession(client, flow, sessionId, input, options)` enables search for waiting sessions via `agentSessionActiveStepSearchMode`. It uses `ActiveStepSearchMode.DEFAULT` until the SDK supports `ActiveStepSearchMode.WITH_WAIT_FOR`. [Reference](/reference/agents/approvals#find-sessions)
- `Client.startFlow` and `AsyncClient.start_flow` are unchanged, so a session they
  start stays out of this search. So do sessions started on 0.2.0, until
  `UpdateFlowConfig` turns it on. No stored value changes.
- Go core: unchanged.
- See [Find sessions that need a person](../docs/reference/agents/approvals.mdx#find-sessions).

### A session at the state limit fails, with the reason

- Exceeding `limits.maxFlowStateBytes` closes an agent Flow as `FAILED` and records `failed` and `last_turn_error` when it can. Exceeding `values.maxValueBytes` still fails only the turn. [Reference](/reference/agents/session-limits#at-the-limit)
- `AgentSessionClient.get_agent_snapshot` and `read_agent_snapshot` (`readAgentSnapshot`) read the Flow close to report `failed` and its reason even when stored state stays stale. Failure before the first state returns an empty snapshot. [Reference](/reference/agents/session-limits#at-the-limit)
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

- All three cores record `failed` when tool Step recovery reports `attempt_progress` as `NOT_STARTED`. `TOOL_CALL_NOT_RUN_ERROR_PREFIX` tells the model the call never ran and is safe to repeat. [Reference](/reference/agents/failures#never-run-outcome-unknown)
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

- `McpToolSource` runs stdio and Streamable HTTP tools in Steps. Only `read` and `idempotent_write` resend after a missing response. Python requires `mcp>=2.3,<3`; TypeScript peers on `@modelcontextprotocol/sdk` `^1.32.2`. [Reference](/agent-frameworks/built-in-agent-loop#mcp-tools-and-approvals)
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

- `dex-agents` adds optional `otel` for `opentelemetry-api`. Set `AgentSessionOptions.tracing` with `AgentTracingOptions`, and pass `ModelCallMetadata` as `model_call_metadata` in turn outcomes. [Reference](/reference/agents/tracing#options)
- `@superdurable/dex-agents` adds optional peer `@opentelemetry/api@^1.3.0`. Set `AgentSessionOptions.tracing` with `AgentTracingOptions`, and return `modelCallMetadata` in either turn outcome. [Reference](/reference/agents/tracing#options)
- `AgentTraceContext` stores the activation's valid `traceparent`, including unsampled activations. `AgentToolCall` gains `trace_context` to link tool spans to their activation root.
- Adapters name `chat` spans from `ModelCallMetadata`, use provider-reported fields, and keep only shared fields across multiple calls. They map known provider IDs to `gen_ai.provider.name` and never infer a provider from model or environment. [Reference](/reference/agents/tracing#spans)
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

- `dex_agents.stepwise` adds `AgentDefinition`, `@function_tool`, `ModelClient`, `StepwiseAgentRunner`, and `stepwise_agent_session_flow`. After `max_model_calls_per_turn`, the final call offers no tools and any tool request gets a limit result. [Reference](/agent-frameworks/built-in-agent-loop)
- `dex_agents.litellm.LiteLLMModelClient` ships via `dex-agents[litellm]` (`litellm>=1.104,<1.106`). OpenAI uses Responses with `store=False` and encrypted reasoning, while others use streaming Chat Completions. Reasoning and signatures are preserved without retries inside the client.
- `dex-agents`: `ScriptedModelClient` in `dex_agents.testing`, and
  `AgentTestStack.start_worker(python_executable=...)` to run a Worker on
  another environment, such as an earlier release.
- `@superdurable/dex-agents/stepwise`: Stepwise mode:
  `createStepwiseAgentSessionFlow`, `StepwiseAgentRunner`, `functionTool`, and
  the `ModelClient` interface.
- `@superdurable/dex-agents/ai-sdk` adds `createAiSdkModelClient` for `LanguageModel`, with optional peer `ai@^7.0.159`. It makes one `streamText` call per `RunModel` attempt with `maxRetries` 0 and preserves provider metadata.
- `@superdurable/dex-agents/testing`: `ScriptedModelClient` for Stepwise tests
  without a model key.

### Stepwise tool sources, MCP resources and prompts, and the demo model

- `AgentDefinition.tool_sources` (`toolSources`) lists tools with a stable `source_name` (`sourceName`) and uses the source policy in each tool Step. Listings time out at 10 seconds by default, failed sources are omitted, and duplicate names fail before a model request.
- `McpToolSource` caches listings per Worker, refreshes in the background after `listing_ttl` (`listingTtlMs`, default 5 minutes), and backs off failures via `listing_failure_backoff` (`listingFailureBackoffMs`, default 30 seconds). One listing runs per server.
- `McpToolSource(..., resource_and_prompt_tools_enabled=True)` (`resourceAndPromptToolsEnabled: true`) adds `mcp_list_resources_and_prompts`, `mcp_read_resource`, and `mcp_get_prompt` for one source. It defaults to off and requires `@modelcontextprotocol/sdk` 1.32.2 in TypeScript.
- `ScriptedModelClient`, `ScriptedModelReply`, `ScriptedToolCall`, and `ScriptedModelResponder` are supported exports for running agents without a model key. `requests` keeps 100 by default (`max_recorded_requests` / `maxRecordedRequests`). Existing `testing` imports still work.

### Questions, durable waits, and incremental turn messages

Both cores, with identical Step types, RPC names, wire values, events, and
model-visible text. Everything is inert until an application enables it.

- `AgentSessionOptions.built_in_tools` (`builtInTools`) enables `ask_user` and `durable_wait`. Their `read` Steps need no approval and reject invalid arguments immediately. Optional `ask_user` arguments accept `null` as absent. [Reference](/agent-frameworks/built-in-agent-loop)
- `ask_user` and `ToolInputRequested` raise a `question` interrupt. `answer_question` returns the result to the turn waiting in `AwaitQuestionAnswer`. Multiple calls can ask in parallel, and `interrupt_deadline` applies. [Reference](/reference/agents/approvals#approval-deadlines)
- `durable_wait` waits on a whole-second Timer in the new `AwaitTimer` Step.
  `AgentSessionOptions.max_durable_wait` (`maxDurableWaitMs`) defaults to 365
  days and must be whole seconds.
- `ToolCallsRequested.new_messages` (`newMessages`) saves incremental turn items. Set it with `build_unfinished_turn_messages` (`buildUnfinishedTurnMessages`) for `RecordTurnFailure`. Without that method, a failure ends the turn in `RunModel` after one model call.
- Wire values add `AgentToolCall` statuses `awaiting_answer` and `waiting_for_timer`, question and answer fields on interrupts, `AnswerQuestionRequest`, and `timer_started`. [Reference](/agent-frameworks/built-in-agent-loop#questions-and-durable-waits)
- Both cores write RFC 3339 times with milliseconds and `Z`, such as
  `2026-11-01T12:00:00.123Z`. Every reader in both cores, 0.1.0 included,
  accepts the earlier `+00:00` form too.

### Plans and plan mode

Both cores, with identical Step types, persistence names, RPC names, wire
values, events, and model-visible text. Everything is inert until an
application enables it.

- `write_todos` replaces `AgentPlan` and emits `plan_updated` without a tool Step. An empty list clears the plan and duplicate calls in one response fail both. Approval emits the event again when the draft becomes `active`. [Reference](/agent-frameworks/built-in-agent-loop#durable-plans)
- `AgentSessionOptions.plan_mode_enabled` (`planModeEnabled`) enables `AgentUserMessage.plan_mode` (`planMode`). Runners declare `supports_plan_mode` (`supportsPlanMode`) and planning allows only `write_todos`. [Reference](/agent-frameworks/built-in-agent-loop#durable-plans)
- `execute_plan` approves a plan revision, queues it behind user messages, then starts an `executing` turn and emits `plan_execution_started`. A stale revision or duplicate pending approval returns false. [Reference](/agent-frameworks/built-in-agent-loop#durable-plans)
- `AgentDefinition.planning_tool_choice` (`planningToolChoice`) defaults to `auto` and permits text. `required` forces `write_todos` without a draft, and Python rejects it when `supports_forced_tool_choice` is false, including for Claude Opus 5.5, Claude Sonnet 5.5, and Anthropic extended thinking.
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

- Use `steer_user_message` to steer with a queued ID or new text and `delete_queued_message` to remove a message. Consumed IDs raise `ChannelMessageNotFoundError`, and `cancel_turn` cannot stop a committed answer. [Reference](/agent-frameworks/built-in-agent-loop#queued-messages-and-steer)
- `cancel_turn` stops model calls, compaction, and waits while started tool Steps finish. It records remote tasks as `outcome_unknown` with `turn_cancelled` even if `cancel_tool_task` (`cancelToolTask`) fails, and `McpToolSource` sends `tasks/cancel`.
- Steering ends a turn at the next safe Step boundary, letting started calls finish and marking unstarted ones `rejected` with `AgentToolCall.cancellation_reason`. Pending steered messages block `execute_plan`. [Reference](/agent-frameworks/built-in-agent-loop#queued-messages-and-steer)
- `AgentState` adds `accepts_turn_control` (`acceptsTurnControl`) for turns started on 0.2.0. `SteeredUserMessages` and `TurnCancellationRequest` persist requests and wake session waits for steering or cancellation.
- `get_agent_snapshot` adds `queued_messages` and `steered_messages` with message IDs next to `queued_message_count`. New events `turn_steered` and `turn_cancelled` report turn endings, and `interrupt_resolved.cancellation_reason` explains removed interrupts.
- `AgentSessionClient` adds `steer_user_message`, `delete_queued_message`, and `cancel_turn`. `AgentSessionTestClient` adds `steerUserMessage`, `steerQueuedMessage`, `deleteQueuedMessage`, and `cancelTurn`, and the TypeScript UI stream emits `finish` reason `other`.
- `build_unfinished_turn_messages` (`buildUnfinishedTurnMessages`) is also
  called when a steered or cancelled turn ends. A runner that keeps its items
  in run state may implement it to keep its exact transcript.

### Tool tasks

Both cores, with identical Step types, wire values, events, and error texts
(the missing-poller text names each language's method). Inert until a runner
returns the new outcome.

- `execute_deferred_tool_call` can return `ToolTaskStarted(task_id, poll_interval_seconds)` for remote work. The call records `waiting_for_task`, stores `task_id` and `task_poll_interval_seconds`, emits `tool_task_started`, and continues in `PollToolTask`.
- `PollToolTask` uses a Timer and can finish, defer, or ask via `AwaitQuestionAnswer`, whose resolution the next poll receives once. Polls get three attempts, steering waits for a started task, and `cancel_turn` abandons it.
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

- `AgentSessionOptions.compaction` takes `CompactionPolicy`, with `trigger_ratio` 0.85 and `retained_ratio` 0.3 by default. Flow construction rejects it unless the runner supports compaction. [Reference](/reference/agents/session-limits#compaction)
- `CompactContext` summarizes before `RunModel` would exceed the threshold, using the same retry policy. The runner then gets `AgentTurnContext.context_summary` (`contextSummary`) and only newer messages. Retention later removes older ones. [Reference](/reference/agents/session-limits#compaction)
- `AgentDefinition.compaction` supports mid-turn summaries without splitting model responses from their tool results. If the summary input plus `max_output_tokens` exceeds `ContextSummaryRequest.input_token_budget`, it omits the oldest complete responses and reports the omission.
- Each `CompactContext` attempt emits a `chat` span for the summary call, as
  `RunModel` does, named from the optional
  `ContextSummaryResult.model_call_metadata` (`modelCallMetadata`).
- The `message_count` of `turn_completed`, `turn_steered`, and
  `turn_cancelled` counts every message the turn appended, also those a
  mid-turn compaction summarized. A steer or cancellation that arrives during
  `CompactContext` applies at the next `RunModel`, before any model call.
- Compaction adds `compacting_context`, `ContextSummary`, `CompactContextInput`, `AgentSnapshot.context_summary`, and `compaction_started` / `compaction_finished`. Browser snapshots expose `context_summary`, and the AI SDK UI stream excludes summary-call usage from message steps.
- `AgentSessionStatus` adds `COMPACTING_CONTEXT` in Python and `"compacting_context"` in TypeScript. Update exhaustive Python matches and upgrade 0.1.0 snapshot readers before enabling compaction. [Reference](/reference/agents/upgrades)
- The 2,000-turn compaction sessions that calibrate the deployment guide run
  in the new `Agents Long Tests Nightly` workflow: Python with
  `DEX_AGENTS_LONG_TESTS=1`, TypeScript with `npm run test:long`. `Agents CI`
  runs an 800-turn guard in each core instead.

### Chunked messages and retention

- `AgentMessageChunks` keeps older messages and `AgentMessages` keeps the newest 64 by default (`message_window_size`). Chunks default to 32 messages and 1 MiB, max 8 MiB. Oversize messages go directly into their own chunk. [Reference](/reference/agents/session-limits#storage)
- `RunModel` no longer loads the whole message map. With compaction, it loads
  the window and only the chunks after the summary; the snapshot and
  `list_agent_messages` load the window and every kept chunk.
- Retention deletes summarized chunks, keeping `retained_message_limit` (default 1,000, or `None`/`null` to keep all). Copy transcripts first and use `AgentMessagesPage.deleted_before_sequence` to identify deleted ranges. [Reference](/reference/agents/session-limits#message-retention)

### Go core

- `github.com/superdurable/dex/agents/go/core` adds the Go session Flow, RPCs, `AgentEvents`, tool policies and approvals, and the `AgentFrameworkRunner` contract. It decodes shared state but refuses steering via `accepts_turn_control`. [Reference](/agent-frameworks/sessions#framework-support)
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

- **Upgrade rule: stop every 0.1.0 Worker of a Flow type before any 0.2.0 Worker of it starts. Do not run them side by side.** 0.2.0 moves older messages to `AgentMessageChunks`, which 0.1.0 cannot read. [Reference](/reference/agents/upgrades)
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
- Use `steer_user_message` and `cancel_turn` after every Worker runs 0.2.0 and turns active during rollout end. Older turns may keep waits that steering cannot wake, and the RPCs refuse turns with `AgentState.accepts_turn_control` false. [Reference](/reference/agents/upgrades)
- `ResumeTurn` now locks `PendingInterrupts` in its short Execute, so
  `resolve_interrupt` and `answer_question` can raise `RpcLockConflictError`
  in sessions that never steer. `AgentSessionClient` retries it for 5 seconds;
  direct `invoke_rpc` callers should retry it too.
- Python `ToolCallStatus` adds three non-final members, so update exhaustive matches. Runners that ask questions implement `AgentFrameworkRunner[DeferredToolOutcome]`, the return type of `AgentSessionFlow.runner`, while a bare `AgentFrameworkRunner` keeps its 0.1.0 default.
- TypeScript `AgentSnapshot.toolCalls` and `Client.getAttributes` now return `AgentToolCallRecord`, so widen `AgentToolCall` assignments to accept the new non-final statuses. Question-capable runners implement `AgentFrameworkRunner<DeferredToolOutcome>` and existing runners keep their default.
- TypeScript Claude Agent SDK counts subagent model calls in `AgentUsage.modelCalls`. Totals for sessions started on 0.1.0 mix old main-conversation-only counts with new counts that include subagents.
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
