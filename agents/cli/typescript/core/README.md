# @superdurable/dex-agents-cli-core

The shared CLI core of the TypeScript Dex Agents CLI commands. Each harness
command, such as `dex-agents-claude-sdk`, is a thin package that implements
one `AgentHarness` and calls `runAgentCli`. This package does the rest:

- reads `~/.dex/agents/<harness>.yaml`, the format in
  [`../../README.md`](../../README.md), and creates it on first run;
- reuses or starts a local `dexcli dev`, downloading `dexcli` when needed;
- connects the connectors MCP server and applies `connectors.enabled`;
- offers the local tools: files, shell, web fetch, and a browser;
- runs the Worker in the command's process and chats in the terminal.

The Python CLI core in `agents/cli/python/core` has the same behavior and
the same concepts.

## Commands

```text
dex-agents-<harness> [run|init|connectors|sessions] [options]

  run          Chat with the agent in this terminal (the default).
  init         Create or replace the configuration file.
  connectors   List the authorized connections and the configured ones.
  sessions     List this command's sessions.

  -c, --config <file>   Configuration file (default ~/.dex/agents/<harness>.yaml).
  -y, --yes             Write and use the defaults instead of asking.
  -r, --resume <id>     Continue a session.
  -m, --model <id>      Override the configured model.
      --plain           Chat in plain text, without the terminal UI.
```

Without a configuration file, `run` asks the `init` questions at a terminal
and writes the file. With `--yes` it writes the defaults without asking.
Without a terminal it runs on the defaults, writes nothing, and says to run
`init`.

In the chat, each line is a user message. The reply and tool calls stream as
they happen. A tool call that needs approval shows its name and arguments and
asks `[y/n, or n <reason>]`. A question from the agent asks for an answer, or
a choice number. Ctrl-C cancels the running turn, and a second Ctrl-C exits.
`/exit` and the end of input also exit. Exiting never ends the session; the
command prints the `--resume` line that continues it. The command exits with 1
when a turn or the session failed during the chat, 2 for invalid arguments or
configuration, and 0 otherwise.

`/image <path>` attaches an image file to the next message. A leading `~` is
the home directory, and a relative path is relative to the current directory.
The command refuses a missing or special file, a file over `maxImageBytes`
(3,750,000 bytes, checked before it reads the file), and bytes that are not
PNG, JPEG, GIF, or WebP, and says why. A valid image is copied into the media
store, which prints `Attached chart.png (image/png, 48 KB)`, and goes with the
next message. A message carries at most `maxImagesPerMessage` (20) images.
When the harness's runner does not implement `buildUserMessageWithImages`,
the message is not sent: the command says the agent does not take images and
removes the attached images.

The media store comes from `media.store`, built once when `run` starts: `auto`
is a `LocalDirectoryMediaStore` at `~/.dex/agents/media`, and
`s3://<bucket>[/<prefix>]` is an `S3MediaStore` from
`@superdurable/dex-agents/s3`, with path-style addressing when
`media.endpoint` is set. Its credentials come from the environment or the
standard AWS chain. The S3 store loads only for an `s3://` store and needs
the optional `@aws-sdk/client-s3` peer; without it, or with a store the core
refuses, the command exits with 2. Connector tool result images go to the same
store.

### Sessions belong to one execution environment

The CLI core registers the session Flow type `<flowType>-<suffix>`. The suffix
is 6 lowercase base36 characters (`0-9a-z`). Take the SHA-256 of
`<host name>\n<user>\n<configuration identity>\n<workspace>` in UTF-8:

- the user is the numeric user ID, or the user name where the platform has
  none;
- the configuration identity is the real path of the configuration file, or
  `defaults` without a file;
- the workspace is its canonical real path.

Read the digest's first 8 bytes as an unsigned big-endian integer, reduce it
modulo 36^6, write it in base36, and left-pad it with `0` to 6 characters.
`namespaceSessionFlowType` computes it, and every CLI core passes the cases
in
[`agents/conformance/cli_session_flow_types.json`](../../../conformance/cli_session_flow_types.json).

