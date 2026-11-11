# dex-agents-builtin-cli

`dex-agents-builtin-cli` provides the `dex-agents-builtin` command. It runs
the Dex built-in agent loop, Stepwise mode, as a durable Dex agent session
and chats with it in your terminal. No agent framework is involved: the
session's own `RunModel` Step makes one model call through
[LiteLLM](https://docs.litellm.ai/), and every tool call runs in its own Dex
tool Step. The shared runtime, [`dex-agents-cli`](../runtime/README.md),
starts or reuses `dexcli dev`, connects the connectors MCP server, and runs
the Worker. This package only builds the Stepwise agent with the
[core's LiteLLM model client](../../../python/core/README.md).

## Running it

The command needs Python 3.11 or later and a key for the model's provider:

```bash
export OPENAI_API_KEY=sk-...
uvx --from dex-agents-builtin-cli dex-agents-builtin
```

The first run asks the `init` questions and writes
`~/.dex/agents/builtin.yaml`. Then type a message at the `>` prompt. Approve
or reject tool calls with `y` or `n`, answer the agent's questions, and press
Ctrl-D to exit. The session stays in Dex, and the command prints the
`--resume` line that continues it. See the
[runtime README](../runtime/README.md#running-a-command) for every subcommand
and option, and [`agents/cli`](../../README.md) for the configuration file.

LiteLLM reads each provider's standard environment variables, such as
`OPENAI_API_KEY` and `OPENAI_BASE_URL` for `openai/` models and
`ANTHROPIC_API_KEY` for `anthropic/` models. The command never prints or
stores a key.

## Configuration

The model defaults to `openai/gpt-5.2`; set `model` in the file or pass
`--model` to change it. It is a LiteLLM model name, `<provider>/<model>`, such
as `anthropic/claude-opus-5-5`. A session's settings may change the model and
the instructions; each model call uses the values of its turn.
`system_prompt` replaces the default instructions. The `builtin` section has
no options yet, and any key in it is a configuration error.

## Tools

The agent's tools are the runtime's: its local tool source and the connector
tools of the connectors MCP server. Every call runs in its own Dex tool Step,
with the runtime's policy. Reads run at once, file writes and shell commands
wait for approval unless the shell allow list has the command, and the file
tools stay in the workspace. See the
[runtime README](../runtime/README.md) for the local tools. The agent can also
ask you a question with the `ask_user` built-in tool.

## Development

Develop with uv 0.12.2. The package resolves `dex-agents` and the runtime
from this repository by path. `uv.lock` pins `litellm` to the version the
core's lock pins, and `ci.sh` checks that. `ci.sh` installs the locked
dependencies, checks lint and types, and runs the tests against a private
`dexcli dev`:

```bash
cd agents/cli/python/builtin
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the command in a pseudo-terminal with a private home and an
allow-listed environment. LiteLLM sends `openai/` models to the scripted
Responses API of [`../testing`](../testing); a refused proxy stops any other
request. A fake connectors MCP server serves a connector read and write. The
tests cover a chat turn and `--resume`, `read_file` without approval and
`write_file` after approval, a connector read and an approved connector write
that runs once, session settings that change the model, and a key in the
`builtin` section.
They also cover a `system_prompt` set in the session settings, which reaches
the next model request, and a rejected file write and connector write, which
never run.
