# @superdurable/dex-agents-cli

The shared runtime of the TypeScript Dex Agents CLI commands. Each harness
command, such as `dex-agents-claude-sdk`, is a thin package that implements
one `AgentHarness` and calls `runAgentCli`. This package does the rest:

- reads `~/.dex/agents/<harness>.yaml`, the format in
  [`../../README.md`](../../README.md), and creates it on first run;
- reuses or starts a local `dexcli dev`, downloading `dexcli` when needed;
- connects the connectors MCP server and applies `connectors.enabled`;
- offers the local tools: files, shell, web fetch, and a browser;
- runs the Worker in the command's process and chats in the terminal.

The Python runtime in `agents/cli/python/runtime` has the same behavior and
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
command prints the `--resume` line that continues it.

## The harness interface

A harness package exports one `AgentHarness`. Its bin calls `runAgentCli` and
exits with its result, because open connections can keep Node running.

```ts
#!/usr/bin/env node
import { createStepwiseAgentSessionFlow } from "@superdurable/dex-agents/stepwise";
import { runAgentCli, type AgentHarness } from "@superdurable/dex-agents-cli";

const harness: AgentHarness = {
  name: "builtin", // command dex-agents-builtin, file ~/.dex/agents/builtin.yaml
  configSection: "builtin", // this harness's section of the file
  flowType: "DexAgentsBuiltinCliSession",
  defaultModel: "openai/gpt-5-mini",
  replacedLocalTools: [], // for example ["files", "shell", "web_fetch"] for Claude Code
  createSessionFlow: (context) =>
    createStepwiseAgentSessionFlow(
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
  createSessionFlow(context: HarnessContext): AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
}

interface HarnessContext {
  readonly flowType: string;
  readonly config: EffectiveConfig; // the file, defaults filled in
  readonly workspace: string; // absolute, exists
  readonly instructions: string | null; // system_prompt or system_prompt_file text
  readonly harnessOptions: Readonly<Record<string, unknown>>;
  readonly toolSources: readonly ToolSource[]; // connectors, then local tools
  resolveTurn(sessionSettings: SessionSettings): ResolvedTurn; // model, instructions, narrowing
}

type LocalToolGroup = "files" | "shell" | "web_fetch" | "browser";
```

- A Stepwise harness puts `context.toolSources` in
  `AgentDefinition.toolSources`. A runner-based adapter, such as the Claude
  Agent SDK, OpenAI Agents, Vercel AI, or Mastra adapter, passes them to its
  runner's `toolSources` and builds the Flow with `createAgentSessionFlow`.
- Each tool source applies the turn's session settings itself:
  `connectors.enabled`, `local_tools`, the shell allow list, and
  `approval.always`. A harness reads only the model and the instructions,
  with `resolveTurn` in each model call. Rejected settings throw there, so
  the turn fails with the error code and JSON Pointer.
- `replacedLocalTools` lists the groups the harness serves with its own
  tools. The runtime leaves them out of `context.toolSources`. The harness
  switches its own tools on from `context.config.local_tools` and gives them
  the same policy: reads run at once, writes and shell commands wait for
  approval unless the shell allow list has the exact command, and the working
  directory is `context.workspace`.
- The runtime wraps the Flow's runner so that each model call runs with its
  turn's configuration in scope. The harness does not need to do anything for
  that.

The Python runtime uses the same names in snake case:

| TypeScript | Python |
| --- | --- |
| `AgentHarness` | `AgentHarness` (Protocol) |
| `name`, `configSection`, `flowType`, `defaultModel`, `replacedLocalTools` | `name`, `config_section`, `flow_type`, `default_model`, `replaced_local_tools` |
| `createSessionFlow(context)` | `create_session_flow(context)` |
| `HarnessContext` with `config`, `workspace`, `instructions`, `harnessOptions`, `toolSources`, `resolveTurn` | `HarnessContext` with `config`, `workspace`, `instructions`, `harness_options`, `tool_sources`, `resolve_turn` |
| `LocalToolGroup` | `LocalToolGroup` (`files`, `shell`, `web_fetch`, `browser`) |
| `runAgentCli(harness)` | `run_agent_cli(harness)` |

