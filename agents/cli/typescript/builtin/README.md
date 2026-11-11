# @superdurable/dex-agents-builtin-cli

`@superdurable/dex-agents-builtin-cli` provides the `dex-agents-builtin`
command. It runs an agent with the built-in Stepwise loop of
[`@superdurable/dex-agents`](../../../typescript/core/README.md), with no agent
framework, as a durable Dex agent session, and chats with it in your terminal.
The shared runtime, [`@superdurable/dex-agents-cli`](../runtime/README.md),
starts or reuses `dexcli dev`, connects the connectors MCP server, offers the
local tools, and runs the Worker. This package only builds the agent with the
core's AI SDK model client.

## Running it

The command needs Node.js 22 or 24 and a provider API key:

```bash
export OPENAI_API_KEY=sk-...
npx -p @superdurable/dex-agents-builtin-cli dex-agents-builtin
```

The first run asks the `init` questions and writes
`~/.dex/agents/builtin.yaml`. Then type a message. Approve or reject tool
calls with `y` or `n`, and type `/exit` to leave. The session stays in Dex,
and the command prints the `--resume` line that continues it. See the
[runtime README](../runtime/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

## Models and keys

The model defaults to `openai/gpt-5-mini`; set `model` in the file or pass
`--model` to change it. A model ID names its provider:

| Model ID | Provider package | Environment variables |
| --- | --- | --- |
| `openai/<model>`, such as `openai/gpt-5-mini` | `@ai-sdk/openai`, Responses API | `OPENAI_API_KEY`, optional `OPENAI_BASE_URL` |
| `anthropic/<model>`, such as `anthropic/claude-sonnet-4-5` | `@ai-sdk/anthropic` | `ANTHROPIC_API_KEY`, optional `ANTHROPIC_BASE_URL` |

Any other model ID stops the command, or fails the turn when session settings
set it. The command never prints or stores a key; the provider package reads
it from the environment on each model call.

## Configuration and tools

The `builtin` section has no options yet, and any key in it is a
configuration error. Each turn uses the model and instructions its session
settings resolve to.

The agent offers the connectors MCP server's tools, the runtime's
[local tools](../runtime/README.md#local-tools), and the `ask_user` built-in
tool, whose questions you answer in the terminal. Every model call is one
`RunModel` Step, and every tool call is its own tool Step with the runtime's
approval rules.

## Development

`ci.sh` builds the core and the runtime, which this package installs from the
repository, then typechecks and runs the integration tests against a private
`dexcli dev`:

```bash
cd agents/cli/typescript/builtin
DEXCLI=/path/to/dexcli ./ci.sh
```

`dex-server-zd` must sit next to `dexcli`. The tests run the command as a child
process with a private home and an explicit environment allow list. Its
`@ai-sdk/openai` model calls `test/fixtures/mock-responses-api.ts`, a scripted
Responses API on 127.0.0.1, through `OPENAI_BASE_URL`, and its connector tools
come from `test/fixtures/fake-connectors-mcp-server.ts`.
