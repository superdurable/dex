# @superdurable/dex-agents-vercel-ai-cli

`@superdurable/dex-agents-vercel-ai-cli` provides the `dex-agents-vercel-ai`
command. It runs a [Vercel AI SDK](https://ai-sdk.dev) agent as a durable Dex
agent session and chats with it in your terminal. The shared runtime,
[`@superdurable/dex-agents-cli`](../runtime/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, offers the local tools, and
runs the Worker. This package only builds the agent with the
[Vercel AI SDK adapter](../../../typescript/vercel-ai/README.md).

## Running it

The command needs Node.js 22 or 24 and a provider API key:

```bash
export OPENAI_API_KEY=sk-...
npx -p @superdurable/dex-agents-vercel-ai-cli dex-agents-vercel-ai
```

The first run asks the `init` questions and writes
`~/.dex/agents/vercel-ai.yaml`. Then type a message. Approve or reject tool
calls with `y` or `n`, and type `/exit` to leave. The session stays in Dex,
and the command prints the `--resume` line that continues it. See the
[runtime README](../runtime/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

## Models and keys

The model defaults to `openai/gpt-5.2`; set `model` in the file or pass
`--model` to change it. A model ID names its provider:

| Model ID | Provider package | Environment variables |
| --- | --- | --- |
| `openai/<model>`, such as `openai/gpt-5.2` | `@ai-sdk/openai`, Responses API | `OPENAI_API_KEY`, optional `OPENAI_BASE_URL` |
| `anthropic/<model>`, such as `anthropic/claude-sonnet-4-5` | `@ai-sdk/anthropic` | `ANTHROPIC_API_KEY`, optional `ANTHROPIC_BASE_URL` |

Any other model ID stops the command, or fails the turn when session settings
set it. The command never passes a model ID string to the AI SDK, so no model
call goes to the Vercel AI Gateway. It never prints or stores a key; the
provider package reads it from the environment on each model call.

## Configuration and tools

The `vercel_ai` section has no options yet, and any key in it is a
configuration error.

The agent offers the connectors MCP server's tools and the runtime's
[local tools](../runtime/README.md#local-tools), each call in its own tool Step
with the runtime's approval rules. The adapter takes one model and fixed
instructions per Flow, so the command passes it a language model,
**TurnLanguageModel**, that serves each model call with the running turn's
model and adds the turn's instructions as the system message. Each turn
therefore uses the model and instructions its session settings resolve to.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the Vercel AI SDK adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

`ci.sh` builds the core, the Vercel AI SDK adapter, and the runtime, which this
package installs from the repository, then typechecks and runs the integration
tests against a private `dexcli dev`:

```bash
cd agents/cli/typescript/vercel-ai
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home and an explicit environment allow list. Its
`@ai-sdk/openai` model calls `test/fixtures/mock-responses-api.ts`, a scripted
Responses API on 127.0.0.1, through `OPENAI_BASE_URL`, and its connector tools
come from `test/fixtures/fake-connectors-mcp-server.ts`.