Each new session records these values in its start input,
`AgentSessionStartInput.startEnvironment`, so a run shows where it was
started: `host_name`, `user_id`, `config_identity`, and `workspace` are the
hashed values, and `user_name` is the user's name, left out when the system
cannot tell it. They are not session settings and cannot change.

A Worker takes only the Steps of its own Flow type, so an approved write or
shell command always runs as the same user, with the same configuration file
and workspace it was approved under. Tool settings, such as the shell allow
list, are not part of the suffix, so changing them keeps `--resume` working.
Changing the workspace, the file, or the user starts a new type: `--resume`
of an older session then exits with 2 and says to resume it in its original
environment, and `sessions` lists only the current environment's sessions.
Two processes in the same environment share their sessions' Steps. Because
tool settings may differ between them, the Worker that runs a tool Step
checks the call again right before it acts, with its own file and the
settings the call's turn started with. It refuses the call, which then
records `failed` without running, when the tool's group or connector
connection or operation is off there, or when a call that had no approval is
no longer allowed without one: a shell command missing from its allow list,
or a tool now in `approval.always`.

## The harness interface

A harness package exports one `AgentHarness`. Its bin calls `runAgentCli` and
exits with its result, because open connections can keep Node running.

```ts
#!/usr/bin/env node
import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
import { runAgentCli, type AgentHarness } from "@superdurable/dex-agents-cli-core";

const harness: AgentHarness = {
  name: "builtin", // command dex-agents-builtin, file ~/.dex/agents/builtin.yaml
  configSection: "builtin", // this harness's section of the file
  flowType: "DexAgentsBuiltinCliSession",
  defaultModel: "openai/gpt-5-mini",
  replacedLocalTools: [], // for example ["files", "shell", "web_fetch"] for Claude Code
  createSessionFlow: (context) =>
    createBuiltinAgentSessionFlow(
      {
        name: "assistant",
        instructions: (turn) =>
          context.resolveTurn(turn.sessionSettings ?? {}).instructions ?? "You are a helpful assistant.",
        model: modelFor(context.config.model),
        resolveModel: (settings) => modelFor(context.resolveTurn(settings).model),
        toolSources: context.toolSources,
        builtInTools: ["ask_user"],
      },
      context.flowType,
    ),
};

process.exit(await runAgentCli(harness));
```

```ts
interface AgentHarness {
  readonly name: string;
  readonly configSection: string;
  readonly flowType: string;
  readonly defaultModel: string | null;
  readonly replacedLocalTools?: readonly LocalToolGroup[];
  readonly defaultWorkspaceLocation?: "dex_agent_workspace" | "launch_directory"; // TypeScript only
  createSessionFlow(context: HarnessContext): AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
}

interface HarnessContext {
  readonly flowType: string; // <AgentHarness.flowType>-<hash>; build the Flow with it
  readonly config: EffectiveConfig; // the file, defaults filled in
  readonly workspace: string; // absolute, exists
  readonly instructions: string | null; // system_prompt or system_prompt_file text
  readonly harnessOptions: Readonly<Record<string, unknown>>;
  readonly toolSources: readonly ToolSource[]; // connectors, then local tools
  readonly mediaStore?: MediaStore; // media.store; the CLI core always sets it
  resolveTurn(sessionSettings: SessionSettings): ResolvedTurn; // model, instructions, narrowing
  currentTurn(): ResolvedTurn; // the running model call's turn, else the file
  resolveToolStepPermissions(step: { flowId: string; turnId: string }): Promise<ResolvedTurn>; // in a tool Step
}

type LocalToolGroup = "files" | "shell" | "web_fetch" | "browser";
```

- A built-in loop harness puts `context.toolSources` in
  `AgentDefinition.toolSources`. A runner-based adapter, such as the Claude
  Agent SDK, OpenAI Agents, Vercel AI, or Mastra adapter, passes them to its
  runner's `toolSources` and builds the Flow with `createAgentSessionFlow`.
- `context.mediaStore` holds the images of `/image` and of connector tool
  results. Pass it to an adapter that takes a media store, such as
  `mediaStore: context.mediaStore`, so its runner reads them from the same
  store. The CLI core keeps the runner's own methods, such as
  `buildUserMessageWithImages`.
