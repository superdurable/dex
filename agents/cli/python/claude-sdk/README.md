# dex-agents-claude-sdk-cli

`dex-agents-claude-sdk-cli` provides the `dex-agents-claude-sdk` command. It
runs a [Claude Agent SDK](https://github.com/anthropics/claude-agent-sdk-python)
agent as a durable Dex agent session and chats with it in your terminal. The
shared CLI core, [`dex-agents-cli-core`](../core/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, and runs the Worker. This
package only builds the agent with the
[Claude Agent SDK adapter](../../../python/claude-agent-sdk/README.md).

The TypeScript package `@superdurable/dex-agents-claude-sdk-cli` provides the
same command with the same behavior.

## Running it

The command needs Python 3.11 or later and an Anthropic API key:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
uvx --from dex-agents-claude-sdk-cli dex-agents-claude-sdk
```

The first run asks the `init` questions and writes
`~/.dex/agents/claude-sdk.yaml`. Then type a message at the `>` prompt. Approve
or reject tool calls with `y` or `n`, and press Ctrl-D to exit. The session
stays in Dex, and the command prints the `--resume` line that continues it.
See the [CLI core README](../core/README.md#running-a-command) for every
subcommand and option, and [`agents/cli`](../../README.md) for the
configuration file.

The command passes its environment to Claude Code unchanged and never stores
the key.

## Configuration

The model defaults to `claude-opus-5-5`; set `model` in the file or pass
`--model` to change it. `system_prompt` replaces the default instructions.
The `claude` section has no options yet, and any key in it is a configuration
error.

## Local tools

Claude Code's own tools replace the CLI core's `files`, `shell`, and
`web_fetch` tools. The CLI core's browser tools and connector tools stay.

| `local_tools` group | Claude Code tools | Approval |
| --- | --- | --- |
| `files` | `Read`, `Grep`, `Glob` | No |
| `files` | `Write`, `Edit` | Every call |
| `shell` | `Bash` | Unless `local_tools.shell.allow` has the whole command text. A background command always waits. |
| `web_fetch` | `WebFetch` | No |

- Each turn offers only the tools whose group the turn switches on, after
  the session settings narrow the file. A switched-off group's tools are not
  offered, and a call of one is refused. One exception: a resumed segment
  also lists the Claude Code tools the turn already recorded calls of, so the
  CLI can replay those records; a new call of such a tool is still refused
  while its group is off.
- `approval.always` adds approval to any of these tools by name, such as
  `Read`.
- Claude Code runs in the workspace. The file tools refuse a path outside it,
  after resolving `~`, symbolic links, and relative paths, and refuse any
  path with `..`. A dangling symbolic link counts as the target it would
  create. A `Glob` pattern or a `Grep` `glob` must not contain `..` at all,
  and an absolute one must start inside the workspace. `Bash` is not confined:
  an approved command runs with your permissions.
- Claude Code reads no settings files, so their allow rules cannot skip an
  approval. Its other tools, such as subagents, are not offered.
- The command uses only these Claude Code tools, the CLI core's browser
  tools, and the connector tools. It loads no MCP servers and no claude.ai
  connectors from your Claude Code configuration or login, such as a
  workspace `.mcp.json` or the Gmail connector of a claude.ai account.
  External tools come only from the Dex connectors, which Dex approves and
  records as Steps.
- An approved call is checked again in its tool Step, on the Worker that runs
  it, with that Worker's configuration file narrowed by the turn's session
  settings. It fails without running when its group is off there or its path
  is outside the workspace, and the model sees why. The session settings
  count as the turn started with them, so a change takes effect from the next
  turn.
- These tools run inside the Claude Code CLI, not in their own Dex Steps. A
  Worker that dies during a turn segment runs the segment again, so a tool
  call in it can run twice. Connector and browser tools run in their own
  Steps and are recorded.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the Claude Agent SDK adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

Develop with uv 0.12.2. The package resolves `dex-agents`, the adapter, and
the CLI core from this repository by path. `ci.sh` installs the locked
dependencies, checks lint and types, and runs the tests against a private
`dexcli dev`:

```bash
cd agents/cli/python/claude-sdk
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the command in a pseudo-terminal with a private home. The
bundled Claude Code CLI calls `tests/mock_messages_api.py`, a scripted
Messages API on 127.0.0.1; a refused proxy stops any other request. A fake
connectors MCP server serves a connector read and write. The tests cover a
chat turn and `--resume`, a connector read and an approved connector write
that runs once, `Read`, `Glob`, `Grep`, and `WebFetch` without approval,
`Write`, `Edit`, and `Bash` after approval, an allow-listed command run in the
workspace and an allow-listed background command that still waits, paths and
patterns that leave the workspace, a dangling link to outside, session
settings that switch the shell off, an approved call that the Worker running
it refuses after its file switched the group off, and the environment that
reaches Claude Code with and without a key.
