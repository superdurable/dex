# dex-agents-openai-sdk-cli

`dex-agents-openai-sdk-cli` provides the `dex-agents-openai-sdk` command. It
runs an [OpenAI Agents SDK](https://openai.github.io/openai-agents-python/)
agent as a durable Dex agent session and chats with it in your terminal. The
shared CLI core, [`dex-agents-cli-core`](../core/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, and runs the Worker. This
package only builds the agent with the
[OpenAI Agents SDK adapter](../../../python/openai-agents/README.md).

## Running it

The command needs Python 3.11 or later and an OpenAI API key:

```bash
export OPENAI_API_KEY=sk-...
uvx --from dex-agents-openai-sdk-cli dex-agents-openai-sdk
```

The first run asks the `init` questions and writes
`~/.dex/agents/openai-sdk.yaml`. Then type a message at the `>` prompt.
Approve or reject tool calls with `y` or `n`, and press Ctrl-D to exit. The
session stays in Dex, and the command prints the `--resume` line that
continues it. See the [CLI core README](../core/README.md#running-a-command)
for every subcommand and option, and [`agents/cli`](../../README.md) for the
configuration file.

The SDK reads `OPENAI_API_KEY`, and `OPENAI_BASE_URL` for another endpoint.
The command never prints or stores the key. The SDK's own tracing is off, so
conversations are not uploaded to the OpenAI traces dashboard; Dex records
every model call and tool call in the session.

## Configuration

The model defaults to `gpt-5.2`; set `model` in the file or pass `--model` to
change it. It is an OpenAI Responses API model name. A session's settings may
change the model and the instructions; each model call uses the values of its
turn. The session setting `reasoning_effort` may be `low`, `medium`, or
`high`; each model call sends it as the SDK's `ModelSettings.reasoning.effort`.
Without it, the model's default applies. In the terminal UI, `/effort` sets
it, and `/model` also offers `gpt-5.2`, `gpt-5-mini`, and `gpt-5-nano`.
`system_prompt` replaces the default instructions. The `openai` section
has no options yet, and any key in it is a configuration error.

## Tools

The agent's tools are the CLI core's: its local tool source and the connector
tools of the connectors MCP server. Every call runs in its own Dex tool Step,
with the CLI core's policy. Reads run at once, file writes and shell commands
wait for approval unless the shell allow list has the command, and the file
tools stay in the workspace. See the
[CLI core README](../core/README.md) for the local tools.

The SDK fixes an agent's tools when the command starts. The command offers
the connector tools that the configuration file enables and that the
connectors MCP server lists at startup. Session settings still narrow the
tools of each turn. Restart the command in two cases:

- The connectors MCP server did not answer at startup, such as while it was
  still starting. The command warns that this run has no connector tools.
- You authorized a connection while the command ran.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the OpenAI Agents SDK adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

Develop with uv 0.12.2. The package resolves `dex-agents`, the adapter, the
CLI core, and the shared test support from this repository by path. `uv.lock`
pins `openai-agents` and `openai` to the versions the adapter's lock pins, and
`ci.sh` checks that. `ci.sh` installs the locked dependencies, checks lint and
types, and runs the tests against a private `dexcli dev`:

```bash
cd agents/cli/python/openai-sdk
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the command in a pseudo-terminal with a private home and an
allow-listed environment. The SDK calls the scripted Responses API of
[`../testing`](../testing); a refused proxy stops any other request. A fake
connectors MCP server serves a connector read and write. The tests cover a
chat turn and `--resume`, `read_file` without approval and `write_file` after
approval, a connector read and an approved connector write that runs once,
session settings that change the model, and a key in the `openai` section.
A `reasoning_effort` in the session settings reaches the next Responses API
request, and an undeclared one fails the turn. They also cover a `system_prompt` set in the session settings, which reaches
the next model request, and a rejected file write and connector write, which
never run.
The OpenAI Agents SDK command also warns to restart when the connectors MCP
server does not answer at startup.
