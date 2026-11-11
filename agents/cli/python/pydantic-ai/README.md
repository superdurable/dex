# dex-agents-pydantic-ai-cli

`dex-agents-pydantic-ai-cli` provides the `dex-agents-pydantic-ai` command. It
runs a [Pydantic AI](https://ai.pydantic.dev/) agent as a durable Dex agent
session and chats with it in your terminal. The shared runtime,
[`dex-agents-cli`](../runtime/README.md), starts or reuses `dexcli dev`,
connects the connectors MCP server, and runs the Worker. This package only
builds the agent with the
[Pydantic AI adapter](../../../python/pydantic-ai/README.md).

## Running it

The command needs Python 3.11 or later and a key for the model's provider:

```bash
export OPENAI_API_KEY=sk-...
uvx --from dex-agents-pydantic-ai-cli dex-agents-pydantic-ai
```

The first run asks the `init` questions and writes
`~/.dex/agents/pydantic-ai.yaml`. Then type a message at the `>` prompt.
Approve or reject tool calls with `y` or `n`, and press Ctrl-D to exit. The
session stays in Dex, and the command prints the `--resume` line that
continues it. See the [runtime README](../runtime/README.md#running-a-command)
for every subcommand and option, and [`agents/cli`](../../README.md) for the
configuration file.

The provider reads its key from its standard environment variable. The
package includes the OpenAI, Anthropic, and Google providers:

| Model prefix | Environment variables |
| --- | --- |
| `openai:` | `OPENAI_API_KEY`; `OPENAI_BASE_URL` for another endpoint |
| `anthropic:` | `ANTHROPIC_API_KEY`; `ANTHROPIC_BASE_URL` for another endpoint |
| `google-gla:` | `GOOGLE_API_KEY` |

The command never prints or stores a key.

## Configuration

The model defaults to `openai:gpt-5.2`; set `model` in the file or pass
`--model` to change it. It is a Pydantic AI model name, `<provider>:<model>`,
such as `anthropic:claude-opus-5-5`. A session's settings may change the model
and the instructions; each model call uses the values of its turn.
`system_prompt` replaces the default instructions. The `pydantic_ai` section
has no options yet, and any key in it is a configuration error.

## Tools

The agent's tools are the runtime's: its local tool source and the connector
tools of the connectors MCP server. Every call runs in its own Dex tool Step,
with the runtime's policy. Reads run at once, file writes and shell commands
wait for approval unless the shell allow list has the command, and the file
tools stay in the workspace. See the
[runtime README](../runtime/README.md) for the local tools.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the Pydantic AI adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

Develop with uv 0.12.2. The package resolves `dex-agents`, the adapter, the
runtime, and the shared test support from this repository by path. `uv.lock`
pins `pydantic-ai-slim` to the version the adapter's lock pins, and `ci.sh`
checks that. `ci.sh` installs the locked dependencies, checks lint and types,
and runs the tests against a private `dexcli dev`:

```bash
cd agents/cli/python/pydantic-ai
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the command in a pseudo-terminal with a private home and an
allow-listed environment. Pydantic AI's OpenAI provider calls the scripted
Responses API of [`../testing`](../testing); a refused proxy stops any other
request. A fake connectors MCP server serves a connector read and write. The
tests cover a chat turn and `--resume`, `read_file` without approval and
`write_file` after approval, a connector read and an approved connector write
that runs once, session settings that change the model, and a key in the
`pydantic_ai` section.
They also cover a `system_prompt` set in the session settings, which reaches
the next model request, and a rejected file write and connector write, which
never run.
