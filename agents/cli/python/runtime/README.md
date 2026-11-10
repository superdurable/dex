# dex-agents-cli

`dex-agents-cli` (import `dex_agents_cli`) is the shared runtime of the Python
Dex Agents CLI commands, such as `dex-agents-claude-sdk`. Each command is a
thin harness package. It builds the agent session Flow of one agent framework
and calls `dex_agents_cli.main`. The runtime does everything else:

- reads `~/.dex/agents/<harness>.yaml`, the shared format in
  [`agents/cli`](../../README.md), and creates it on first run;
- reuses the machine's running `dexcli dev`, or starts one, and downloads
  `dexcli` when it is not installed;
- offers connector tools from the connectors MCP server and local tools for
  files, shell commands, web pages, and a browser;
- runs the Worker in the command's process and chats with one session in the
  terminal.

## Running a command

```bash
uvx dex-agents-claude-sdk            # new session; asks the init questions on first run
uvx dex-agents-claude-sdk --resume <session-id>
uvx dex-agents-claude-sdk sessions   # this agent's sessions
uvx dex-agents-claude-sdk connectors # connector tools and Dex Web connections
uvx dex-agents-claude-sdk init       # create or replace the configuration file
```

| Option | Meaning |
| --- | --- |
| `--config PATH` | Use this configuration file instead of `~/.dex/agents/<harness>.yaml`. |
| `--yes` | Use the defaults instead of asking questions. Without a file, writes the default file. |
| `--resume ID` | Continue an existing session. `run --resume ID` is the same. |

Without a configuration file, a command in a terminal asks the init questions,
writes the file, and runs. Without a terminal it runs on the defaults, writes
nothing, and says that `init` creates a file. A file that fails the rules stops
the command with the error code and JSON Pointer, such as
`schema at /local_tools/shell/allow`.

### The terminal chat

Each line is a user message. Model text streams as it arrives, and each tool
call prints a `[tool]` line when it starts and ends. A call that waits for
approval prints `[approval] <tool> <arguments>`; answer `y`, or `n` with an
optional reason. A question from the agent prints `[question]`; answer with the
text or a choice number, or an empty line to decline.

The first Ctrl-C during a turn cancels it (`cancel_turn`). Another Ctrl-C, or
Ctrl-C or Ctrl-D while idle, exits. Exiting never ends the session: it stays in
Dex, and `--resume <id>` continues it. Dex Web shows it at the printed URL.

Before each message the command reads the session's settings. It prints a
warning for keys it ignores, and the rejection when the settings fail the
rules; that turn then fails with the same error.

## The Dex server

`dex.server: auto` finds a server in this order:

1. The `dexcli dev` an earlier command started, recorded in
   `~/.dex/agents/dexcli-dev.json`, while its process lives and Dex Web's
   `/readyz` answers.
2. A `dexcli dev` on the default ports, 127.0.0.1:8801 and Dex Web on 8802.
3. A new `dexcli dev -open=false`, started in the background in its own
   session. The command reads its ready output (`Dex Server:`, `Dex Web:`,
   `Connectors MCP:`), records it, and prints how to stop it. It keeps running
   after the command exits, so later commands and `--resume` reuse it. When
   `connectors.mcp` is a URL and `dexcli dev` supports `--connectors-mcp`, it
   starts with `--connectors-mcp=false`.

The command uses `dexcli` from `DEXCLI`, then `PATH`, then `~/.dex/bin`.
Without one, it downloads the pinned release `cli-v<version>` for the platform
from GitHub, checks the archive against the release's `checksums.txt`, and
installs only `dexcli` and `dex-server-zd` into `~/.dex/bin`.

Another `dex.server` value is a server URL; the command connects to it and
starts nothing.

| Environment variable | Meaning |
| --- | --- |
| `DEXCLI` | The `dexcli` executable to run. |
| `DEX_AGENTS_DEV_DEX_PORT`, `DEX_AGENTS_DEV_WEB_PORT` | The ports to look for, and to start `dexcli dev` on. |
| `DEX_AGENTS_DEXCLI_RELEASE_URL` | A mirror of the `dexcli` release downloads. |

