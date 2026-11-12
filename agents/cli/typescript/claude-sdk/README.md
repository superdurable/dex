# @superdurable/dex-agents-claude-sdk-cli

`@superdurable/dex-agents-claude-sdk-cli` provides the `dex-agents-claude-sdk`
command. It runs a
[Claude Agent SDK](https://github.com/anthropics/claude-agent-sdk-typescript)
agent as a durable Dex agent session and chats with it in your terminal. The
shared CLI core, [`@superdurable/dex-agents-cli-core`](../core/README.md), starts
or reuses `dexcli dev`, connects the connectors MCP server, and runs the
Worker. This package only builds the agent with the
[Claude Agent SDK adapter](../../../typescript/claude-agent-sdk/README.md).

The Python package `dex-agents-claude-sdk-cli` provides the same command with
the same behavior; see [its README](../../python/claude-sdk/README.md) for the
local tools and their rules.

## Running it

The command needs Node.js 22 or 24 and an Anthropic API key:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
npx -p @superdurable/dex-agents-claude-sdk-cli dex-agents-claude-sdk
```

The first run asks the `init` questions and writes
`~/.dex/agents/claude-sdk.yaml`. Then type a message. Approve or reject tool
calls with `y` or `n`, and type `/exit` to leave. The session stays in Dex,
and the command prints the `--resume` line that continues it. See the
[CLI core README](../core/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

The command passes its environment to Claude Code unchanged and never stores
the key.

## Configuration and local tools

The model defaults to `claude-opus-5-5`; set `model` in the file or pass
`--model` to change it. The `claude` section has no options yet, and any key in
it is a configuration error.

Claude Code's own tools replace the CLI core's `files`, `shell`, and `web_fetch`
tools; the CLI core's browser tools and connector tools stay:

| `local_tools` group | Claude Code tools | Approval |
| --- | --- | --- |
| `files` | `Read`, `Grep`, `Glob` | No |
| `files` | `Write`, `Edit` | Every call |
| `shell` | `Bash` | Unless `local_tools.shell.allow` has the whole command text. A background command always waits. |
| `web_fetch` | `WebFetch` | No |

Each turn offers only the groups its session settings switch on. A resumed
segment also lists the Claude Code tools the turn already recorded calls of, so
the CLI can replay those records; a new call of such a tool is still refused
while its group is off. File tools
refuse paths outside the workspace, counting a dangling symbolic link as its
target, and `Glob` and `Grep` patterns must not contain `..`. `approval.always`
adds approval by tool name, Claude Code reads no settings files, and the tool Step checks an approved
call again on the Worker that runs it. These tools run inside the Claude Code
CLI, so a Worker that dies during a turn segment can run one of them twice.

The command uses only these Claude Code tools, the CLI core's browser tools,
and the connector tools. It loads no MCP servers and no claude.ai connectors
from your Claude Code configuration or login, such as a workspace `.mcp.json`
or the Gmail connector of a claude.ai account. External tools come only from
the Dex connectors, which Dex approves and records as Steps.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the Claude Agent SDK adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

`ci.sh` builds the core, the Claude adapter, and the CLI core, which this
package installs from the repository, then typechecks and runs the integration
tests against a private `dexcli dev`:

```bash
cd agents/cli/typescript/claude-sdk
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home. The native Claude Code binary of
`@anthropic-ai/claude-agent-sdk` calls `test/fixtures/mock-messages-api.ts`, a
scripted Messages API on 127.0.0.1; a refusing proxy stops any other request.
They cover the same scenarios as the Python package's tests.