- Each tool source applies the turn's session settings itself:
  `connectors.enabled`, `local_tools`, the shell allow list, and
  `approval.always`. A harness reads only the model and the instructions,
  with `resolveTurn` in each model call. Rejected settings throw there, so
  the turn fails with the error code and JSON Pointer.
- `defaultWorkspaceLocation: "launch_directory"` makes the directory the
  command starts in the workspace when the file sets none, as Pi does, and
  `init` then writes no `workspace`. The default is `~/dex-agent-workspace`.
  The Python CLI core has no such option.
- `replacedLocalTools` lists the groups the harness serves with its own
  tools. The CLI core leaves them out of `context.toolSources`. The harness
  gives its own tools the same rules, with these exports:
  - `isLocalToolGroupEnabled(turn, group)`: whether the turn switches the
    group on, from `local_tools` and the session's narrowing;
  - `isShellCommandApprovalRequired(turn, command)`: whether a command waits
    for approval, which is unless the allow list has its exact text;
  - `resolveTurnToolPolicy(turnContext, toolName, effect, requiresApproval, turn)`:
    the call's policy, with the tool's effect and approval as floors and the
    turn's `approval.always`;
  - `context.currentTurn()`: the running turn's configuration, for callbacks
    that run during the model call;
  - `context.resolveToolStepPermissions({ flowId, turnId })`: the permissions
    inside the harness's own tool Step, which runs outside the model call and
    maybe on another Worker: this Worker's file narrowed by the settings the
    turn started with. Check them right before acting;
  - `WorkspaceFiles`: the workspace file layer of the local file tools, with
    the rules under Local tools. A harness's own file tools read, list, and
    write through it, including `readFileWithVersion`,
    `replaceFileIfUnchanged`, `statExisting`, and `readDirectoryEntries` for
    tools that need bytes, versions, stats, or raw entries. A refusal throws
    `WorkspaceFileRefusedError` before anything changed; any other failure of
    a write may come after a change, such as after its rename.

  Reads run at once, writes and shell commands wait for approval, and the
  working directory is `context.workspace`. A replacement shell tool, for
  example:

  ```ts
  const replacementShell = functionTool<{ command: string }>({
    name: "shell",
    description: "Run a shell command in the workspace.",
    parameters: { type: "object", properties: { command: { type: "string" } }, required: ["command"] },
    effect: "non_idempotent_write",
    execute: async ({ command }, call) => {
      // call is the built-in loop ToolCallContext, with flowId and turnId.
      const permissions = await context.resolveToolStepPermissions(call);
      if (!isLocalToolGroupEnabled(permissions, "shell")) {
        throw new ToolExecutionError("shell commands are switched off; the command did not run");
      }
      return runInWorkspace(command, context.workspace);
    },
  });
  ```
- The CLI core wraps the Flow's runner so that each model call runs with its
  turn's configuration in scope. The harness does not need to do anything for
  that.

The Python CLI core uses the same names in snake case:

| TypeScript | Python |
| --- | --- |
| `AgentHarness` | `AgentHarness` (Protocol) |
| `name`, `configSection`, `flowType`, `defaultModel`, `replacedLocalTools` | `name`, `config_section`, `flow_type`, `default_model`, `replaced_local_tools` |
| `createSessionFlow(context)` | `create_session_flow(context)` |
| `HarnessContext` with `flowType`, `config`, `workspace`, `instructions`, `harnessOptions`, `toolSources`, `mediaStore`, `resolveTurn`, `currentTurn` | `HarnessContext` with the same fields in snake case; `mediaStore` and `media_store` are absent only in a context a test builds |
| `LocalToolGroup` | `LocalToolGroup` (`files`, `shell`, `web_fetch`, `browser`) |
| `terminalInterface` (`"dexcli"` or `"plain"`) | `terminal_interface` |
| `runAgentCli(harness)` | `run_agent_cli(harness)` |
| `isLocalToolGroupEnabled`, `isShellCommandApprovalRequired`, `resolveTurnToolPolicy` | the same names in snake case |
| `HarnessContext.resolveToolStepPermissions(step)` | `HarnessContext.resolve_tool_step_permissions(step)` |

## Local tools

Each call is one Dex tool Step, recorded in the session.

