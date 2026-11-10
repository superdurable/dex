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

The exit code is 0 on success, 1 for a failure at run time, such as a Dex
server that does not start or an explicit `connectors.mcp` URL that is
unavailable, and 2 for a usage or configuration error.

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

`dex.server: auto` finds a server in this order:

1. The `dexcli dev` an earlier command started, recorded in
   `~/.dex/agents/dexcli-dev.json`, while its process lives and Dex Web's
   `/readyz` answers.
2. A `dexcli dev` whose Dex Web answers `/readyz` on the configured ports,
   127.0.0.1:8801 for Dex and 8802 for Dex Web by default.
3. A new `dexcli dev -open=false`, started in the background in its own
   session, with its output in `~/.dex/agents/dexcli-dev.log`. The command
   reads its ready output (`Dex Server:`, `Dex Web:`, `Connectors MCP:`),
   records it, and prints how to stop it. It keeps running after the command
   exits, so later commands and `--resume` reuse it.

The command uses `dexcli` from `DEXCLI`, then `PATH`, then `~/.dex/bin`.
Without one, it downloads the pinned release `cli-v<version>` for the platform
from GitHub, checks the archive against the release's `checksums.txt`, and
installs only `dexcli` and `dex-server-zd` into `~/.dex/bin`.

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
`connectors.enabled` is set, a tool without these keys is hidden.

At startup the command lists the server once:

- An `auto` server that does not answer, such as one answering 503 while it
  starts or an older `dexcli` without `/mcp`, gives a warning with the reason.
  The command runs without connector tools and lists the server again on every
  turn, so a starting server comes alive. An explicit `connectors.mcp` URL
  that does not answer stops the command with exit code 1.
- An entry of `connectors.enabled` with no tool on the server has no
  authorized connection. The command prints a hint with the Dex Web
  Connectors page, `<Dex Web URL>/v1/connectors`, and opens it only in an
  interactive terminal.

## Local tools

The local tool source offers these tools in their own Dex tool Steps. Paths
are relative to `workspace`, and every path that resolves outside it, through
`..`, an absolute path, or a symlink, fails the call.

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
- The browser is Playwright's headless Chromium, downloaded the first time a
  browser tool runs. Each session has one page in the Worker process. Every
  tool returns the page's URL, title, and accessibility snapshot as text;
  there are no screenshots until image support. After a Worker restart a
  session has no page until the agent navigates again.
- `approval.always` adds approval to any tool by name.

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

The returned Flow must use `context.flow_type`. The runtime wraps its runner so
that each model call resolves the turn first, keeping the runner's optional
protocols, and registers the result.

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
