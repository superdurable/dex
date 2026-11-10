# dex-agents-cli

`dex-agents-cli` (import `dex_agents_cli`) is the shared runtime of the Python
Dex Agents CLI commands, such as `dex-agents-claude-sdk`. Each command is a
thin harness package. It builds the agent session Flow of one agent framework
and calls `dex_agents_cli.run_agent_cli`. The runtime does everything else:

- reads `~/.dex/agents/<name>.yaml`, the shared format in
  [`agents/cli`](../../README.md), and creates it on first run;
- reuses the machine's running `dexcli dev`, or starts one, and downloads
  `dexcli` when it is not installed;
- offers connector tools from the connectors MCP server and local tools for
  files, shell commands, web pages, and a browser;
- runs the Worker in the command's process and chats with one session in the
  terminal.

The TypeScript runtime in `agents/cli/typescript/runtime` has the same
behavior and the same concepts, with the same names in camel case.

## Running a command

```text
dex-agents-<name> [run|init|connectors|sessions] [options]

  run          Chat with the agent in this terminal (the default).
  init         Create or replace the configuration file.
  connectors   List the connector tools and the Dex Web connections.
  sessions     List this command's sessions.

  -c, --config <file>   Configuration file (default ~/.dex/agents/<name>.yaml).
  -y, --yes             Write and use the defaults instead of asking.
  -r, --resume <id>     Continue a session.
  -m, --model <id>      Override the configured model.
```

Without a configuration file, `run` asks the `init` questions at a terminal
and writes the file. With `--yes` it writes the defaults without asking.
Without a terminal it runs on the defaults, writes nothing, and says to run
`init`. A file that fails the rules stops the command with the error code and
JSON Pointer, such as `schema at "/local_tools/shell/allow" (schema keyword type)`.

The exit code is 0 on success; 1 for a failure at run time, such as a Dex
server that does not start, an explicit `connectors.mcp` URL that is
unavailable, or a turn or session that failed during the chat; and 2 for a
usage or configuration error.

### Sessions and Flow types

The command registers its sessions under `<flow_type>-<12 hex digits>`, the
harness's Flow type namespaced by this machine, user, configuration file, and
workspace. The digits are the start of the SHA-256 of
`"<hostname>\n<user>\n<config identity>\n<workspace>"`: the user is the
numeric user ID, or the user name where there is none; the config identity is
the configuration file's real path, or `defaults` without a file; and the
workspace is its real path. Tool settings do not count. `session_flow_type`
computes it. Two commands that differ in any of these never take each other's
tool Steps, so an approved write runs only with the workspace, tools, and
connections it was approved under. `sessions` lists, and `--resume` continues,
only sessions of the same Flow type; resuming another one exits with code 2
and says to use the user, configuration file, and workspace that started it.

### The terminal chat

Each line is a user message. Model text streams as it arrives, and each tool
call prints a `[tool]` line when it starts and ends. A call that waits for
approval prints `[approval] <tool> <arguments>`; answer `y`, or `n` with an
optional reason. A question from the agent prints `[question]`; answer with the
text or a choice number, or an empty line to decline.

The first Ctrl-C during a turn cancels it (`cancel_turn`), and a second Ctrl-C
exits. Ctrl-C while idle, Ctrl-D, and `/exit` also exit. Exiting never ends the
session: it stays in Dex, and the command prints the `--resume` line that
continues it. New sessions are named `<name>-YYYYMMDD-HHMMSS-<6 hex digits>`.

## The Dex server

`dex.server: auto` looks at the configured ports, 127.0.0.1:8801 for Dex and
8802 for Dex Web by default:

1. Nothing answers on the Dex Web port: the command starts
   `dexcli dev -open=false -dex-port <port> -web-port <port>` in the
   background in its own session, with its output in
   `~/.dex/agents/dexcli-dev.log`. It reads the ready output (`Dex Server:`,
   `Dex Web:`, `Connectors MCP:`), records the process and ports in
   `~/.dex/agents/dexcli-dev.json`, and prints how to stop it. It keeps running
   after the command exits, so later commands and `--resume` reuse it.