| Group | Tool | Effect | Approval |
| --- | --- | --- | --- |
| `files` | `read_file`, `list_directory`, `search_files` | read | none |
| `files` | `write_file` | idempotent write | every call |
| `files` | `edit_file` | single-attempt write | every call |
| `shell` | `run_shell_command` | single-attempt write | unless the allow list has the whole command text |
| `web_fetch` | `fetch_web_page` | read | none |
| `browser` | `browser_navigate`, `browser_snapshot` | read | none |
| `browser` | `browser_click`, `browser_fill`, `browser_submit_form` | single-attempt write | every call |

- File paths are relative to `workspace`, or absolute inside it, compared
  after the file system resolves links and case. A path that leaves it,
  through `..`, an absolute path, or a symbolic link, fails. Files open
  without following a final link, and the opened file must still be the one
  the path names, so a link swapped in meanwhile fails. A file with more than
  one hard link is refused. Searches check every file they open the same way.
  Reads and searches open files without blocking, so a FIFO or device in
  the workspace fails at once as not a regular file, and a search skips it.
  `write_file` and `edit_file` write a new file in the checked directory and
  rename it over the target, and never write through a link. After the
  rename, the target must be the written file inside the workspace.
- Residual write race: Node has no `openat` or `renameat`, so the CLI core
  cannot pin the checked directory while it writes. A concurrent process that
  swaps a directory of the path for a link during an approved write can make
  the temporary file or the renamed file land outside the workspace. The
  CLI core then reports the call as failed, says the content may be outside,
  and deletes the written file where its known names still find it. This is
  not a sandbox: an approved shell command, or any other program, can change
  the workspace while a call runs.
- `edit_file` takes `path`, `old_text`, and `new_text`. `old_text` must occur
  exactly once in the file. When the file changed after `edit_file` read it,
  by identity, size, modification time, or content, the call fails with a
  conflict and asks to read the file again. That check runs right before the
  rename, not atomically with it: a change made in the short window between
  the check and the rename is still replaced.
- Shell commands run with the system shell in `workspace`, in their own
  process group, with a 120-second default timeout. A command whose Worker
  died while it ran records `outcome_unknown` and never runs again.
- Browser tools return the page title, URL, and accessibility snapshot as
  text; they take no screenshots. The browser is headless Chromium:
  Playwright's own when installed, else the system Chrome, else Playwright
  downloads Chromium on first use. Each session has one page in the Worker's
  memory.
- `approval.always` adds approval to any tool by name.
- A local tool's effect and approval are floors. An explicit `ToolPolicy` for
  it may raise the effect, ranked as in `McpToolSource.mergeToolPolicies`.
  The call waits for approval when the tool asks for it, when the policy sets
  `requiresApproval: true`, or when the policy leaves `requiresApproval` out
  and the raised effect needs approval by default. `requiresApproval: false`
  cannot remove the tool's own approval.

## Connectors

`connectors.mcp: auto` uses the connectors MCP server that `dexcli dev`
serves at `<Dex Web URL>/mcp`. When the CLI core starts `dexcli dev`, it takes
the URL from the `Connectors MCP:` line of its output. When an `auto` server is
not available, such as one answering 503 while it starts, or an older
`dexcli` without `/mcp`, the command warns and continues; each model call
lists the server again, so its tools appear once it is ready. The terminal
shows one short line when connector tools become unavailable, naming the log
file `~/.dex/agents/<harness>.log` and the Dex Web Connectors page, and one
line when they are available again. Each failed listing and its error go to
the log file only. A server that answers HTTP 401 or 403 gets a line saying it
denied access, so check its access settings and `connectors.mcp`. For an
explicit `connectors.mcp` URL, only a failed `initialize` probe stops the
command. A server that answers `initialize` but cannot be listed only warns,
whichever URL names it.

A harness whose runner fixes its tools when the Flow is built declares
`isToolSetFixedAtBuild: true`. The CLI core then lists the connector tools the
file enables before it builds the Flow, so the connector source's
`cachedTools()` holds them. When the server does not answer at startup, or
that listing fails, one warning says the run has no connector tools and to
restart the command once the server answers. Every TypeScript adapter lists
its tool sources in each model call, so none of the TypeScript commands sets
it; the Python CLI core's `is_tool_set_fixed_at_build` is the same switch.