## Connector tools

`connectors.mcp: auto` uses `http://127.0.0.1:<web-port>/mcp`, which
`dexcli dev` serves from 1.43 on, or the `Connectors MCP:` address a started
`dexcli dev` prints. A URL names another loopback server.

`ConnectorsMcpToolSource` offers the server's tools through `McpToolSource`,
so each call is a Dex tool Step with the server's effect class, approval,
idempotency key, and tasks. The tool names are the server's own names, such as
`slack_post_thread_reply`, so `approval.always` lists them as the agent sees
them.

`connectors.enabled` filters by connector, connection, and operation. The
runtime reads them from each tool definition's `_meta` keys
`io.superdurable.dex/connector`, `io.superdurable.dex/connection`, and
`io.superdurable.dex/operation`. A tool without them is offered only while
`connectors.enabled` is omitted.

At startup the command lists the server once:

- When it cannot, it prints the reason, including the text of a 503 such as a
  server that is still starting. A file that lists connectors in
  `connectors.enabled` stops the command. Otherwise the command continues, and
  each later turn lists the server again.
- An entry of `connectors.enabled` with no tool on the server has no
  authorized connection. The command prints a hint and, in a terminal, opens
  the Dex Web Connectors page.

## Local tools

The local tool source offers these tools in their own Dex tool Steps. Paths
are relative to `workspace`, and every path that resolves outside it, through
`..`, an absolute path, or a symlink, fails the call.

| Group | Tool | Effect | Approval |
| --- | --- | --- | --- |
| `files` | `read_file`, `list_directory`, `search_files` | read | No |
| `files` | `write_file` | idempotent write | Yes |
| `files` | `edit_file` | non-idempotent write | Yes |
| `shell` | `run_shell_command` | non-idempotent write | Yes, unless the whole command text is in `local_tools.shell.allow` |
| `web_fetch` | `web_fetch` | read | No |
| `browser` | `browser_open`, `browser_snapshot` | read | No |
| `browser` | `browser_click`, `browser_fill`, `browser_submit` | non-idempotent write | Yes |

- A shell command runs with `/bin/sh -c` in the workspace, in its own process
  group, so Ctrl-C never reaches it. It times out after 120 seconds by default
  and 600 at most. It is a single-attempt write: a Worker that dies during it
  records `outcome_unknown` and never runs it again.
- The browser is Playwright's headless Chromium, downloaded the first time a
  browser tool runs. Each session has one page in the Worker process. Every
  tool returns the page's URL, title, and accessibility snapshot as text;
  screenshots come with image support. After a Worker restart a session has no
  page until the agent opens one.
- `approval.always` adds approval to any of these tools.

## Session settings

