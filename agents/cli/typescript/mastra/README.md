# @superdurable/dex-agents-mastra-cli

`@superdurable/dex-agents-mastra-cli` provides the `dex-agents-mastra`
command. It runs a [Mastra](https://mastra.ai) agent as a durable Dex agent
session and chats with it in your terminal. The shared runtime,
[`@superdurable/dex-agents-cli`](../runtime/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, offers the local tools, and
runs the Worker. This package only builds the agent with the
[Mastra adapter](../../../typescript/mastra/README.md).

## Running it

The command needs Node.js 22 or 24 and a provider API key:

```bash
export OPENAI_API_KEY=sk-...
npx -p @superdurable/dex-agents-mastra-cli dex-agents-mastra
```

The first run asks the `init` questions and writes
`~/.dex/agents/mastra.yaml`. Then type a message. Approve or reject tool calls
with `y` or `n`, and type `/exit` to leave. The session stays in Dex, and the
command prints the `--resume` line that continues it. See the
[runtime README](../runtime/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

## Models and keys

The model defaults to `openai/gpt-5.2`; set `model` in the file or pass
`--model` to change it. The model ID goes to Mastra's model router unchanged,
so any `<provider>/<model>` ID that Mastra knows works, such as
`anthropic/claude-sonnet-4-5`. Mastra reads the provider's standard
environment variable, such as `OPENAI_API_KEY` (with optional
`OPENAI_BASE_URL`) or `ANTHROPIC_API_KEY`. The command never prints or stores
a key.

The package pins `@mastra/core` to the version the Mastra adapter's lockfile
tests, and `ci.sh` fails when the two lockfiles differ. Mastra brings its own
copies of `@ai-sdk/provider`, 3.x and 4.x, for its model router; the command
does not call the AI SDK itself.

## Configuration and tools

The `mastra` section has no options yet, and any key in it is a configuration
error.

The agent offers the connectors MCP server's tools and the runtime's
[local tools](../runtime/README.md#local-tools), each call in its own tool Step
with the runtime's approval rules. The Mastra agent's model and instructions
are functions that Mastra calls in every model call, so each turn uses the
model and instructions its session settings resolve to.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the Mastra adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

`ci.sh` builds the core, the Mastra adapter, and the runtime, which this
package installs from the repository, then typechecks and runs the integration
tests against a private `dexcli dev`:

```bash
cd agents/cli/typescript/mastra
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home and an explicit environment allow list. Mastra's
OpenAI model calls `test/fixtures/mock-responses-api.ts`, a scripted Responses
API on 127.0.0.1, through `OPENAI_BASE_URL`, and the connector tools come from
`test/fixtures/fake-connectors-mcp-server.ts`.