The agent sees each connector tool by its MCP name, such as
`slack_post_thread_reply`. `connectors.enabled` matches only these `_meta`
keys of each tool, never its name:

| Key | Value |
| --- | --- |
| `io.superdurable.connectors/connector` | Connector ID, such as `slack` |
| `io.superdurable.connectors/connection` | Connection name |
| `io.superdurable.connectors/operation` | operationId, such as `postThreadReply` |
| `io.superdurable.connectors/operationName` | Its snake-case name, such as `post_thread_reply` |
| `io.superdurable.connectors/connectorVersion` | The connector's version |

While the list is set, a tool without a connector and a connection is
hidden. An entry with `tools` also needs `operation` and `operationName`, and
matches a listed name against either.

A call records the connector, connection, and operation its tool name had
when the model requested it. Right before it runs, the CLI core lists the
server again and refuses the call, which records `failed` without sending
anything, when the name now has another identity, such as another connection.
The `tools/call` request also carries the recorded identity in `_meta` as
`io.superdurable.connectors/expectedConnector`, `expectedConnection`, and
`expectedOperation`. The client-side check only narrows the window between
the listing and the send; the connectors MCP server enforces the expected
identity atomically from the release that supports these keys.

The check that the connection and operation are enabled on the executing
Worker needs no listing and always runs, from the identity recorded in the
call. The listing check is skipped only when the core says the retry can
never send (`McpToolSource.willResumeToolTaskWithoutSending`): it resumes a
task that an earlier attempt started and the session polls, so it recovers
even while the catalog changed or is unavailable. A task polled inside the
tool Step, such as one bound to an MCP protocol session, may be called again
when the server lost it, so its retry lists the server first and fails closed
when the catalog is unavailable.

A configured connection that the server
does not list is reported as not authorized, and at a terminal the command
opens the Dex Web Connectors page.

## The Dex server and dexcli

`dex.server: auto` reuses a `dexcli dev` with Dex Web on port 8802 and Dex on
8801 only when `/readyz` answers with Dex Web's readiness JSON, checked by
value (`status` is `ready`, a non-empty `source`, a non-negative
`definitionCount`, and an entity tag equal to `definitionRevision`), and the
Dex FlowService answers on the Dex port; otherwise the command stops and
names the port. This check also runs for a server that a command started.
These are protocol checks, not authentication: a local process of another
user that binds the ports first and imitates both answers is trusted. Without
any server, it starts `dexcli dev` in the background on those ports. Its output goes to `~/.dex/agents/dexcli-dev.log`,
and its pid and ports to `~/.dex/agents/dexcli-dev.json`. It keeps running
after the command exits, so later commands and Dex Web reuse it.

The CLI core finds `dexcli` in this order: `DEXCLI`; an earlier download in
`~/.dex/bin/dexcli-<version>`, whose `dexcli` and `dex-server-zd` must still
match the SHA-256 digests recorded in its `install.json`; the first `dexcli`
on `PATH` of at least the pinned version; otherwise it downloads the pinned
release for the platform from GitHub and checks its SHA-256 against the
release's `checksums.txt`. Every download URL, and every redirect, must be
`https`, or `http` on a loopback host. It then writes only the archive's regular
`dexcli` and `dex-server-zd` files into a private staging directory and
renames it into place. An archive with a link, an absolute path, or a `..`
entry is refused.