Every model call resolves its turn's session settings with the shared rules
in [`agents/cli`](../../README.md#session-settings) before the harness runs.
Settings that fail the schema or widen the file fail the model call with
`SessionSettingsRejectedError`, `session settings rejected: <code> at <path>`.
The session retries it and then fails the turn with that error. Otherwise
`current_turn_config()` returns the turn's configuration while the harness's
runner runs:

- the runtime's tool sources offer only the connectors, operations, and local
  tool groups the turn enables, and outside a model call they offer nothing;
- a shell command's allow list and `approval.always` come from the turn;
- the harness reads the turn's model and instructions.

## Writing a harness command

A harness package implements `AgentHarness` and calls `main` from its console
script:

```python
from dex_agents_cli import AgentHarness, HarnessBuildContext, LocalToolGroup, main


class MyHarness:
    command_name = "dex-agents-my-framework"
    config_name = "my-framework"          # ~/.dex/agents/my-framework.yaml
    config_section = "builtin"            # the configuration file's harness section
    flow_type = "DexAgentsMyFramework"    # keep it stable: sessions resume by it
    default_model = "openai/gpt-5-mini"   # None: the file must set model
    replaced_local_tool_groups: frozenset[LocalToolGroup] = frozenset()

    def build_session_flow(self, context: HarnessBuildContext) -> AgentSessionFlow:
        ...


def run() -> None:
    raise SystemExit(main(MyHarness()))
```

Import `dex_agents_cli` before any module that imports gRPC, such as `dex` or
a `dex_agents` adapter. The package turns off gRPC's fork handlers, which only
works before gRPC loads, so they do not print into every shell command.

### `AgentHarness`

| Member | Meaning |
| --- | --- |
| `command_name: str` | The console script name. |
| `config_name: str` | The configuration file stem. |
| `config_section: str` | The harness section of the file, such as `claude`. |
| `flow_type: str` | The Flow type of the sessions. |
| `default_model: str \| None` | The model when the file sets none. |
| `replaced_local_tool_groups: frozenset[LocalToolGroup]` | Local tool groups the harness provides itself. |
| `build_session_flow(context: HarnessBuildContext) -> AgentSessionFlow` | Builds the Flow once per command run. |

`LocalToolGroup` is `FILES`, `SHELL`, `WEB_FETCH`, or `BROWSER`. The runtime
leaves a replaced group out of its local tool source. The harness then provides
that group itself and honors the same rules: the workspace as the working
directory, the turn's `local_tools` switches and allow list, and the approvals
in the table above. The Claude Agent SDK command, for example, replaces
`FILES`, `SHELL`, and `WEB_FETCH` with Claude Code's Read, Grep, Glob, Write,
Edit, Bash, and WebFetch, and keeps the runtime's browser tools. Another
harness may replace only `FILES` and `SHELL`.

### `HarnessBuildContext`

| Member | Meaning |
| --- | --- |
| `config: EffectiveConfig` | The file's effective configuration, which bounds every turn. |
| `config_path: Path \| None` | The file, or `None` on defaults. |
| `workspace: Path` | The expanded workspace, which exists. |
| `tool_sources: Sequence[ToolSource]` | The connector and local tool sources. Offer all of them. |
| `system_prompt_file_text: str \| None` | The text of `system_prompt_file`, read at startup. |
| `resolver: TurnConfigResolver` | Resolves session settings. |
| `harness_options` | The harness's own section, `{}` by default. |
| `turn_config(session_settings=None) -> EffectiveConfig` | The running turn's configuration, or that of some settings. Without settings outside a model call it raises `RuntimeError`, so a turn never silently gets the file's wider bounds. |
| `instructions(turn_config) -> str \| None` | `system_prompt`, else the file's text, else `None` for the harness default. |

The returned Flow must use `flow_type`. The runtime wraps its runner so that
each model call resolves the turn first, keeping the runner's optional
protocols, and registers the result.

A runner-based adapter passes the sources to its flow builder and reads the
turn inside its options factory or runner:

```python
def build_session_flow(self, context: HarnessBuildContext) -> AgentSessionFlow:
    def options_factory(options_context: ClaudeAgentOptionsContext) -> ClaudeAgentOptions:
        turn_config = context.turn_config()
        return ClaudeAgentOptions(
            model=turn_config.model,
            system_prompt=context.instructions(turn_config),
            cwd=str(context.workspace),
        )

    return claude_agent_session_flow(
        options_factory, flow_type=self.flow_type, tool_sources=context.tool_sources
    )
```

A Stepwise harness puts the sources in `AgentDefinition.tool_sources` and
resolves the model and instructions from the session settings it is given:

```python
def build_session_flow(self, context: HarnessBuildContext) -> AgentSessionFlow:
    def resolve_model(settings: Mapping[str, Any]) -> ModelClient:
        return LiteLLMModelClient(context.turn_config(settings).model)

    definition = AgentDefinition(
        name="assistant",
        instructions=lambda instruction: context.instructions(
            context.turn_config(instruction.session_settings)
        ) or "You are a helpful assistant.",
        model=resolve_model({}),
        resolve_model=resolve_model,
        tool_sources=list(context.tool_sources),
    )
    return stepwise_agent_session_flow(definition, flow_type=self.flow_type)
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
Playwright's headless Chromium and skips only when it cannot start. Agents CI
runs `ci.sh` for every `agents/cli/<language>/<package>`.