2. Dex Web answers `/readyz` with its readiness JSON (`status: "ready"` with
   `definitionRevision`, `source`, and `definitionCount`), and within five
   seconds the Dex port answers as FlowService, reporting an unknown Flow as not
   found: the command reuses it. It checks this even for the `dexcli dev` it
   started itself. While Dex Web reports that Dex is starting, it waits up to
   a minute.
3. Anything else answers: the command stops with exit code 1 instead of
   trusting it, because that service would stand in for Dex and the connectors
   MCP server.

The readiness JSON is checked by its values: `status` is `"ready"`,
`definitionRevision` and `source` are strings, and `definitionCount` is an
integer. These checks tell a `dexcli dev` from an unrelated service, but they
do not prove who owns the ports: another local user's process that binds them
first and answers the same way is not detected. Run on a machine you do not
share, or set `dex.server` and `connectors.mcp` to URLs you trust.

The command finds `dexcli` in this order: `DEXCLI`; the release it downloaded,
`~/.dex/bin/dexcli-<version>/dexcli`, after checking both executables against
the SHA-256 recorded when it was installed; the first `dexcli` on `PATH` whose
`--version` is at least the pinned version. Otherwise it downloads the pinned
release `cli-v<version>` for the platform from GitHub and checks the archive
against the release's `checksums.txt`. Every archive entry must be a relative
path without `..` and a regular file or a directory, or nothing is installed.
Entry names must not contain a backslash or a NUL either. Only `dexcli` and
`dex-server-zd` are extracted, into a private staging directory with
`install.json` (`version`, `archive`, `archiveSha256`, and the `sha256` of
each executable), which then becomes `~/.dex/bin/dexcli-<version>` in one
rename. A downloaded copy that changed later stops the command; delete its
directory to download it again. A cached executable must also still be
executable, and a `dexcli` on `PATH` counts only when `--version` exits with
code 0. When the command downloads, `DEX_AGENTS_DEXCLI_DOWNLOAD_URL`, and every
redirect of the checksums and the archive, must be an https URL, or http on
`127.0.0.1`, `localhost`, or `[::1]`.

Another `dex.server` value is a server URL; the command connects to it and
starts nothing.

| Environment variable | Meaning |
| --- | --- |
| `DEXCLI` | The `dexcli` executable to run. |
| `DEX_AGENTS_DEX_PORT`, `DEX_AGENTS_WEB_PORT` | The ports of the `dexcli dev` that `auto` reuses or starts. |
| `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` | The base URL of `dexcli` release downloads, such as a mirror. |

## Connector tools

`connectors.mcp: auto` uses the connectors MCP server that `dexcli dev` serves
at `<Dex Web URL>/mcp`. When the command started `dexcli dev`, it takes the URL
from the `Connectors MCP:` line of its output. With an explicit `dex.server`,
`auto` has no server, so connectors are off with a hint to set the URL.

`ConnectorsMcpToolSource` offers the server's tools through `McpToolSource`,
so each call is a Dex tool Step with the server's effect class, approval,
idempotency key, and tasks. The agent sees each tool by its MCP name, such as
`slack_post_thread_reply`, without a server prefix, and `approval.always`
names it the same way.

`connectors.enabled` filters by connector, connection, and operation. The
runtime reads them from the top-level `_meta` keys of each tool, which
`CONNECTOR_TOOL_META_KEYS` holds: `io.superdurable.connectors/connector`,
`io.superdurable.connectors/connection`, and
`io.superdurable.connectors/operation` (the operationId) or
`io.superdurable.connectors/operationName` (its snake-case name); a `tools`
entry matches either. The runtime never parses tool names. While
`connectors.enabled` is set, a tool without the connector and connection keys
is hidden, and an entry that lists operations matches only tools with both
`operation` and `operationName`.