| Variable | Meaning |
| --- | --- |
| `DEXCLI` | The `dexcli` executable to use. |
| `DEX_AGENTS_DEX_PORT`, `DEX_AGENTS_WEB_PORT` | The ports of the `dexcli dev` that `auto` reuses or starts. |
| `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` | The base URL of `dexcli` release downloads, such as a mirror. It must be `https`, or `http` on 127.0.0.1, localhost, or [::1]. |
| `DEX_AGENTS_OPEN_BROWSER` | `0` or `false` keeps the command from opening a web browser, such as the Dex Web Connectors page at a terminal. |
| `DEX_AGENTS_TERMINAL_UI` | Internal: `1` makes the chat try the `dexcli` terminal UI. See [The terminal UI](#the-terminal-ui-internal). |

## The terminal UI (internal)

The chat can run in `dexcli agents-tui`, a terminal UI that `dexcli` draws.
It is off until it ships: only `DEX_AGENTS_TERMINAL_UI=1` turns it on, and
without it every command behaves as described above. `--plain` keeps the text
chat even then. The handoff follows
[terminal UI contract 1](../../TERMINAL_UI_CONTRACT.md); this section covers
the launcher side that this core implements. Only the chat (`run`) hands off
for now; `sessions` and `connectors` stay text.

A run tries the terminal UI only when all of these hold, checked in this
order before anything else: the switch is on, the platform is not Windows,
`--plain` is absent, stdin and stdout are terminals, `TERM` is not `dumb`, and
the harness does not declare `terminalInterface: "plain"` (Pi does). The
command then resolves `dexcli` as for `dexcli dev` and runs
`dexcli agents-tui --describe` with an empty environment and a 5 s limit. When
that fails, or no common contract has the chat mode, it prints one
`Plain chat: <reason>.` line and runs the text chat in its own process, as
before.

Otherwise the command becomes a thin **launcher** with two children:

| Process | stdin | stdout and stderr | Process group | Environment |
| --- | --- | --- | --- | --- |
| launcher (the command) | the terminal, never read during the handoff | the terminal; its own JavaScript writes go to the log during the handoff | foreground | the user's |
| Worker host: the same bin with the hidden `__worker-host` argument | `/dev/null` | `~/.dex/agents/<harness>.log` | its own (`detached`), which it leads | the user's, with the provider keys |
| `dexcli agents-tui --channel-fd 3` | the terminal | the terminal | foreground | the contract's allow-list only |

- The launcher sends the Worker host the effective configuration, the
  resolved Dex server, and the `--resume` ID over Node's IPC channel. That
  channel is internal to one core version (`src/worker-host/worker-host-protocol.ts`).
  The Worker host opens the session exactly as the text chat does, then
  reports the session, its Flow type, and the capabilities of the Flow it
  built: `images` only when the runner implements
  `buildUserMessageWithImages`, `plan_mode` from the Flow's options, and
  `turn_control`. A startup failure, such as a `--resume` of another
  environment's session, exits with the code the text chat would use.
- Its log lines before the handoff print on the terminal, as today. After
  `ready` they reach the terminal UI as `notice` messages, cut to 500 code
  points.
- The launcher saves the terminal mode with `stty -g`, starts `dexcli` with
  the channel on fd 3, and writes the launch document. `dexcli` must send
  `ready` within 5 s of its spawn. Before `ready`, any failure (`dexcli`
  exits or cannot start, `launch_rejected`, the channel ends or breaks, or the
  timeout) stops `dexcli` if it still runs (1 s to exit on its own, then
  SIGTERM, then SIGKILL 2 s later), restores the terminal, prints
  `Plain chat: <reason>.`, stops the Worker host, and continues in the text
  chat with `--resume` of the same session.
- After `ready` the launcher relays `put_image`, `start_session`, and
  `list_connectors` to the Worker host and serves `open_url` itself.
  `src/terminal-ui/local-request-relay.ts` checks each request against the
  channel schema, answers `busy` beyond 4 in flight, remembers each key's
  outcome (`request-outcome-table.ts`), answers `get_request_result`, and
  answers `cancel` at once. `start_session` starts one of the two session IDs
  the key derives, with the key as the start request ID and "ignore already
  started": the 12-digit one when it exists and this key started it, else the
  6-digit one, else the 12-digit one. So a resent key finds its own session.
  `open_url` answers `browser_off` when `DEX_AGENTS_OPEN_BROWSER` is off, and
  `open_failed` when the platform's opener (`open` or `xdg-open`) fails.
  `host_stopped` is never remembered as a key's outcome.
- When the Worker host exits, every waiting and later relayed request answers
  `host_stopped`, and the terminal UI gets `worker` `stopped` with the exit
  status.
- When `dexcli` exits, the launcher restores the saved terminal mode; after a
  crash or a kill it also leaves the alternate screen, turns off bracketed
  paste, and shows the cursor, then prints
  `The chat closed unexpectedly (dexcli exit N).` A channel that ends or
  breaks after `ready` also ends the chat: `dexcli` gets 1 s to exit, then
  SIGTERM, and the line names the cause. It prints the `--resume` line and
  stops the Worker host: `shutdown`, up to 10 s, then SIGTERM to its group and
  SIGKILL 2 s later.

| Exit | When |
| --- | --- |
| 0 | `dexcli` exited 0 and the Worker host kept running. |
| 1 | `dexcli` exited 1, crashed, or was killed after `ready`, or the Worker host stopped during the chat. |
| 2 | The Worker host could not open the session, as for the text chat. |
| 130 | An external SIGINT, to the foreground group or to the launcher alone. |
| 143 | SIGTERM to the launcher: it sends SIGTERM to `dexcli`, SIGKILL after 2 s. |
| from the text chat | A failure before `ready`. |

Signals while `dexcli` runs: the launcher keeps a SIGINT handler (never
`SIG_IGN`, which `dexcli` would inherit). A SIGINT to the foreground group
reaches `dexcli` too, so the handler gives it 1 s to exit on its own; only
then does it forward SIGINT, and after 2 s more it stops `dexcli` with SIGTERM.
SIGTSTP stays at its default so job control stops the launcher with `dexcli`.
SIGTERM is handled as above.
The Worker host is in another process group, so terminal signals never reach
it.

The Worker host stops when the launcher asks, when the IPC channel
disconnects, on SIGTERM, or when its watchdog finds the launcher gone. The
watchdog runs in a `worker_threads` thread, so it keeps working while the
main event loop is stuck. It checks `process.ppid` every second. Once the
launcher is gone or a shutdown started, it allows 15 s; then it sends SIGTERM
to each registered tool process group, SIGKILL 2 s later, and SIGKILL to the
Worker host's own group. The registered groups live in a `SharedArrayBuffer`
that the main thread updates synchronously on each registration and release,
so the watchdog never signals a released group even while the main loop is
stuck. Before each signal it also checks that the group's leader still has
the start time the run record holds and leads the group.

The shell tool runs each command in its own process group. In a Worker host,
`runShellCommand` registers that group right after the spawn and releases it
when Node reaps the shell, so a signal to the group before the release cannot
reach another process. An orderly shutdown sends each registered group
SIGTERM, then SIGKILL after 2 s.

Each Worker host keeps a run record, `~/.dex/agents/run/<harness>-<pid>.json`,
with the `ps` identity (pid, process group, start time, and the SHA-256 of the
command line) of
its launcher, itself, and each registered tool group. With the switch on,
every command start scans these records. For a record whose launcher no
longer runs, it sends SIGTERM to the Worker host's group and each tool group,
then SIGKILL 2 s later, each time only when the process still has the
recorded start time and command line and leads the recorded group. It then
deletes the record and prints
`Stopped a leftover worker host (pid N) and 1 tool process group.`

Known limits: the 1 s plus 15 s bound needs the Worker host to be scheduled
at all, so it does not hold while the process is stopped. Processes that a
harness starts outside the Worker host's group, such as Claude Code's own Bash
tool, are not registered, and a tool group whose leader was already reaped by
another process is not signaled. If the launcher and `dexcli` are both killed,
nobody restores the terminal; run `reset` or `stty sane`. Node cannot
redirect its own file descriptors, so during the handoff only JavaScript
stream writes of the launcher go to the log. A module the launcher loads,
such as a harness's framework, that writes to fd 1 or 2 directly while
`dexcli` runs would reach the terminal UI's screen; the CLI core's own code
never does (a conformance test checks it), and such a harness must load the
framework in `createSessionFlow` instead.

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` builds the core, installs the locked dependencies, typechecks, and
runs the conformance cases of `agents/conformance/cli_config.json` and
`agents/conformance/terminal_ui`, and the integration tests. The terminal UI
tests need Python 3.11 or later on `PATH`: they run the command on a
pseudo-terminal and point `DEXCLI` at a script that runs the contract host
(`agents/cli/tests/terminal_ui_contract_host.py`) as the terminal UI. The integration tests run a scripted built-in loop harness
against a private `dexcli dev`, a fake connectors MCP server, and a local
web page. `.npmrc` sets `install-links`, so the local core is copied with one
shared `@superdurable/dex`, as in a published install.