## Local tools

Each call is one Dex tool Step, recorded in the session.

| Group | Tool | Effect | Approval |
| --- | --- | --- | --- |
| `files` | `read_file`, `list_directory`, `search_files` | read | none |
| `files` | `write_file` | idempotent write | every call |
| `shell` | `run_shell_command` | single-attempt write | unless the allow list has the whole command text |
| `web_fetch` | `fetch_web_page` | read | none |
| `browser` | `browser_navigate`, `browser_snapshot` | read | none |
| `browser` | `browser_click`, `browser_fill`, `browser_submit_form` | single-attempt write | every call |

- File paths are relative to `workspace`, or absolute inside it. A path that
  leaves it, through `..`, an absolute path, or a symbolic link, fails.
- Shell commands run with the system shell in `workspace`, in their own
  process group, with a 120-second default timeout. A command whose Worker
  died while it ran records `outcome_unknown` and never runs again.
- Browser tools return the page title, URL, and accessibility snapshot as
  text; there are no screenshots yet. The browser is headless Chromium:
  Playwright's own when installed, else the system Chrome, else Playwright
  downloads Chromium on first use. Each session has one page in the Worker's
  memory.
- `approval.always` adds approval to any tool by name.

## Connectors

`connectors.mcp: auto` uses the connectors MCP server that `dexcli dev`
serves at `<Dex Web URL>/mcp`. When the runtime starts `dexcli dev`, it takes
the URL from the `Connectors MCP:` line of its output. An `auto` server that
is not available, such as one answering 503 while it starts, or an older
`dexcli` without `/mcp`, turns connectors off for the run with a warning. An
explicit `connectors.mcp` URL that is not available stops the command.

The agent sees each connector tool by its MCP name, such as
`slack_post_thread_reply`. `connectors.enabled` matches the
`io.superdurable.dex/connector`, `connection`, and `operation` values in each
tool's `_meta`. While the list is set, a tool without them is hidden. A
configured connection that the server does not list is reported as not
authorized, and at a terminal the command opens the Dex Web Connectors page.

## The Dex server and dexcli

`dex.server: auto` reuses the `dexcli dev` whose Dex Web answers `/readyz` on
port 8802, with Dex on 8801. Without one, it starts `dexcli dev` in the
background on those ports. Its output goes to `~/.dex/agents/dexcli-dev.log`,
and its pid and ports to `~/.dex/agents/dexcli-dev.json`. It keeps running
after the command exits, so later commands and Dex Web reuse it.

The runtime finds `dexcli` in this order: `DEXCLI`; an earlier download in
`~/.dex/bin/dexcli-<version>`; `dexcli` on `PATH` when it is at least the
pinned version; otherwise it downloads the pinned release for the platform
from GitHub, checks its SHA-256 against the release's `checksums.txt`, and
unpacks it into `~/.dex/bin`.

| Variable | Meaning |
| --- | --- |
| `DEXCLI` | The `dexcli` executable to use. |
| `DEX_AGENTS_DEX_PORT`, `DEX_AGENTS_WEB_PORT` | The ports of the `dexcli dev` that `auto` reuses or starts. |
| `DEX_AGENTS_DEXCLI_DOWNLOAD_URL` | The base URL of `dexcli` release downloads, such as a mirror. |

## Development

```bash
DEXCLI=/path/to/dexcli ./ci.sh
```

`ci.sh` builds the core, installs the locked dependencies, typechecks, and
runs the conformance cases of `agents/conformance/cli_config.json` and the
integration tests. The integration tests run a scripted Stepwise harness
against a private `dexcli dev`, a fake connectors MCP server, and a local
web page. `.npmrc` sets `install-links`, so the local core is copied with one
shared `@superdurable/dex`, as in a published install.