Each call is checked against its turn again when it is built: a tool whose
connection or operation the turn does not enable, such as from a listing a
runner kept, becomes a call that its tool Step refuses with
`tool <name> is switched off for this session`. The local tools do the same
for a switched-off group.

The tool Step authorizes the call once more, on the Worker that runs it, with
that Worker's own configuration file narrowed by the session settings recorded
for the call's turn. A call may run on another Worker than the one that built
it, such as a command started later with the same file after the file was
edited. The tool's group, connection, and operation must be enabled there too,
and a shell command that was built without approval must still be in that
allow list; otherwise the call fails and nothing runs.

At startup the command lists the server once:

- An `auto` server that does not answer, such as one answering 503 while it
  starts or an older `dexcli` without `/mcp`, gives a warning with the reason.
  The command runs without connector tools and lists the server again in every
  model call, printing `Connector tools are unavailable for now` and
  `Connector tools are available again` when that changes. An explicit `connectors.mcp` URL
  that does not answer stops the command with exit code 1.
- An entry of `connectors.enabled` with no tool on the server has no
  authorized connection. The command prints a hint with the Dex Web
  Connectors page, `<Dex Web URL>/v1/connectors`, and opens it only in an
  interactive terminal.

## Local tools

The local tool source offers these tools in their own Dex tool Steps. Paths
are relative to `workspace`, or absolute inside it.

The file tools resolve a path's symbolic links one hop at a time, reading each
link relative to its directory's descriptor, and every hop must stay inside the
workspace, so a link such as `node_modules/.bin/tool` that points inside works
and one that points outside fails the call. `..` may not climb above the
workspace. An absolute path may spell the workspace in another case on a
case-insensitive disk. The resolved path is then opened from the workspace one
directory at a time, each component relative to its parent with `O_NOFOLLOW`,
so a directory swapped for a link while a tool runs fails the call. Each opened file or directory is checked against
the workspace again by the path the kernel reports for it, which also holds on
a case-insensitive disk. A file with more than one hard link is refused,
because the other link may be outside; `search_files` skips such files and
checks every file it reads the same way. `write_file` and `edit_file` create a
temporary file with `O_CREAT|O_EXCL|O_NOFOLLOW` in the checked directory and
rename it over the name, all through the directory's descriptor, so the write
lands in the directory that was checked. They refuse a symbolic link at the
name, check the directory again right before the rename, and afterwards check
that the name holds exactly the file they wrote. `edit_file` changes files of
at most 4 MiB (4194304 bytes) and refuses larger ones. Right before the rename
it checks that the name is the same file, with the same size, modification
time, and content, as when it read it; otherwise it writes nothing and says to
read the file again and retry the edit.

These checks have limits:

- Another process that moves a directory out of the workspace after the last
  check and before the rename can make a write land outside. The tool then
  reports the error, but the file is written. Only OS-level isolation, such as
  a container, closes this; writes always wait for approval.
- They cover the file tools only. An approved shell command runs with your
  permissions and can change anything, including the workspace and files
  outside it.

| Group | Tool | Effect | Approval |
| --- | --- | --- | --- |
| `files` | `read_file`, `list_directory`, `search_files` | read | No |
| `files` | `write_file` | idempotent write | Every call |
| `files` | `edit_file` | non-idempotent write | Every call |
| `shell` | `run_shell_command` | non-idempotent write | Unless the whole command text is in `local_tools.shell.allow` |
| `web_fetch` | `fetch_web_page` | read | No |
| `browser` | `browser_navigate`, `browser_snapshot` | read | No |
| `browser` | `browser_click`, `browser_fill`, `browser_submit_form` | non-idempotent write | Every call |

- A shell command runs with `/bin/sh -c` in the workspace, in its own process
  group, so Ctrl-C never reaches it. It times out after 120 seconds by default
  and 600 at most. It is a single-attempt write: a Worker that dies during it
  records `outcome_unknown` and never runs it again.
