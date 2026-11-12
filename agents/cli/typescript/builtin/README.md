# @superdurable/dex-agents-builtin-cli

`@superdurable/dex-agents-builtin-cli` provides the `dex-agents-builtin`
command. It runs an agent with the built-in agent loop of
[`@superdurable/dex-agents`](../../../typescript/core/README.md), with no agent
framework, as a durable Dex agent session, and chats with it in your terminal.
The shared CLI core, [`@superdurable/dex-agents-cli-core`](../core/README.md),
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
[CLI core README](../core/README.md) for every subcommand and option, and
[`agents/cli`](../../README.md) for the configuration file.

## Models and keys

The model defaults to `openai/gpt-5.2`; set `model` in the file or pass
`--model` to change it. A model ID names its provider:

| Model ID | Provider package | Environment variables |
| --- | --- | --- |
| `openai/<model>`, such as `openai/gpt-5.2` | `@ai-sdk/openai`, Responses API | `OPENAI_API_KEY`, optional `OPENAI_BASE_URL` |
| `anthropic/<model>`, such as `anthropic/claude-sonnet-4-5` | `@ai-sdk/anthropic` | `ANTHROPIC_API_KEY`, optional `ANTHROPIC_BASE_URL` |

Any other model ID stops the command, or fails the turn when session settings
set it. The command never prints or stores a key; the provider package reads
it from the environment on each model call.

## Configuration and tools

The `builtin` section has no options yet, and any key in it is a
configuration error. Each turn uses the model and instructions its session
settings resolve to.

The agent offers the connectors MCP server's tools, the CLI core's
[local tools](../core/README.md#local-tools), and two built-in tools that the
session runs itself:

- `ask_user` asks you a question, which you answer in the terminal.
- `write_todos` replaces the session's plan, a short task list, with no
  approval.

Every model call is one `RunModel` Step, and every tool call is its own tool
Step with the CLI core's approval rules.

Plan mode is on. Use it through the terminal UI (`dexcli agents-tui`) or the
session API, not the plain chat, which sends `/plan` and `/run` as ordinary
messages. In the terminal UI, `/plan <message>` starts a planning turn: the
agent drafts a plan with `write_todos` and uses no other tool. `/run` approves
the draft, and the agent runs it with every tool.

The names `ask_user` and `write_todos` are reserved, so a connector or local
tool with one of these names fails the turn. This is a breaking change for
such a tool: rename the conflicting connector or local tool, or use another
harness.

## Development

`ci.sh` builds the core and the CLI core, which this package installs from the
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
