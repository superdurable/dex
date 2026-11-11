# dex-agents-google-adk-cli

`dex-agents-google-adk-cli` provides the `dex-agents-google-adk` command. It
runs a [Google ADK](https://github.com/google/adk-python) agent as a durable
Dex agent session and chats with it in your terminal. The shared runtime,
[`dex-agents-cli`](../runtime/README.md), starts or reuses `dexcli dev`,
connects the connectors MCP server, and runs the Worker. This package only
builds the agent with the
[Google ADK adapter](../../../python/google-adk/README.md).

## Running it

The command needs Python 3.11 or later and a Gemini API key:

```bash
export GOOGLE_API_KEY=...
uvx --from dex-agents-google-adk-cli dex-agents-google-adk
```

The Google Gen AI SDK reads the key from `GOOGLE_API_KEY` or
`GEMINI_API_KEY`. To call Gemini on Vertex AI instead, set
`GOOGLE_GENAI_USE_VERTEXAI=true`, `GOOGLE_CLOUD_PROJECT`, and
`GOOGLE_CLOUD_LOCATION`, and sign in with Application Default Credentials.
The command passes these variables to the SDK and never prints or stores
them.

The first run asks the `init` questions and writes
`~/.dex/agents/google-adk.yaml`. Then type a message at the `>` prompt.
Approve or reject tool calls with `y` or `n`, and press Ctrl-D to exit. The
session stays in Dex, and the command prints the `--resume` line that
continues it. See the [runtime README](../runtime/README.md#running-a-command)
for every subcommand and option, and [`agents/cli`](../../README.md) for the
configuration file.

## Configuration

The model defaults to `gemini-2.5-flash`; set `model` in the file or pass
`--model` to change it. It is a Gemini model name. `system_prompt` replaces
the default instructions. Session settings that change `model` or
`system_prompt` apply from the session's next turn. The `google_adk` section
has no options yet, and any key in it is a configuration error.

## Tools

The agent is one ADK `LlmAgent`. Its tools are the runtime's connector tools
and local tools: files, shell commands, web fetch, and the browser. Each
call runs in its own Dex tool Step with the runtime's rules: reads run at
once, writes and shell commands wait for approval unless the shell allow list
has the command, and file tools stay in the workspace. See the
[runtime README](../runtime/README.md) for the tools and their rules.

## Development

Develop with uv 0.12.2. The package resolves `dex-agents`, the adapter, the
runtime, and the shared test support from this repository by path. `ci.sh`
installs the locked dependencies, checks that `google-adk` and `google-genai`
match the adapter's lock, checks lint and types, and runs the tests against a
private `dexcli dev`:

```bash
cd agents/cli/python/google-adk
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the command in a pseudo-terminal with a private home. ADK calls
the scripted Gemini API of [`../testing`](../testing) on 127.0.0.1 through
`GOOGLE_GEMINI_BASE_URL`; a refused proxy stops any other request. A fake
connectors MCP server serves a connector read and write. The tests cover a
chat turn and `--resume`, `read_file` without approval and `write_file` after
approval, a connector read and an approved connector write that runs once,
session settings that change the model, and a key in the `google_adk`
section.