- The browser is headless: Playwright's own Chromium when it is installed,
  else the system Chrome, else Chromium that Playwright downloads the first
  time a browser tool runs. `browser_navigate` opens only `http` and `https`
  URLs, so the browser never reads local files. Each session has one page in
  the Worker process. Every tool returns the page's URL, title, and
  accessibility snapshot as text;
  there are no screenshots until image support. After a Worker restart a
  session has no page until the agent navigates again.
- `approval.always` adds approval to any tool by name.
- Each tool's effect and approval are a floor. An explicit `ToolPolicy` in
  the session options may make a tool more cautious, such as a `read` that
  must be approved, but never less: a write stays a write, and
  `requires_approval=False` never removes a tool's own approval.

## Session settings

Every model call resolves its turn's session settings with the shared rules
in [`agents/cli`](../../README.md#session-settings) before the harness runs.
Settings that fail the schema or widen the file fail the model call with
`ConfigError`, whose message is `<code> at "<pointer>"`. The session retries
it and then fails the turn with that error. Ignored top-level keys print
`Session settings keys ignored: <pointers>` to standard error, once per
distinct settings. While the harness's runner runs, `current_turn_config()`
returns the turn's configuration:

- the runtime's tool sources offer only the connectors, operations, and local
  tool groups the turn enables, and outside a model call they offer nothing;
- a shell command's allow list and `approval.always` come from the turn;
- the harness reads the turn's model and instructions with
  `HarnessContext.resolve_turn`.

## Writing a harness command

A harness package implements `AgentHarness` and calls `run_agent_cli` from its
console script:

```python
import dex_agents_cli  # first: before anything that imports gRPC

from dex_agents_cli import AgentHarness, HarnessContext, LocalToolGroup, run_agent_cli


class MyHarness:
    name = "my-framework"                  # dex-agents-my-framework, ~/.dex/agents/my-framework.yaml
    config_section = "builtin"             # the configuration file's harness section
    flow_type = "DexAgentsMyFrameworkCliSession"  # keep it stable: sessions resume by it
    default_model = "openai/gpt-5-mini"    # None: the file must set model
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        ...


def run() -> None:
    raise SystemExit(run_agent_cli(MyHarness()))
```

Import `dex_agents_cli` before any module that imports gRPC, such as `dex` or
a `dex_agents` adapter. The package turns off gRPC's fork handlers, which only
works before gRPC loads, so they do not print into every shell command.

### `AgentHarness`

| Member | Meaning |
| --- | --- |
| `name: str` | Command `dex-agents-<name>` and file `~/.dex/agents/<name>.yaml`. |
| `config_section: str` | The harness section of the file, such as `claude`. |
| `flow_type: str` | The Flow type of the sessions. |
| `default_model: str \| None` | The model when the file sets none. |
| `replaced_local_tools: frozenset[LocalToolGroup]` | Local tool groups the harness provides itself. |
| `create_session_flow(context: HarnessContext) -> AgentSessionFlow` | Builds the Flow once per command run. |

`LocalToolGroup` is `FILES`, `SHELL`, `WEB_FETCH`, or `BROWSER`. The runtime
leaves a replaced group out of `context.tool_sources`. The harness then
switches its own tools on from `context.config.local_tools` and gives them the
same policy: reads run at once, writes and shell commands wait for approval
unless the shell allow list has the exact command, and the working directory
is `context.workspace`. The Claude Agent SDK command, for example, replaces
`FILES`, `SHELL`, and `WEB_FETCH` with Claude Code's Read, Grep, Glob, Write,
Edit, Bash, and WebFetch, and keeps the runtime's browser tools. Another
harness may replace only `FILES` and `SHELL`.

### `HarnessContext`

| Member | Meaning |
| --- | --- |
| `flow_type: str` | The harness's Flow type. |
| `config: EffectiveConfig` | The file's effective configuration, with `--model` applied. It bounds every turn. |
| `workspace: Path` | The absolute workspace, which exists. |
| `instructions: str \| None` | `system_prompt`, else the text of `system_prompt_file`, else `None` for the harness default. |
| `harness_options: Mapping[str, Any]` | The harness's own section, `{}` by default. |
| `tool_sources: Sequence[ToolSource]` | The connector and local tool sources, which apply each turn's settings themselves. Offer all of them. |
| `resolve_turn(session_settings) -> ResolvedTurn` | A turn's narrowed configuration with its `model` and `instructions`. Raises `ConfigError` for rejected settings. |
| `resolve_tool_step_permissions(call=None) -> ResolvedTurn` | The permissions a replacement tool's own Step applies: this Worker's file narrowed by the session settings recorded for the call's turn. Pass the `DeferredToolExecution` from a runner, or nothing from a Stepwise function tool. Raises `RuntimeError` without a call outside a tool Step and a model call. |
| `current_turn() -> ResolvedTurn` | The turn whose model call or tool Step is running, or the file's configuration outside both. In a tool Step it is the configuration of the turn that made the call, from the settings the session recorded for that turn, so a replacement tool sees the turn's switches, shell allow list, and `approval.always`. |

The returned Flow must use `context.flow_type`, which is namespaced as
[Sessions and Flow types](#sessions-and-flow-types) describes. The runtime wraps its runner so
that each model call resolves the turn first, keeping the runner's optional
protocols, and registers the result.

A harness that replaces a local tool group gives its own tools the runtime's
rules with these helpers:

| Helper | Use |
| --- | --- |
| `is_local_tool_group_enabled(turn, group) -> bool` | Offer the group's tools only while the turn switches it on. |
| `is_shell_command_approval_required(turn, command) -> bool` | Approval is required unless the turn's allow list has the command's whole text. |
| `resolve_turn_tool_policy(turn_context, tool_name, effect, requires_approval, turn) -> ToolPolicy` | The call's policy: the tool's effect and approval as a floor, raised by an explicit session policy, and `approval.always`. |

A runner-based adapter passes the sources to its flow builder. Where its hook
receives no session settings, it reads the running turn with
`current_turn_config()`, which every model call sets:

```python
def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
    def options_factory(options_context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        turn = current_turn_config()
        assert turn is not None, "the runtime sets the turn around every model call"
        return ClaudeAgentOptions(
            model=turn.model, system_prompt=turn.instructions, cwd=str(context.workspace)
        )

    return claude_agent_session_flow(
        options_factory, flow_type=context.flow_type, tool_sources=context.tool_sources
    )
```

A Stepwise harness puts the sources in `AgentDefinition.tool_sources` and
resolves the model and instructions from the session settings it is given:

```python
def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
    def resolve_model(settings: Mapping[str, Any]) -> ModelClient:
        return LiteLLMModelClient(context.resolve_turn(settings).model)

    definition = AgentDefinition(
        name="assistant",
        instructions=lambda instruction: context.resolve_turn(
            instruction.session_settings
        ).instructions or "You are a helpful assistant.",
        model=resolve_model({}),
        resolve_model=resolve_model,
        tool_sources=list(context.tool_sources),
    )
    return stepwise_agent_session_flow(definition, flow_type=context.flow_type)
```

## Development

Develop with uv 0.12.2. The package resolves `dex-agents` from
`../../../python/core` by path. `ci.sh` installs the locked dependencies,
checks lint and types, and runs the tests against a private `dexcli dev`:

```bash
cd agents/cli/python/runtime
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the commands as processes, in a pseudo-terminal where a test
needs one, with a scripted Stepwise harness (`tests/scripted_harness.py`), a
fake connectors MCP server, and a private home directory each. They also run
every case of `agents/conformance/cli_config.json`. The browser test downloads
Playwright's headless Chromium when it is missing and skips only when it
cannot start. Agents CI runs `ci.sh` for every `agents/cli/<language>/<package>`.
