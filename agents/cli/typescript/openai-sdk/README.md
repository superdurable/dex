# @superdurable/dex-agents-openai-sdk-cli

`@superdurable/dex-agents-openai-sdk-cli` provides the `dex-agents-openai-sdk`
command. It runs an
[OpenAI Agents SDK](https://openai.github.io/openai-agents-js/) agent as a
durable Dex agent session and chats with it in your terminal. The shared
CLI core, [`@superdurable/dex-agents-cli-core`](../core/README.md), starts or
reuses `dexcli dev`, connects the connectors MCP server, offers the local
tools, and runs the Worker. This package only builds the agent with the
[OpenAI Agents SDK adapter](../../../typescript/openai-agents/README.md).

## Running it

The command needs Node.js 22 or 24 and an OpenAI API key:

```bash
export OPENAI_API_KEY=sk-...
npx -p @superdurable/dex-agents-openai-sdk-cli dex-agents-openai-sdk
```

The first run asks the `init` questions and writes
`~/.dex/agents/openai-sdk.yaml`. Then type a message. Approve or reject tool
calls with `y` or `n`, and type `/exit` to leave. The session stays in Dex,
and the command prints the `--resume` line that continues it. See the
[CLI core README](../core/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

## Models and keys

The model defaults to `gpt-5.2`; set `model` in the file or pass `--model`
to change it. The model is an OpenAI model name. An `openai/` prefix, as in
`openai/gpt-5.2`, is accepted and dropped. The SDK's `OpenAIProvider` calls
the Responses API and reads `OPENAI_API_KEY` and, optionally,
`OPENAI_BASE_URL`. The command never prints or stores the key.

The SDK's trace upload to the OpenAI platform is off, because the Dex session
already records every model call and tool call.

The terminal UI's `/model` suggests `gpt-5.2`, `gpt-5-mini`, and `gpt-5-nano`.
`/effort`, the `reasoning_effort` session setting, takes `low`, `medium`, or
`high`, and becomes the SDK's `modelSettings.reasoning.effort` of every model
call in the turn. Without it, the model's own default applies.

## Configuration and tools

The `openai` section has no options yet, and any key in it is a configuration
error.

The agent offers the connectors MCP server's tools and the CLI core's
[local tools](../core/README.md#local-tools), each call in its own tool Step
with the CLI core's approval rules. Its instructions are a function of the
running turn, and it sets no model, so the SDK asks the command's model
provider, **TurnModelProvider**, in each model call. Each turn therefore uses
the model and instructions its session settings resolve to. The adapter lists
the connector tools in every model call rather than when the Flow is built, so
connector tools that appear while the command runs need no restart.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the OpenAI Agents SDK adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

`ci.sh` builds the core, the OpenAI Agents SDK adapter, and the CLI core, which
this package installs from the repository, then typechecks and runs the
integration tests against a private `dexcli dev`:

```bash
cd agents/cli/typescript/openai-sdk
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home and an explicit environment allow list. The SDK
calls `test/fixtures/mock-responses-api.ts`, a scripted Responses API on
127.0.0.1, through `OPENAI_BASE_URL`, and the connector tools come from
`test/fixtures/fake-connectors-mcp-server.ts`.
