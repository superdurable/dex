# Changelog

All twenty-six published Dex agents packages share this changelog and one version line. A
release is tagged `agents/v<version>`. See [README.md](README.md#versions-and-compatibility)
for the Dex SDK and framework versions each release supports.

## 0.6.6 (2026-11-13)

### Added

- Python Claude Agent SDK adapter: `ClaudeAgentRunner` polls and cancels the
  tool tasks of its tool sources. A source call that returns
  `ToolTaskStarted` waits for its task: `poll_tool_task` polls it through the
  source on a Timer, and the resumed Claude session receives its result.
  `cancel_turn` cancels the task through the source's `cancel_tool_task`
  and records the call `outcome_unknown`. Before, such a call recorded `failed`
  for a `read` tool and `outcome_unknown` otherwise. `McpToolSource` now sees
  `supports_tool_tasks` set, so it hands the tasks of a Streamable HTTP
  server to the session to poll instead of finishing them inside the tool
  Step.
  A task that a tool Step was already polling when its Worker upgrades stays
  in that tool Step: a retried CallTool attempt resumes it from the heartbeat
  checkpoint, and if the server no longer knows the task, that attempt sends
  the call again with the same idempotency key. Only calls made after the
  upgrade hand their tasks to the session.
- `@superdurable/dex-agents-claude-agent-sdk`: `ClaudeAgentRunner` polls and
  cancels the tool tasks of its tool sources through the new `pollToolTask`
  and `cancelToolTask`. A source call that returns `ToolTaskStarted` waits
  for its task: the session polls it through the source on a Timer, and the
  resumed Claude session receives its result. `cancel_turn` records the call
  `outcome_unknown` and cancels the task through the source. A source call
  that returns `ToolInputRequested` waits for the user's answer, and the
  source runs the call again with it, as in the Python adapter. Before, a call
  that started a task recorded `failed` for a `read` tool and
  `outcome_unknown` otherwise, and a call that asked a question recorded
  `failed`. `McpToolSource` now sees `supportsToolTasks` set, so it hands the
  tasks of a Streamable HTTP server to the session to poll instead of
  finishing them inside the tool Step. A task that a tool Step was already
  polling when its Worker upgrades stays in that tool Step: a retried
  `CallTool` attempt resumes it from the heartbeat checkpoint, and if the
  server no longer knows the task, that attempt sends the call again with the
  same idempotency key. Only calls made after the upgrade hand their tasks to
  the session. The `dex-agents-claude-sdk` command gets the same behavior for
  its MCP servers and connector tools.

### Changed

- `dex-agents`: `McpToolSource` marks the heartbeat checkpoint of a task it
  hands to the session with `mcp_task_polled_by_session`, as the TypeScript
  source does. A retried `CallTool` attempt returns the saved task as
  `ToolTaskStarted` only when the checkpoint carries the marker, and
  `will_resume_tool_task_without_sending` is `True` only then. A task saved
  without it was polled inside its tool Step, so the retried attempt keeps
  polling it there and sends the call again with the same idempotency key
  when the server no longer knows the task.

### Fixed

- `@superdurable/dex-agents-claude-agent-sdk`: `ClaudeAgentRunner` now
  implements `AgentFrameworkRunner<DeferredToolOutcome>`, and its
  `executeDeferredToolCall` returns `Promise<DeferredToolOutcome>` instead of
  `Promise<ToolExecutionResult>`. `createClaudeAgentSessionFlow` returns
  `AgentSessionFlow<ClaudeAgentRunner>` instead of `AgentSessionFlow`, as
  `createBuiltinAgentSessionFlow` returns `AgentSessionFlow<BuiltinLoopAgentRunner>`.
  The tool tasks and questions above need these types. Code that annotates
  the Flow as a bare `AgentSessionFlow` drops the annotation, or writes
  `AgentSessionFlow<ClaudeAgentRunner>`. A parameter that takes the Flow of
  any adapter can use `AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>`.
  A subclass that overrides `executeDeferredToolCall` declares
  `Promise<DeferredToolOutcome>`, and code that reads `.output` from its
  result first checks that the outcome has no `kind`.

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

- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: the
  configuration file's optional `mcp_servers` map names remote MCP servers,
  each with a `url`, `headers`, and `trust_tool_annotations`. The effective
  configuration carries it as `mcp_servers` (`RemoteMcpServerConfig` values),
  with header values as written. `resolve_mcp_servers` (`resolveMcpServers`)
  expands each `${NAME}` header reference from the environment, treats
  `DEX_API_KEY`, `DEX_CREDENTIAL`, and `DEX_CREDENTIAL_FILE` as not set, and
  raises `McpServerSettingsError` with the setting and an exact message that
  names the variable, never its value. `McpServersToolSource`, source name
  `mcp_servers` (`MCP_SERVERS_TOOL_SOURCE_NAME`, `mcpServersToolSourceName`),
  wraps `McpToolSource`, applies each turn's `approval.always`, and checks it
  again in the tool Step. There it also refuses, unsent, a call that lacks the
  approval or single attempt that the Worker's own `trust_tool_annotations:
  false` requires, and it replaces every header and variable value of at least
  8 characters with `[redacted]` in its log lines, errors, and tool output.
  `mcp_server_secret_texts` (`mcpServerHeaderVariableValues`) returns the
  secret texts to pass it. The new configuration error code is
  `insecure_mcp_server_url`. The rules and cases are in
  `agents/conformance/cli_config.json`.
- `@superdurable/dex-agents-pi-cli` and `dex-agents-deepagents-cli`: writes of
  `mcp_servers` tools wait for approval, as connector writes do.

## 0.6.3 (2026-11-13)

### Added

- `dex-agents` and `@superdurable/dex-agents`: the session Flow registers the
  `SessionTerminalLease` Attribute and the `acquire_session_terminal_lease`
  and `release_session_terminal_lease` RPCs, with the values
  `SessionTerminalLease`, `AcquireSessionTerminalLeaseRequest`,
  `SessionTerminalLeaseResult`, and `ReleaseSessionTerminalLeaseRequest`.
  Acquire succeeds when the lease is free, held by the same `holder_id`, or
  expired, and otherwise returns the other holder's lease; acquiring again
  renews it. Python's `AgentSessionClient` has
  `acquire_session_terminal_lease` and `release_session_terminal_lease`.
  The Go core declares the Attribute and the values, and `remotesession`
  calls both RPCs. A Worker of an earlier release does not serve them, so call
  them only after every Worker of the Flow type runs 0.6.3. Names and golden
  values are in `agents/conformance/names.json` and `values/`.
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: a command
  holds the terminal lease of the session it shows, renews it every 5 seconds
  with a 15-second lease, and releases it on exit. A second command waits with
  one status line that updates in place, then takes the session over. The
  lines, spinner, and timing are in `agents/conformance/cli_session_lease.json`.
  `CommandEnvironment.session_lease_ttl_seconds` reads the test override
  `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS`.
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

- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: the
  configuration file's optional `dex.auth` (`none`, `api_key`, or `bearer`)
  and `dex.tls` (`ca_file`, `server_name`) connect the Worker, the Clients,
  the Worker host, and the terminal UI to a remote Dex server that requires
  TLS and a credential. Secrets come only from `DEX_API_KEY`,
  `DEX_CREDENTIAL`, or a token file. `DEX_TOKEN_URL`, `DEX_SERVER_ID`,
  `DEX_CREDENTIAL_FILE`, `DEX_TLS_CA_FILE`, and `DEX_TLS_SERVER_NAME`
  override the matching fields. The file's `dex.auth` alone turns
  authentication on.
- New configuration error codes: `insecure_token_url`,
  `auth_needs_server_url`, and `tls_needs_https_server`.
  `EffectiveConfig.dex` carries `auth` and `tls`. In TypeScript both are
  optional, so code that builds an `EffectiveConfig` itself keeps compiling.
  `resolve_dex_connection_settings` (`resolveDexConnectionSettings`) returns
  the connection settings without secrets. The `dex_connection` cases in
  `agents/conformance/cli_config.json` fix the rules.
- Terminal UI contract 1: the launch document's `dex.auth` and `dex.tls`
  carry the command's settings instead of `none` and the defaults. The
  terminal UI gets the one credential variable that `dex.auth` names.
- The commands remove `DEX_API_KEY`, `DEX_CREDENTIAL`, and
  `DEX_CREDENTIAL_FILE` from the environment of shell commands, browsers,
  downloads, and `dexcli dev`, also without `dex.auth`. Only the Worker host
  and the terminal UI get the one variable that the mode needs.
  `childProcessVariables` (TypeScript) lacks them too, and the new
  `removeDexSecretVariables` (`remove_dex_secret_variables`) does the removal.
- `dex-agents`, `@superdurable/dex-agents`, and the Go core: the optional
  `sender_name` (`senderName`, `SenderName`) on `AgentUserMessage`,
  `SteerUserMessageRequest`, and `AgentMessage` holds a self-declared display
  name of the person who sent a user message. The model sees the message as
  `"<name>: <text>"` through the framework item, while `AgentMessage.text`
  keeps the text as sent. `send_user_message` and `steer_user_message` return
  false for a name that `is_valid_sender_name` (`isValidSenderName`,
  `IsValidSenderName`) refuses, and for a name sent with `message_id`.
  Encoding omits an absent name, and a Worker before this release ignores it.
- The session graph declares the optional `sender_name` input of the
  `steer_user_message` Action.
- `describe_stored_message` (`describeStoredMessage`), which builds the text
  of a context summary prompt, shows a named user message as
  `user: <name>: <text>`.
- `dex-agents-cli-core` and `@superdurable/dex-agents-cli-core`: the
  `reasoning_effort` session setting sets the reasoning effort of every model
  call from the next turn. A harness declares the values it maps with the
  optional `supported_reasoning_efforts` (`supportedReasoningEfforts`), and the
  turn's value is `ResolvedTurn.reasoning_effort` (`reasoningEffort`). A value
  the harness does not declare fails the turn with
  `unsupported_reasoning_effort`. A harness may also list
  `suggested_models` (`suggestedModels`).
- Python `resolve_turn_config` takes the supported values as the keyword
  argument `supported_reasoning_efforts` and returns the turn's value in
  `TurnConfigResolution.reasoning_effort`. TypeScript `resolveTurn` takes them
  as a third argument and returns `TurnResolution`, with the value in
  `reasoningEffort`. `TurnConfigResolver` takes them at construction in both.
  All these additions are optional. A harness may not declare `default`,
  which the terminal UI reserves, and a suggested model ID has no space or
  control character.
- Terminal UI contract 1: the launch document carries the optional
  `display.models` and `capabilities.reasoning_effort`, which dexcli 1.47.0
  reads for `/model` and `/effort`. Older dexcli releases ignore them.
- Harness commands: `dex-agents-claude-sdk` maps `reasoning_effort` to the
  Claude Agent SDK `effort` (`low`, `medium`, `high`, `xhigh`, `max`). The
  built-in commands, `dex-agents-openai-sdk`, `dex-agents-pydantic-ai`,
  `dex-agents-vercel-ai`, and `dex-agents-pi` map `low`, `medium`, and `high`
  to their SDK's reasoning option. `dex-agents-deepagents`,
  `dex-agents-google-adk`, and `dex-agents-mastra` declare none yet. Every
  command suggests a few models for `/model`.

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

- The terminal UI of the Python Agents CLI commands (`dex-agents-cli-core`) no
  longer prints `The chat closed: dexcli closed the channel.` when you exit
  normally after a turn failed. `dexcli` closing the channel and exiting by
  itself is a normal end. The command still exits 1 after a failed turn, as
  the plain chat does. The line still appears when `dexcli` closes the
  channel and keeps running, or sends a malformed line.
- The terminal UI of the Python Agents CLI commands now prints
  `The chat closed unexpectedly (dexcli exit 143).` when `dexcli` exits 143
  by itself, such as after a SIGTERM sent to `dexcli` alone, as the
  TypeScript commands do. The command still exits 1.

## 0.6.0 (2026-11-12)

### Breaking

- `dex-agents-deepagents` (Python): file writes and shell commands no longer
  wait for approval by default, as in Deep Agents without `interrupt_on`.
  Name `write_file`, `edit_file`, `delete`, `execute`, or `task` in
  `approval.always` to make them wait. `local_tools.shell.allow` has no effect
  on this command. The tool names are Deep Agents' own, so `read_file`,
  `write_file`, and `edit_file` take `file_path` with an absolute path, and the
  CLI core's `list_directory`, `search_files`, and `run_shell_command` are gone
  from this command; a resumed session replays its earlier calls of them
  unchanged.
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
- Python and TypeScript cores: `ToolExecutionResult` has an optional
  `is_finished_by_resumed_turn` (`isFinishedByResumedTurn`). A runner sets it
  when its tool Step only approves a tool that really runs later, inside the
  resumed `RunModel`, such as a Claude Code tool. The session still records
  `succeeded` and writes `tool_call_started`, but writes no
  `tool_call_finished`; the runner writes that event when the resumed turn
  runs the call. Nothing new is stored, and runners that leave it unset
  behave as before.
- Go core: package `remotesession` calls agent sessions that Python or
  TypeScript Workers serve, from a Go Client that serves none.
  `RemoteSessionFlow` registers every session RPC of those cores with the same
  names, locks, state loads, and wire values, including `answer_question`,
  `cancel_turn`, `steer_user_message`, `delete_queued_message`,
  `execute_plan`, and `send_user_message` with `plan_mode` and `images`, plus
  the `AgentEvents` Stream; `RemoteSessionClient` types the calls. The flow is
  client-only, so a Go Worker refuses it. The Go core adds the wire types
  `ExecutePlanRequest`, `SteerUserMessageRequest`,
  `DeleteQueuedMessageRequest`, `CancelTurnRequest`, and
  `AnswerQuestionRequest`. The Go Worker's session Flow is unchanged.
- Python and TypeScript cores: a tool approval can say what the call acts on
  and what it does. New `AgentInterruptDisplay` `{target, summary, preview}`:
  a tool source returns it in `DeferredToolCall.approval_display`
  (`approvalDisplay`), and the session stores it in the new optional
  `AgentInterrupt.display`, on the call's record as `approval_display`
  (`approvalDisplay`), and in the `interrupt_raised` event's new `display`
  key. A queued ordered write raises its approval later with the display its
  source built. The session cuts a target over `MAX_APPROVAL_TARGET_BYTES`
  (`maxApprovalTargetBytes`, 1 KiB) and a summary over
  `MAX_APPROVAL_SUMMARY_BYTES` (`maxApprovalSummaryBytes`, 2 KiB), ending
  them with `…`, and drops a preview over `MAX_APPROVAL_PREVIEW_BYTES`
  (`maxApprovalPreviewBytes`, 16 KiB). Each new field is `null` when there is
  none, so a reader treats a missing display as "show the arguments"; a
  Worker before 0.6.0 ignores the fields and drops them when it rewrites a
  record or `PendingInterrupts`. The Go core decodes and keeps both fields.
- Python and TypeScript cores: `ConnectorToolSource` sets the display of a call
  that needs approval, with the new `build_connector_tool_approval_display`
  (`buildConnectorToolApprovalDisplay`): the target is
  `<connector>/<connection>`, and the summary names the operation, the
  connection, and up to five short scalar arguments, never the operation's
  description. A credential-like argument, such as `credentials`, `apiKey`, or
  `PGPASSWORD`, shows `<redacted>`, and string values and names have common
  credential shapes replaced. The new `is_credential_like_name`
  (`isCredentialLikeName`) and `redact_inline_credentials`
  (`redactInlineCredentials`) hold the rules, and
  `agents/conformance/approval_display.json` fixes them for every core. This
  redaction is best-effort hygiene for displayed text, not a security
  boundary: the session stores the call's full arguments as before.
- Python and TypeScript CLI cores: a `write_file` or `edit_file` approval names
  the workspace path and carries a unified diff of the file as it is when the
  model asks against the new content, with header lines that say so and
  `\ No newline at end of file` markers. The preview reads the file with the
  write's own link and workspace checks. It is left out for files or content
  over 64 KiB, diffs over 200 lines, paths the write would refuse, content the
  best-effort credential redaction changes, and credential files such as
  `.env`, `.aws/credentials`, `.kube/config`, or `secrets.yaml`. A
  `run_shell_command` approval names the working directory and the command
  with common credential shapes redacted; the call's arguments keep the full
  command. A connector tool's approval names its operation, connection, and
  short arguments.
- TypeScript CLI core (`@superdurable/dex-agents-cli-core`):
  `AgentHarness.defaultWorkspaceLocation`. With `launch_directory`, the
  workspace is the directory the command starts in when the file sets none,
  and `init` writes no `workspace`. `resolveConfig` takes the harness's
  default workspace as an optional fourth argument.
  `ConnectorsMcpToolSource.deferredToolCall` and `executeDeferredToolCall`
  take an optional name for `approval.always` to match, for a wrapper that
  offers the tool under another name. All additions are optional, so existing
  harnesses build and behave as before.
- `@superdurable/dex-agents-pi-cli`: `PrefixedConnectorToolSource` and
  `collidingConnectorToolPrefix`, which offer the connector tools beside Pi's
  tools.
- Python, TypeScript, and Go cores: a stored user message that the session
  wrote itself carries the new optional `AgentMessage.origin`, so a client can
  tell it apart from text a person typed. The values are `plan_execution` for
  the message that starts an `execute_plan` turn, and `turn_failure_note`,
  `turn_steered_note`, and `turn_cancelled_note` for the notes that tell the
  model which tool calls of an ended turn ran. The new `AgentMessageOrigin`
  names them. A message that a user sent, and every message stored before
  0.6.0, has none. The wire field is `null` when absent; a Worker before
  0.6.0 ignores it and drops it when it moves the message into an archive
  chunk. Decoders keep a value they do not know, so a later value still
  reads as a session-written message. The Go core decodes the field in
  `remotesession` and sets `turn_failure_note` on its own turn-failure note.
- Python and TypeScript cores: a session Flow can keep a summary for session
  lists in three indexed Attributes. Turn it on with
  `AgentSessionOptions.summary_attributes_indexed` (Python) or
  `summaryAttributesIndexed` (TypeScript); it is off by default, and then the
  Flow neither declares nor writes them. `flow.options` reports the setting.
  `AgentSessionTitle` (FULL_TEXT, index key `text1`) is the first user
  message with each run of ASCII whitespace made one space, cut to its first
  80 Unicode code points, and only the first turn writes it.
  `AgentSessionLastActivityAt` (DATETIME, `datetime1`) is the RFC 3339 time
  the first attempt of the Step that started or ended the latest turn
  started. `AgentSessionTurnCount` (INT, `int1`) is `AgentState.turn_count`
  as of that Step. Python exports them as `AGENT_SESSION_TITLE`,
  `AGENT_SESSION_LAST_ACTIVITY_AT`, and `AGENT_SESSION_TURN_COUNT`; the
  TypeScript Flow has `agentSessionTitle`, `agentSessionLastActivityAt`, and
  `agentSessionTurnCount`. The persisted format only gains these Attributes.
- Adapter factories take the same option: Python `summary_attributes_indexed`
  of `builtin_agent_session_flow`, `openai_agents_session_flow`,
  `pydantic_ai_session_flow`, and `adk_agent_session_flow`; TypeScript
  `summaryAttributesIndexed` of `createBuiltinAgentSessionFlow`,
  `createOpenAIAgentsSessionFlow`, `createMastraAgentSessionFlow`,
  `createVercelAiAgentSessionFlow`, and `createClaudeAgentSessionFlow`.
- Before turning the option on: the title is the user's own words, and anyone
  who can search the namespace's Flows reads it. With PostgreSQL search, a
  Worker with the option fails to start with `FAILED_PRECONDITION` when the
  namespace registered `text1`, `datetime1`, or `int1` with another type, or
  when all three keys of one of these types are taken by other names.
- Python and TypeScript cores: `list_agent_sessions` / `listAgentSessions`
  reads one page of a session Flow type's sessions, with an optional title
  search, in one `SearchFlows` call, without snapshots and without a Worker.
  A title search raises `SessionTitleSearchUnavailableError` while the
  namespace has not registered `text1`; another search error stays a
  `DexServiceError`. `sort_agent_sessions_by_last_activity` /
  `sortAgentSessionsByLastActivity` sorts loaded sessions, because Dex sorts
  only by start or close time. `build_session_title` / `buildSessionTitle`
  gives the title rule, which the conformance suite fixes in `names.json`
  `session_titles`.
- A session without the option, or one that an earlier release started,
  lists without the summary and never matches a title search. An earlier
  session gains the last activity time and the turn count at its next turn on
  a Worker with the option. During a rolling upgrade, turns that an earlier
  Worker or a Go Worker runs leave the summary as it was. An earlier session
  with no turn yet gains a title at its first turn. Turning the option off
  stops new writes; the values written before stay. A session that fails
  because its state would exceed the server's limit can keep the values of its
  last turn start.
- Python and TypeScript CLI cores: a new `sessions.summary` setting, `auto`
  by default, decides whether the commands' session Flows keep the summary.
  `auto` keeps it only with `dex.server: auto`, the local `dexcli dev`, and
  leaves a Dex server URL alone; `on` keeps it with any server; `off` never.
  The CLI core passes the result to the harness as
  `HarnessContext.is_session_summary_indexed` /
  `HarnessContext.isSessionSummaryIndexed`; every built-in harness builds its
  Flow with it, and the command refuses to start when a harness's Flow does
  not follow it, in either direction. The check reads the Flow's persistence
  schema, which decides the index keys the Worker registers, and its options;
  it verifies what the Flow declares and does not sandbox harness code. A
  custom harness must honor the field.
  The TypeScript `EffectiveConfig` gains an optional `sessions` field, which
  `resolveConfig` always sets. `sessions` loads up to five pages of 50
  sessions, sorts them by last activity, says when more exist, and adds the
  time of the latest turn, the turn count, and the title. The TypeScript line
  keeps its tab-separated ID, status, and start time and appends the three
  columns, with `—` for a missing value.
- The Python CLI commands (`dex-agents-cli-core`) accept `--plain`, which
  keeps the chat in plain text lines instead of the `dexcli` terminal UI.
- The TypeScript CLI commands (`@superdurable/dex-agents-cli-core`) take
  `--plain`, which keeps the chat in plain text instead of the `dexcli`
  terminal UI. Pi always keeps the plain text chat, through the new
  `AgentHarness.terminalInterface` declaration (`"plain"`).

### Changed

- `dex-agents-claude-sdk` (Python and TypeScript): the command no longer loads
  MCP servers or claude.ai connectors from the user's Claude Code
  configuration or login, such as a workspace `.mcp.json`, a user-scope MCP
  server, or the Gmail and Google Drive connectors of a claude.ai account. Its
  Claude Agent SDK options set `strictMcpConfig` (`strict_mcp_config`) and the
  flag setting `disableClaudeAiConnectors`, so the model sees only Claude
  Code's built-in tools, the browser tools, and the Dex connector tools, which
  Dex approves and records as Steps. The Claude Agent SDK adapters keep their
  defaults for applications.
- **dex-agents-builtin** (Python and TypeScript): plan mode is on, and the
  agent has the `write_todos` built-in tool next to `ask_user`. In the
  terminal UI, `/plan` drafts a plan with `write_todos` only, and `/run`
  approves it and runs it with every tool; the session API works too. The
  plain chat has no plan commands.
- **dex-agents-builtin**: `write_todos` is now a reserved tool name, so a
  connector or local tool with that name fails the turn. Rename the
  conflicting tool, or use another harness.
- Python CLI core, terminal UI: the startup notices of the Worker host, such
  as connector tools that are unavailable, and the lines about a `dexcli dev`
  that the command started, such as its log file, reach the terminal UI as
  launch notices. Before, they were printed only before the terminal UI
  covered the screen, so the user saw them after exiting.
- Python and TypeScript CLI cores (`dex-agents-cli-core`,
  `@superdurable/dex-agents-cli-core`): a `dexcli dev` that a command started
  for `dex.server: auto` now stops when the command exits, instead of running
  on. This covers `/exit`, the end of input, the Ctrl-C that exits, a failure,
  SIGTERM, and SIGHUP, and the `run`, `sessions`, and `connectors`
  subcommands. The command removes `~/.dex/agents/dexcli-dev.json`, sends
  SIGTERM to the `dexcli dev` process group, which includes `dex-server-zd`,
  then SIGKILL after 20 seconds, and prints "Stopped the dexcli dev this
  command started." Another signal during those 20 seconds sends SIGKILL at
  once. The command signals the group only while `dexcli dev` itself still
  runs. After SIGTERM or SIGHUP it exits with 128 plus the signal number. The
  startup line says that the server stops with the command.
- A reused `dexcli dev` is never stopped: one you started, or one that another
  command started. To share one server across sessions, run `dexcli dev`
  yourself first, or set `dex.server`. A command that reused another command's
  `dexcli dev` fails with exit code 1 and one short line when that server
  stops. A command killed with SIGKILL, or one that crashes, leaves its
  `dexcli dev` running; later commands reuse it and do not stop it.
- On Windows, the TypeScript CLI core stops a started `dexcli dev` with
  `taskkill /T`, then `taskkill /T /F`, so `dex-server-zd` stops too. The
  Python CLI core sends CTRL_BREAK_EVENT first, then `taskkill /T /F`. It no
  longer checks the recorded `dexcli dev` with `os.kill(pid, 0)`, which ends
  that process on Windows.
- TypeScript CLI core: during the chat, the Dex SDK Worker's task stream
  reconnect messages go only to `~/.dex/agents/<harness>.log`, so a lost Dex
  server no longer prints stack traces into the chat. Other library warnings
  show their first line on the terminal and their full text in the log file.
- TypeScript CLI core: closing the terminal no longer ends a command on Linux
  with an uncaught `setRawMode EIO` error before it handles SIGHUP. The
  command now stops the `dexcli dev` it started and exits with 129.
- `dex-agents-deepagents` (Python) keeps Deep Agents' own tools: `ls`,
  `read_file`, `write_file`, `edit_file`, `delete`, `glob`, `grep`, and
  `execute` on the real workspace files through a `LocalShellBackend` rooted at
  the workspace, LangChain's `write_todos`, and the `task` subagent tool. They
  replace the CLI core's file and shell tools; the connector, web fetch, and
  browser tools stay. Connector writes always wait for approval. File tools
  refuse a path whose real path is outside the workspace, and `ls`, `glob`,
  and `grep` leave out such entries, such as links to files elsewhere. This
  is a guardrail against mistaken paths, not a sandbox: `execute` runs any
  command with the user's permissions. `task`
  runs its subagent inside one tool Step, with the configuration file's model
  and no Steps or approvals of its own, so it waits for approval whenever
  `approval.always` names any Deep Agents tool. A connector tool whose name a
  Deep Agents tool has gets the prefix `connector_`, repeated until the name
  is free. Large tool results and long user messages go to a 0700 directory
  per session under `~/.dex/agents/deepagents-artifacts`, which the command
  deletes at startup once its files are 30 days old. `write_todos` returns a
  graph `Command` that updates the todo list for the turn. Deep Agents' summarization stays off.
- Python LangGraph adapter, behavior change: `should_defer_tool_calls_before_middleware`
  now defaults to `True` in `LangGraphAgentRunner`,
  `langchain_agent_session_flow`, and `langgraph_agent_session_flow`. In a
  session the deferral is the outermost tool wrapper of every root graph tool
  node that has an async tool wrapper, whatever the middleware order, and
  each tool Step attempt runs the node's other middleware tool wrappers around
  the tool. Middleware listed before `DurableToolCallMiddleware` no longer see
  calls in `RunModel`, and middleware listed after it now run in the tool Step;
  their side effects follow the tool's retry policy. Every call of such a
  node with a wrapper besides the bare deferral, as in every `create_agent`
  graph, stores the non-message graph state with its record, so N calls of
  one model response store it N times. Pass
  `should_defer_tool_calls_before_middleware=False` to keep the earlier
  order. Each turn keeps the order its first model call used, recorded in its
  run state and call records, so a Worker with the other setting finishes it
  without running a wrapper twice; turns paused before this release keep the
  middleware order. Known upgrade limitation: a turn in flight under a 0.5.x
  runner that set the option to `True`, such as the 0.5.0
  `dex-agents-deepagents`, recorded no order, so it finishes in the
  middleware order and a wrapper listed before the deferral runs once in
  `RunModel` where 0.5.x ran none; it never runs twice. A tool node without
  wrappers, a tool node in a subgraph, and a node with only synchronous
  wrappers are left unchanged. A tool name that root tool nodes with any
  different tool wrappers share, a bare deferral and a wrapped node
  included, now raises `ValueError` when the runner loads the graph.
- Python LangGraph adapter: a tool Step that runs a tool node's wrappers no
  longer applies the node's `handle_tool_errors`, so an exception in the tool
  body, such as a `ValidationError`, fails the attempt and a write records
  `outcome_unknown`, as it does without wrappers.
- Python LangGraph adapter: an `InjectedStore` parameter of a tool receives
  the compiled graph's store in its tool Step.
- Every agents TypeScript package (`@superdurable/dex-agents`, the adapters,
  `@superdurable/dex-agents-pi`, and the agent CLI packages) adds Node 26
  (`^26.0.0`) to its **engines.node** range, so installing on Node 26 no longer
  prints `EBADENGINE` warnings for them. The two Pi packages keep their
  `^22.19.0` lower bound. Agents CI runs the TypeScript packages on Node 22
  and Node 26.
- `dex-agents-pi` (TypeScript) keeps Pi's own `read`, `grep`, `find`, `ls`,
  `write`, `edit`, and `bash` with Pi's behavior, built from pi-coding-agent's
  tool factories with their default local operations. Each call is still its
  own Dex tool Step: reads retry, and `write`, `edit`, and `bash` never run
  twice. `bash` output reaches the session as `pi.tool_progress` events while
  it runs. The connector, web fetch, browser, and `ask_user` tools stay.
  Connector writes always wait for approval, also when the connector marks
  them idempotent. A connector tool whose name a Pi tool has gets the prefix
  `connector_`, repeated until the name is free.
- Python and TypeScript CLI cores: the commands download `dexcli` 1.46.0,
  which has the terminal UI, instead of 1.45.0 when they find no `dexcli` of
  at least 1.46.0. A `dexcli` on `PATH` older than 1.46.0 is no longer used.
- Python and TypeScript CLI cores: at a terminal, `run` now chats in the
  `dexcli agents-tui` terminal UI by default. `--plain` keeps the plain text
  chat. The plain chat is still used without a message when standard input or
  output is not a terminal, when `TERM` is unset or `dumb`, on Windows, and in
  `dex-agents-pi`; it is used with a `Plain chat:` line when `dexcli` has no
  terminal UI or the terminal UI fails to start.

### Fixed

- Python and TypeScript Claude Agent SDK adapters: when the user rejects a
  call of a tool that runs inside the Claude Code CLI, such as `Write` or
  `Bash`, the model now sees "The user rejected this Write call: not now"
  instead of "PreToolUse:Write hook error: not now". The resumed PreToolUse
  hook answers the rejected record with `ask`, and `can_use_tool` /
  `canUseTool` denies the call with that text, which Claude Code passes to the
  model without a prefix. A rejection without a reason reads "The user
  rejected this Write call.", and deadline, planning-turn, and cancellation
  rejections keep their recorded text. The session still records the call as
  `rejected`.
- Python and TypeScript Claude Agent SDK adapters: an approved call of a tool
  that runs inside the Claude Code CLI writes one `tool_call_started`, from its
  tool Step, and one `tool_call_finished`, from the CLI's tool result in the
  resumed turn segment: `failed` when the run fails, such as a `Bash` command
  that exits non-zero, and `succeeded` otherwise. A segment that ends without
  that result, such as one that fails or is cancelled, writes
  `outcome_unknown`. The tool Step returns `is_finished_by_resumed_turn`
  (`isFinishedByResumedTurn`), so it no longer writes a `succeeded` finish for
  the approval, and `dex-agents-claude-sdk` prints `[tool] Write started` and
  `[tool] Write succeeded` once. A retried `RunModel` attempt runs the tool
  again and reports it again, as every event of a retried attempt may repeat.
- `dex-agents-claude-sdk` (Python): Claude Code's standard error goes to the
  command's log file instead of the chat terminal, with OSC, CSI, and other
  terminal control sequences removed, so the cursor control sequence
  `ESC[?25h` that Claude Code writes to a terminal when it exits no longer
  appears in the chat after every turn segment. A failed write to the log
  drops the line and never reaches the terminal.
- Python and TypeScript Claude Agent SDK adapters: a stored Claude Code
  login keeps working after the first turn. Every later turn segment resumes
  from a temporary `CLAUDE_CONFIG_DIR` that the Claude Agent SDK prepares, and
  on macOS and Linux Claude Code then looked for its stored login, such as the
  macOS Keychain entry, under that directory. Without an `ANTHROPIC_API_KEY`,
  every later turn failed with "Not logged in · Please run /login", also in
  `dex-agents-claude-sdk`. The adapters now set
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` in the CLI environment to the effective
  `CLAUDE_CONFIG_DIR`, or to `""` without one, unless the application or the
  Worker environment already sets it. The TypeScript adapter merges it into the
  application's `env`, or into a copy of `process.env` when `env` is unset.
- Ctrl-C at a configuration question of `init`, or of the first run without a
  configuration file, now quits the CLI commands of both CLI cores
  (`@superdurable/dex-agents-cli-core` and `dex-agents-cli-core`). The command
  prints `Cancelled; no configuration written.`, writes no file, and exits with
  code 130. Before, the TypeScript commands ignored Ctrl-C there, and the Python
  commands ignored it during the first run and printed a traceback in `init`.
- Ctrl-D at a configuration question now cancels the same way and exits with
  code 1. Before, the Python commands took the default answer and went on.
- Python and TypeScript CLI cores: a Dex server that cannot be reached at
  startup stops the command with one line that names its address and says how
  to fix it, instead of the raw gRPC error. A reachable server that answers
  `UNAVAILABLE` is named with its details and a hint to try again. The Python
  commands printed a traceback before.
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
- The shell tool of the Python CLI commands (`dex-agents-cli-core`) now stops
  what a command leaves running in its process group, such as `sleep 30 &`,
  when the command ends: SIGTERM, then SIGKILL 0.5 s later. Before, those
  processes ran on after the plain chat's tool call returned, and one holding
  the output kept the call waiting until it exited.
- A process that left the command's group, for example with `setsid`, and
  still holds its output no longer delays the result by more than 1 s after
  the group is cleaned. It is not stopped.
- The Python shell tool's result now has the TypeScript CLI's form: a timeout
  is a result, `killed: timed out after N seconds`, instead of a tool error; a
  shell that a signal ended reports `exit code 128+N`; both `--- stdout ---`
  and `--- stderr ---` always appear, each cut at 64 KiB with a
  `[cut at 65536 bytes]` line. The output is cut while it is read, so a
  command that prints a lot no longer fills the memory.
- A cancelled shell tool call now finishes stopping the command's group,
  reaping the shell, and closing its pipes before the cancellation goes on.
  One thread per call owns the shell, so this cleanup also finishes when the
  event loop closes or the interpreter exits.
- The shell tool now gives what a command leaves in its group up to 0.5 s
  after SIGTERM, even when the command's output already ended, and stops the
  group even when its output cannot be read. A command that leaves nothing
  behind gets no signal and no wait.
- Python and TypeScript CLI cores: the terminal shows exactly one short line
  for each change in connector tool availability: when the connectors MCP
  server stops answering, naming the log file `~/.dex/agents/<harness>.log`
  and the Dex Web Connectors page, and when connector tools are available
  again. A harness that fixes its tools at build gets one line saying to
  restart, also when the startup listing fails. Each failed listing and its
  full error go to the log file only. Before, the TypeScript commands printed
  the whole error at startup and twice before every turn.
- Python and TypeScript CLI cores: a connectors MCP server that answers HTTP
  401 or 403, also only to `tools/list`, gets a line saying it denied access
  and to check its access settings and `connectors.mcp`, instead of a line
  saying each turn tries again.
- Python CLI core: a command whose `~/.dex/agents` directory or
  `<harness>.log` file cannot be written prints `Logging is off` with the
  reason and starts without a log file, instead of exiting with an error.
- **ConnectorsMcpToolSource** takes the new `startupAvailability`,
  `writeLogLine`, and `describeUnavailable` options in TypeScript, and
  `describe_unavailable` and `startup_error` in Python. In Python, the line
  `report` prints no longer carries the error, which goes to the `logging`
  warning log. `ConnectorsMcpAvailability` in TypeScript carries the HTTP
  status of a server that answered, and Python raises
  `ConnectorsMcpAccessDeniedError` for HTTP 401 and 403. In TypeScript,
  `readIdentities` and `listConnectorToolIdentities` take an `AbortSignal`,
  and each of their requests waits at most 15 seconds, so a failed listing
  is reported without waiting for a server that does not answer.
- Python and TypeScript cores: **McpToolListingError** carries the HTTP
  status a Streamable HTTP server answered to the failed listing, as
  `httpStatus` in TypeScript and `http_status` in Python.
- TypeScript core: **McpToolSource** takes a `logWarning` option that receives
  its warnings, such as a server left out of the tool listing, instead of
  `console.warn`. A `logWarning` that throws falls back to `console.warn`, so
  it never fails `listTools()` or a tool call.
- The `sessions` subcommand of both CLI cores
  (`@superdurable/dex-agents-cli-core` and `dex-agents-cli-core`) now loads
  pages until it has 250 sessions or Dex has no more, instead of stopping after
  five pages. A page with fewer than 50 sessions no longer shortens the list.
  It sends at most 20 requests, and says that more sessions exist when it
  stops before the end.
- `runAgentCli` of `@superdurable/dex-agents-cli-core` now returns or throws
  only after its output reached stdout and stderr, waiting at most 2 seconds.
  A handled SIGTERM, SIGHUP, or SIGINT waits the same way before the command
  exits. Before, a harness command that exits
  with `process.exit(await runAgentCli(...))` could lose the end of a long
  `sessions` list written to a pipe, including the `Resume one with` line.
- Python and TypeScript CLI cores: a connectors MCP server that answers
  `initialize` but cannot list its tools, such as one answering HTTP 503 to
  `tools/list`, no longer stops an agent command at startup. The command prints
  one line saying connector tools are unavailable for now, writes the error to
  its log file, and starts the chat; each turn lists the server again. An explicit
  `connectors.mcp` URL that does not answer `initialize` still stops the
  command.
- The private test engine (**PrivateDexEngine** in `dex_agents.testing`,
  `@superdurable/dex-agents/testing`, and the Go `agentstest` package) no
  longer downloads or runs a Connectors MCP server. It sets
  `DEX_CONNECTORS_MCP_BINARY` for its `dexcli dev` to a file that does not
  exist, so a test never depends on a published Connectors MCP release. The
  engine also runs with `HOME` set to a directory under its state directory,
  so it never reads or writes the user's `~/.dex`.
- Python LangGraph adapter: with `should_defer_tool_calls_before_middleware`,
  a tool Step runs the call through its root graph tool node's middleware tool
  wrappers, such as Deep Agents' large-result eviction, in each attempt;
  their side effects follow the tool's retry policy. Before, the tool Step called the tool alone, so no wrapper ever
  ran. The wrappers see the state of the call's deferral without the
  message list, and their state writes are recorded with the tool's.
  `RunModel` replays the wrapped result without running the wrappers. Every
  call of a root tool node then stores the non-message state in its record.
  Without the option, nothing changes. `dex-agents-langgraph` now requires
  `langgraph` 1.2, `langgraph-prebuilt` 1.1, and `langchain-core` 1.7, the
  tested releases, because it calls a private `ToolNode` method.
- Python LangGraph adapter: a tool's `ToolRuntime` in its tool Step has the
  compiled graph's `store` instead of `None`. It still has no `context`.
- TypeScript OpenAI Agents adapter and the TypeScript **dex-agents-openai-sdk**:
  the model sees each tool source's tool, such as a Connector tool, by its
  source name, as the Python adapter does. The OpenAI Agents SDK's `tool()`
  used to turn every `-` into `_`, so the model saw
  `hacker_news_daily_list_story_ids` for `hacker-news-daily_list_story_ids`,
  and two tools that differ only by `-` and `_` could not both be offered.
  Only a character the OpenAI API refuses, one other than a letter, digit,
  `_`, or `-`, still reaches the model as `_`.
- The shell tool of the TypeScript CLI commands stops what a command left in
  its process group when the command ends, such as a process started with
  `&`, and answers at once. Before, such a call waited for its timeout and
  then killed the group.
- The Python CLI core (`dex-agents-cli-core`) builds a new session ID from the
  UTC start time, as the TypeScript CLI core does, instead of the local time.
  Both now give the same shape, such as `builtin-20261111-203123-3efeb4`.

## 0.5.0 (2026-11-11)

### Dex SDK 1.45.0

- Every package requires Dex SDK 1.45.0 (`dex-python-sdk==1.45.0`,
  `@superdurable/dex` `1.45.0`, Go `sdk-go` `v1.45.0`) instead of 1.38.0, so an
  application can use the SDK's **Join** helper with the agents packages
  without overriding the SDK version, and for Worker-side Flow definition
  upload. The npm adapters peer-depend on `@superdurable/dex` `1.45.0`.
- The SDK pin stays exact, because an SDK minor release may break APIs that
  an application shares with the agents packages. The agents packages use
  none of the APIs that SDK 1.43.0 removed, and start no SubFlows, which a
  Step type with "-" can no longer do since SDK 1.45.0. Before upgrading, read
  the breaking changes of SDK 1.43 and 1.45 in the
  [Python](../docs/reference/python/changelog.mdx),
  [TypeScript](../docs/reference/typescript/changelog.mdx), and
  [Go](../docs/reference/go/changelog.mdx) SDK change logs.
- Dex SDK 1.45.0 still needs a Dex Server with protocol 4: server-zd v0.1.17
  or later.
- Agents CI, the nightly long run, and the publish workflows test against
  `dexcli` 1.45.0. The Python and TypeScript CLI cores download `dexcli`
  1.45.0 when none is installed.

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
- Python core: `McpToolSource.execute_deferred_tool_call` takes
  `request_meta`, more `_meta` entries for the `tools/call` request, such as
  the identity a caller expects the server to check. The idempotency key wins
  over an entry of the same name. New
  `McpToolSource.will_resume_tool_task_without_sending` tells whether
  executing a call resumes a task an earlier attempt started and can never send
  `tools/call` again.

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

### Showing sessions in Dex Web

- Python and TypeScript cores: new **session_flow_definition_document**
  (`sessionFlowDefinitionDocument`) returns the session Flow Definition Graph
  of one agent as the SDK's **FlowDefinitionDocument**. Pass it in
  `WorkerOptions(flow_definitions=...)` (`flowDefinitions`), and the Worker
  uploads the graph when it starts, so Dex Web shows the agent's sessions and
  Actions without `--flow-rendering-dir`. The upload needs server-zd 0.1.26 or
  later; an older server only logs a warning. Rendering a file with
  **render_session_flow_graph** stays for older servers.
- The document's `generated_at` is a fixed time, the time the graph template
  last changed (`SESSION_FLOW_GRAPH_GENERATED_AT` in
  `dex_agents.session_graph`), the same in both cores. A Worker restart writes
  nothing, and an upgrade replaces the graph. The server keeps a stored graph
  whose time is the same or later, so changing only the Action permission
  needs one upload with `force`. The conformance file
  `session_graph_renderings.json` records the time and the template hash, and
  `generate.py` refuses a template change without a later time.
- Python and TypeScript CLI cores: the command uploads the graph of its
  suffixed session Flow type when it starts, so CLI sessions appear in Dex Web
  with `dexcli dev` 1.45.0. The outcome and any failure go to
  `~/.dex/agents/<harness>.log`, not the terminal. The session Actions require `dex-agents-cli.operate`
  (**SESSION_ACTION_PERMISSION**, `sessionActionPermission`) in every CLI
  core; the conformance file `cli_session_flow_types.json` fixes it.
- Go core: no session graph yet.

### Ending or continuing a built-in loop turn

- Python and TypeScript cores: new built-in loop hook
  **AgentDefinition.decide_turn_continuation** (`decideTurnContinuation`). At
  the end of each loop iteration it receives a **TurnContinuationContext**
  (the iteration's model output and tool results) and returns `"end"`,
  `"default"`, or a continuation with a message: **ContinueTurn(message)** in
  Python, `{ action: "continue", message }` (**ContinueTurn**) in TypeScript.
  After tool calls, `"end"` completes the turn in the next `RunModel` Step
  without a model call, so the turn's last message is a tool result. After an
  answer without tool calls, a continuation stores the answer and its message
  as user text, then runs another `RunModel` Step in the same turn; the next
  request ends with that user message, as every provider requires. After tool
  calls, a continuation's message follows the tool results. `"default"` and
  no hook keep the earlier behavior.
- At most **MAX_CONSECUTIVE_TURN_CONTINUATIONS**
  (`maxConsecutiveTurnContinuations`, 8) continuations in a row; the next one
  ends the turn with its answer stored and `last_turn_error` set to
  **TURN_CONTINUATION_LIMIT_ERROR**. A tool call resets the count. An
  exception in the hook, or an invalid result such as a blank message, fails
  the `RunModel` attempt, which the retry policy runs again.
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

- Python CLI core (`dex-agents-cli-core`, first published in 0.5.0): the shared CLI core
  of the Python harness commands. It reads `~/.dex/agents/<harness>.yaml` and
  asks the init questions on first run, reuses or starts `dexcli dev`
  (downloading a checksum-verified `dexcli` when none is installed), offers the
  connectors MCP server's tools and local file, shell, web fetch, and browser
  tools as Dex tool Steps, narrows each turn by the session settings, and chats
  in the terminal with inline approvals and Ctrl-C to cancel a turn. Harness
  packages implement **AgentHarness**. See
  [cli/python/core](cli/python/core/README.md).
- The **dex-agents-claude-sdk** command, in Python (`dex-agents-claude-sdk-cli`)
  and TypeScript (`@superdurable/dex-agents-claude-sdk-cli`), first published
  in 0.5.0: a Claude Agent SDK agent in the terminal on the shared CLI core.
  Claude Code's own Read, Grep, Glob, Write, Edit, Bash, and WebFetch tools
  replace the CLI core's file, shell, and web fetch tools, with the same rules:
  each turn offers only the groups its session settings switch on, reads run at
  once, writes and shell commands wait for approval unless the shell allow list
  has the command, file tools stay in the workspace, and the tool Step checks an
  approved call again. `ANTHROPIC_API_KEY` reaches Claude Code unchanged. See
  [cli/python/claude-sdk](cli/python/claude-sdk/README.md).
- The **dex-agents-pi** command (`@superdurable/dex-agents-pi-cli`, first
  published in 0.5.0): a Pi coding agent in the terminal on the shared CLI core,
  where Dex runs the agent loop and pi-ai makes each model call. The model is
  `<provider>/<model>` from pi-ai's catalog, and the key comes only from the
  provider's API key variable. pi-coding-agent's `read`, `grep`, `find`, `ls`,
  `write`, `edit`, and `bash` replace the CLI core's file and shell tools, with
  the same rules: their file access goes through the CLI core's workspace file
  layer. `bash` has the shell tool's 120-second default timeout
  (`pi.bash_timeout_seconds`) and its 600-second maximum, and a command that
  ran is a result with its exit status. `grep` and `find` walk the workspace
  themselves, without `rg` or `fd`; `grep` skips files over 1 MiB, and each
  call of either has a 10-second budget. Each model call adds the project's
  `AGENTS.md` files unless `pi.load_project_context` is `false`: a file loads
  only directly in its own directory, and above the workspace the file, and a
  link to it, must belong to you or root. See
  [cli/typescript/pi](cli/typescript/pi/README.md).
- TypeScript CLI core: **WorkspaceFiles**, the workspace file layer of the
  local file tools, is exported for harnesses that bring their own file tools,
  with new methods **readFileWithVersion** (with an optional smaller
  **maximumBytes**), **replaceFileIfUnchanged**, **statExisting**, and
  **readDirectoryEntries**. A refusal throws the new
  **WorkspaceFileRefusedError**, a `ToolExecutionError` thrown before anything
  changed. See
  [cli/typescript/core](cli/typescript/core/README.md#the-harness-interface).
- TypeScript CLI core: `read_file`, `edit_file`, and `search_files` open
  files without blocking, so a FIFO in the workspace fails at once as not a
  regular file, or is skipped by a search, instead of hanging the call, as in
  the Python CLI core.
- Python commands for the other harnesses, first published in 0.5.0, on the
  shared CLI core: **dex-agents-openai-sdk** (OpenAI Agents SDK),
  **dex-agents-pydantic-ai** (Pydantic AI), **dex-agents-deepagents** (Deep
  Agents on the LangGraph adapter), **dex-agents-google-adk** (Google ADK), and
  **dex-agents-builtin** (the Dex built-in agent loop on LiteLLM). Each uses the
  CLI core's local and connector tools, so every tool call is its own Dex tool
  Step with the CLI core's policy, and each model call uses its turn's model and
  instructions. Provider keys come only from each provider's standard
  environment variables. The configuration file's `langgraph` section is now
  `deepagents`. See [cli/python](cli/python).
- The TypeScript **dex-agents-openai-sdk**, **dex-agents-vercel-ai**,
  **dex-agents-mastra**, and **dex-agents-builtin** commands
  (`@superdurable/dex-agents-<name>-cli`), first published in 0.5.0: an OpenAI Agents
  SDK, Vercel AI SDK, Mastra, or built-in loop agent in the terminal on the shared
  CLI core, with its connector and local tools. Each turn uses the model and
  instructions its session settings resolve to. Provider keys come from each
  provider's standard environment variables, such as `OPENAI_API_KEY`. See
  [cli/typescript/openai-sdk](cli/typescript/openai-sdk/README.md),
  [vercel-ai](cli/typescript/vercel-ai/README.md),
  [mastra](cli/typescript/mastra/README.md), and
  [builtin](cli/typescript/builtin/README.md).
- TypeScript CLI core: a harness whose runner fixes its tools when the Flow
  is built sets **AgentHarness.isToolSetFixedAtBuild**. The CLI core then lists
  the file's connector tools before building the Flow and, when the connectors
  MCP server does not answer at startup, says that a restart brings them back.
  See [cli/typescript/core](cli/typescript/core/README.md#connectors).
- Python and TypeScript Claude Agent SDK adapters: **claude_code_tool_permission**
  (`claudeCodeToolPermission`) decides each call of a tool that runs inside the
  Claude Code CLI from its input. It returns a **ToolPolicy** for that call,
  **ClaudeCodeToolDenial** to refuse it, or nothing for the session's policies,
  so `Bash` can run allow-listed commands without approval. See
  [Per-call decisions for Claude Code tools](python/claude-agent-sdk/README.md#per-call-decisions-for-claude-code-tools).
- New npm package `@superdurable/dex-agents-pi`, first published in 0.5.0, runs
  Pi agents on the built-in agent loop's session Flow. Dex runs the agent loop; pi-ai makes
  each model call and Pi tools run as Dex tool Steps. It peer-depends on
  `@earendil-works/pi-ai@^1.5.1`, tested with 1.5.1.
  **createPiAiModelClient(model, { apiKey, headers, prepareRequest })** makes
  one pi-ai request per `RunModel` attempt, stores the replay fields of pi-ai's
  assistant message with the pi-ai version, and replays them to every later
  model call.
  **piAgentFunctionTool(tool, { effect, requiresApproval })** wraps a Pi
  `AgentTool`; without an effect, `replay: "safe"` gives `idempotent_write`,
  `replay: "never"` gives `non_idempotent_write`, and a tool with neither
  fails when the definition is built. **piDurableFunctionTool(tool, { effect })**
  wraps a `pi-durable` tool that does not use the Harness `api`. See
  [typescript/pi](typescript/pi/README.md).
- Python, TypeScript, and Go cores: **AgentSessionStartInput** has an optional
  `start_environment` (`startEnvironment`, `StartEnvironment`), string keys and
  values that say where the program that started the session ran. The session
  never reads it; it stays in the Flow's start input. Python
  `AgentSessionClient.start_session` and TypeScript `startAgentSession` take
  it. Workers before 0.5.0 ignore it. The CLI cores record their host name,
  user ID and user name, configuration identity, and workspace there.
- Python and TypeScript cores: new **ToolCallContext.effect**, the effect of a
  built-in agent loop function tool call after the tool policies are applied.
  It is the effect that chose `CallTool` or `CallSingleAttemptTool`, so a wrapper that
  turns errors into `ToolExecutionError` can let errors of a
  `non_idempotent_write` or `unclassified` call propagate and the session
  records `outcome_unknown`. The runner always sets it. A context built by
  hand has no effect: the field is optional in TypeScript, so existing
  contexts still compile, and defaults to `UNCLASSIFIED` in Python. Treat
  either as an unknown effect and handle the call as a write.
- Python and TypeScript cores: built-in agent loop function tools take an
  optional **prepare_arguments** (`prepareArguments`) that converts the model's decoded
  arguments before the schema check, such as an argument shape an earlier
  version of the tool took. Its result is checked and passed to the tool. An
  error it raises fails the call like a schema violation:
  `invalid arguments for <tool>: <message>`; a cancellation passes through
  unchanged. It must be synchronous and deterministic: an awaitable result
  fails the call with `argument conversion must be synchronous`, and the
  TypeScript type **PreparedToolArguments** refuses a Promise. The stored tool
  call and the approval interrupt keep the model's original arguments.
- Python, TypeScript, and Go cores: **ToolExecutionResult** and **AgentToolCall**
  have an optional `runner_tool_state` (`runnerToolState`, `RunnerToolState`):
  opaque text that a runner returns with a tool's result for its own later
  model calls, such as the framework state the tool changed. The session stores
  it on the `succeeded` record, apart from the output, and returns it to the
  runner in the turn's tool call records. It never reaches the model, the
  browser snapshot (`agent_snapshot_to_json`, `agentSnapshotToJson`), or trace
  attributes. Only the attempt that commits the result stores it. A record
  written before 0.5.0 reads it as absent, and a Worker before 0.5.0 ignores
  it. `agents/conformance` has the field and a `released-0.4.0` tool call
  case.
- Python LangGraph adapter: Deep Agents agents from **create_deep_agent** run
  as agent sessions. A **DeltaChannel** in the root graph is accepted, and each
  turn-local checkpoint stores its whole value. A tool with a **ToolRuntime**
  or **InjectedState** parameter receives the graph state of its deferral in
  its tool Step, without the message list, and reads and writes state as in a
  graph task, as Deep Agents' **StateBackend** file tools do. A tool may return
  a **Command** with an `update` mapping. The tool's state writes travel in
  the result's **runner_tool_state**, and the next `RunModel` replays them
  once. A replayed tool result has the fixed ID
  `dex-tool-result-<turn_id>-<tool_call_id>`. New session Flow and
  **LangGraphAgentRunner** options:
  **excluded_tool_names** removes graph tools from model requests and tool
  Steps, such as `task` to turn off subagents, and frees their names for tool
  sources; **select_turn_chat_model** picks each turn's chat model from its
  **AgentTurnContext**; **should_defer_tool_calls_before_middleware** makes the
  deferral the outermost tool wrapper on the runner's own copy of the graph,
  which `create_deep_agent`'s middleware order otherwise prevents. Existing
  graphs behave as before, except for the fixed ID of a replayed tool result. See
  [A Deep Agents agent](python/langgraph/README.md#a-deep-agents-agent).
- The **dex-agents-deepagents** command uses these options instead of its own
  state schema, subagent middleware replacement, and model middleware. Its
  behavior is unchanged.
- `@superdurable/dex-agents-pi`: a Pi tool's `prepareArguments` becomes the
  function tool's **prepareArguments**, so the built-in loop converts an old
  argument shape before the schema check. A plain error a Pi tool throws
  follows the call's **ToolCallContext.effect**, the effect after
  `toolPolicies`: it fails the call for `read` and `idempotent_write`, and
  records `outcome_unknown` for `non_idempotent_write`, `unclassified`, or a
  context without an effect. See [typescript/pi](typescript/pi/README.md).
- `@superdurable/dex-agents-pi`: **createPiAiModelClient** sends the images of
  built-in loop user text and tool result items as pi-ai image content, with
  the base64 of their bytes from **ModelRequest.imageBytes**, after the item's
  text. A request without an image's bytes throws a **TypeError** before pi-ai
  sends it. A model whose `input` lacks `"image"` gets pi-ai's placeholder
  text instead. New **piAiEstimatedImageInputTokens** (1600), which
  `estimateInputTokens` counts per image. With a media store, the image parts
  of a Pi tool result from **piAgentFunctionTool** or **piDurableFunctionTool**
  are uploaded during the call and returned with **functionToolResult**, so the
  model sees them after the text. Without a store, and for an image the store
  refuses, each image still becomes `[image omitted: <mime type>]`. See
  [typescript/pi](typescript/pi/README.md).

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
- **LocalDirectoryMediaStore** (`dex_agents`; `@superdurable/dex-agents`)
  keeps images in a local directory when every client and Worker runs on one
  machine, such as a command-line tool in local mode. An image's file is
  `<root>/sha256/<sha256>`, and its `uri` is `file://<root>/sha256/<sha256>`
  with the absolute root percent-encoded, the same in every core. Writes go
  through a temporary file and a rename, an existing file with the same bytes
  is success, new directories and files get modes `0700` and `0600`, and
  every put syncs the `sha256` directory, the root, and the root's parent
  before it returns. It refuses symbolic links, special
  files, and files with more than one hard link under the root, and checks the
  SHA-256, size, and type on read. Python opens every path relative to an open
  directory; TypeScript, without `openat`, checks the directory and the file
  before and after each operation, which a same-user process that can write
  the root can still race. It needs a POSIX file system.
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
  (`buildUserMessageWithImages`). The built-in loop implements it when its
  session Flow has a media store; see below.
- Framework adapters show images to their model when built with a media
  store: a new optional **media_store** (`mediaStore`) on each adapter's
  runner and session-flow factory. With a store, the runner accepts images,
  keeps only references in its stored items and run state, and loads the
  bytes right before each model call, so a replay sends the same input.
  A user image becomes the framework's image part, and a tool result's
  images reach the model in the framework's tool-result form. Without a
  store the adapter refuses images, as before. See each adapter README,
  "Images".
- Python and TypeScript cores: **McpToolSource** takes an optional
  **media_store** (`mediaStore`). With one, the `image` content of a
  successful result is uploaded inside the tool Step and returned in
  `ToolExecutionResult.images`, and the output text shows `[image <n>]` in
  its place. More than 20 images, invalid base64, or bytes that are not a
  supported image fail the call. Base64 must be canonical: padded, without
  line breaks or unused trailing bits. Without a store, the image content of
  a successful result stays JSON text, as before.
- Both cores: a failed MCP result (`isError`, or the Dex outcome `rejected`
  or `unknown`) shows `[image <n> omitted]` for each image block in its
  text, with or without a media store, so the recorded error holds no image
  bytes. It used to hold each image block as JSON with its base64. Cases: `mcp_image_content` in
  `agents/conformance/images.json`.
- Runners read bytes with **load_images** (`loadImages`) and may choose them
  with **collect_turn_images** (`collectTurnImages`).
- Go core: no image input. Its values carry `images`, so a value written by
  another core round-trips, and `send_user_message` returns `false` for a
  message with images.
- Shared cases: `agents/conformance/images.json`.
- Agents CLI, Python and TypeScript: a new configuration section **media**.
  `media.store` is `auto`, a **LocalDirectoryMediaStore** at
  `~/.dex/agents/media`, or `s3://<bucket>[/<prefix>]`, an **S3MediaStore**
  with an optional `media.endpoint` for an S3-compatible service. Credentials
  come from the environment or the standard AWS credential chain, never from
  the file. Session settings cannot change the store.
  In the chat, **/image <path>** copies an image file into the store and
  attaches it to the next message; a harness whose runner takes no images
  says the message was not sent. **HarnessContext.media_store**
  (`mediaStore`) hands the store to the harness, and connector tool images go
  to the same store. The Claude Agent SDK, OpenAI Agents SDK, Pydantic AI,
  Deep Agents, and Google ADK commands in Python and the Claude Agent SDK,
  OpenAI Agents SDK, Vercel AI SDK, and Mastra commands in TypeScript pass the
  store to their adapter, and the built-in commands in both languages pass it
  to the built-in loop, so their models see the images. See
  [Images](cli/README.md#images).
- Agents CLI: the TypeScript **dex-agents-pi** command passes the media store
  to the built-in loop, so images attached with **/image** reach its model.
  Images that its Pi tools return, such as `read` of a PNG, JPEG, GIF, or
  WebP file in the workspace, are uploaded to the same store in the tool Step
  and reach the next model call after the result's text, at most 20 per
  result. A refused or excess image becomes `[image omitted: <mime type>]`
  and the call still succeeds. Before, `read` gave the model only that note.
  See [cli/typescript/pi](cli/typescript/pi/README.md#images).
- Built-in agent loop, Python and TypeScript: function tools return images.
  A function tool returns the new **FunctionToolResult** with `output` and
  `images` (`functionToolResult({ output, images })` in TypeScript), and the new
  **ToolCallContext.media_store** (`mediaStore`) gives it the runner's media
  store, or none when the runner has none, to upload the bytes with
  `put_image` (`putImage`). The runner returns the images in
  **ToolExecutionResult.images**, so the session checks, stores, and shows
  them exactly like a tool source's images. Images from a runner without a
  media store fail the call once, without a retry, and the model sees the
  error. Strings and other JSON results are
  unchanged. See the core README, "Images from function tools".
- Built-in agent loop, Python and TypeScript: images reach the model when the
  session Flow has a media store, a new optional **media_store** on
  **builtin_agent_session_flow** and **BuiltinLoopAgentRunner**
  (`mediaStore` in the new third argument of `createBuiltinAgentSessionFlow`,
  **BuiltinAgentSessionFlowOptions**, and the second argument of
  `BuiltinLoopAgentRunner`). With a store, the runner implements
  **ImageUserMessageBuilder**, its stored `user_text` and `tool_result` items
  keep image references in a new `images` list (written only when non-empty,
  so other items are unchanged), and it reads the bytes right before each
  model call into the new **ModelRequest.image_bytes** (`imageBytes`). New
  **UserTextItem.images** and **ToolResultItem.images**
  (`ModelUserTextItem.images`, `ModelToolResultItem.images`). Images of an
  **McpToolSource** built with the same store reach the model too. A store
  read failure fails that `RunModel` attempt, which retries. Context summary
  calls see text only. Without a store the session refuses images, as before,
  and a tool result's references are stored but not shown to the model. See
  the core README, "Images on the built-in loop".
- **LiteLLMModelClient** sends images as base64 `data:` URLs: `input_image`
  parts in Responses API messages and `function_call_output` outputs;
  `image_url` parts in Chat Completions user messages, and in tool messages
  for Anthropic and Bedrock. Other Chat Completions providers, such as
  OpenAI, get the tool result's images in a user message after the run of
  tool messages, with **TOOL_RESULT_IMAGES_TEXT**. Known limitation: Gemini
  models on the Gemini API and Vertex AI get user message images, but not a
  tool result's images, on either API. Their tool result ends with
  **TOOL_RESULT_IMAGES_OMITTED_TEXT** instead.
  `estimate_input_tokens` counts **ESTIMATED_IMAGE_INPUT_TOKENS** per image
  sent.
- **createAiSdkModelClient** sends images as AI SDK `file` parts in user
  messages and in tool result `content` output. `estimateInputTokens` counts
  **estimatedImageInputTokens** per image.

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
  - The framework adapters, the Pi adapter, and the agents CLI core import
    the new names.
- The error text for a session message that the built-in agent loop cannot
  read now says "built-in agent loop item" (Python and Go cores) or "built-in
  loop item" (TypeScript core) instead of "Stepwise item", and the
  error for a framework run state it cannot read says "built-in loop" instead
  of "Stepwise" (Go and TypeScript cores). The
  Python loggers are now named `dex_agents.builtin_loop.*`, and the Go core's
  panic messages for a nil model client or definition start with
  `builtinloop:` instead of `stepwise:`.
- Breaking, Python and TypeScript CLI cores: the session Flow type suffix is
  now 6 lowercase base36 characters instead of 12 hex digits, from the same
  SHA-256 of the host name, user, configuration identity, and workspace: the
  digest's first 8 bytes as an unsigned big-endian integer, modulo 36^6, in
  base36, left-padded with `0`. Every CLI Flow type changes, so the new CLI
  cannot resume or list sessions started under the old 12-hex names. The owner
  authorized this break before launch. New Python `namespace_session_flow_type`,
  `current_session_environment_identity`, and `SessionEnvironmentIdentity`
  match the TypeScript `namespaceSessionFlowType`.
  `agents/conformance/cli_session_flow_types.json` fixes the suffix for every
  CLI core.
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

- Python and TypeScript Claude Agent SDK adapters: the stored transcript no
  longer keeps Claude Code's `last-prompt` entries. Such an entry names the
  leaf a resume continues from, so after a deferred call a later segment could
  resume at an earlier message: the model lost the previous answer, and a
  resumed segment lost the turn's prompt and its tool call. Entries that 0.4.0
  already stored, in messages or in a suspended turn's run state, are dropped
  when the next segment reads them, so existing sessions recover too.
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
