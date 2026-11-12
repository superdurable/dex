# dex-agents-deepagents-cli

`dex-agents-deepagents-cli` provides the `dex-agents-deepagents` command. It
runs a [Deep Agents](https://github.com/langchain-ai/deepagents) agent as a
durable Dex agent session and chats with it in your terminal. The shared
CLI core, [`dex-agents-cli-core`](../core/README.md), starts or reuses
`dexcli dev`, connects the connectors MCP server, and runs the Worker. This
package only builds the agent with `create_deep_agent` and runs it on the
[LangGraph adapter](../../../python/langgraph/README.md).

## Running it

The command needs Python 3.11 or later and a key for the model's provider.
The default model is OpenAI's, so set `OPENAI_API_KEY`:

```bash
export OPENAI_API_KEY=sk-...
uvx --from dex-agents-deepagents-cli dex-agents-deepagents
```

The first run asks the `init` questions and writes
`~/.dex/agents/deepagents.yaml`. Then type a message at the `>` prompt.
Approve or reject tool calls with `y` or `n`, and press Ctrl-D to exit. The
session stays in Dex, and the command prints the `--resume` line that
continues it. See the [CLI core README](../core/README.md#running-a-command)
for every subcommand and option, and [`agents/cli`](../../README.md) for the
configuration file.

The command never prints or stores provider keys. The chat model reads its
key from the provider's standard environment variable.

## Configuration

`model` is a LangChain `provider:model` ID, as `init_chat_model` takes it. It
defaults to `openai:gpt-5.2`; set `model` in the file or pass `--model` to
change it. An `openai:` model uses the Responses API, as Deep Agents does.

| Provider | Model example | Environment |
| --- | --- | --- |
| OpenAI | `openai:gpt-5.2` | `OPENAI_API_KEY`, optional `OPENAI_BASE_URL` |
| Anthropic | `anthropic:claude-opus-5-5` | `ANTHROPIC_API_KEY` |
| Google | `google_genai:gemini-2.5-flash` | `GOOGLE_API_KEY` |

Other providers need their LangChain integration package, such as
`uvx --from dex-agents-deepagents-cli --with langchain-mistralai dex-agents-deepagents`.

`system_prompt` replaces the default instructions. Session settings may
change the model and the instructions from the next turn on. The
`deepagents` section has no options yet, and any key in it is a configuration
error.

## Deep Agents' tools in Dex

The agent keeps Deep Agents' own tools and behavior. The CLI core adds the
connector, web fetch, and browser tools on top. The agent runs on the
LangGraph adapter, so every model call is a `RunModel` Step and every tool
call runs in its own tool Step, after approval when its policy asks for it.

| Deep Agents tool | In this command |
| --- | --- |
| `ls`, `read_file`, `glob`, `grep` | Reads of the real workspace files. They run at once. |
| `write_file`, `edit_file`, `delete` | Writes of the real workspace files. They run at once unless `approval.always` names the tool. |
| `execute` | Runs a command with `/bin/sh` in the workspace. It runs at once unless `approval.always` names it; `local_tools.shell.allow` has no effect. |
| `task` | Runs a subagent inside the call's tool Step. It runs at once unless `approval.always` names any Deep Agents tool, because the subagent may call it without its own approval. |
| `write_todos` | Updates the todo list in the agent's state. It runs at once. |

- Deep Agents asks for no approval unless `interrupt_on` names a tool, and
  this command follows it: `approval.always` plays the role of
  `interrupt_on`. Connector writes always wait for approval.
- The file tools use Deep Agents' `LocalShellBackend` on the host's paths,
  rooted at the workspace, so a file tool and `execute` name a file the same
  way. Paths are absolute. A path that resolves outside the workspace fails
  with an error the model sees, and `ls`, `glob`, and `grep` leave out
  entries whose real path is outside it, such as a link to a file elsewhere.
  `execute` is not confined.
- `local_tools.files` and `local_tools.shell.enabled` switch the tools on and
  off per turn. `task` needs both, because a subagent uses both.
- Each tool Step attempt runs Deep Agents' tool middleware around the tool,
  such as `FilesystemMiddleware`'s eviction of a large result to a file. A
  retried attempt runs it again. The
  session Flow sets the adapter's `should_defer_tool_calls_before_middleware`
  for that. Deep Agents writes evicted results and long user messages under
  the paths `/large_tool_results/` and `/conversation_history/`, which its
  file tools read. Each session keeps them in its own directory,
  `~/.dex/agents/deepagents-artifacts/<SHA-256 of the session ID>/`, mode
  0700, so two sessions never see each other's files and a restarted Worker
  on the same machine finds them.
- `edit_file`, `execute`, and `task` calls are never retried, so a Worker that
  dies during one records `outcome_unknown`. `write_file` and `delete` calls
  may run again after a failure.
- `write_todos` is LangChain's `TodoListMiddleware` tool. It returns a graph
  `Command` that the adapter records and applies. Each turn starts from the
  stored messages, so the todo list lasts for one turn; the plan stays in the
  conversation.
- `task` runs the subagent's model and tool calls inline, in one tool Step.
  They have no Steps or approvals of their own, use the configuration file's
  model even when session settings change it, and do not count in the
  session's usage. Connector tools are not offered to a subagent.
- A connector tool whose name a Deep Agents tool has gets the prefix
  `connector_`, such as `connector_read_file`. The other connector tools keep
  their names.
- Every model call gets the turn's instructions, the workspace path, and the
  turn's model through the adapter's `select_turn_chat_model`.
- Deep Agents' summarization is turned off for now. It would make an extra
  model call with the configuration file's model inside a `RunModel` Step, so
  a model from the session settings would not reach it. The session does not
  compact either: Dex compaction needs
  the model's context window, which the configuration does not set. A long
  session ends when its input no longer fits the model's context window;
  start a new session then.

## Images

`/image <path>` attaches an image file to the next message. The command gives
the LangChain adapter the media store of `media.store`, so the model sees the
image, and images that tools return go to the same store. See
[Images](../../README.md#images).

## Development

Develop with uv 0.12.2. The package resolves `dex-agents`, the LangGraph
adapter, the CLI core, and the test support package from this repository by
path. `ci.sh` installs the locked dependencies, checks that LangChain and
LangGraph match the adapter's lock, checks lint and types, and runs the tests
against a private `dexcli dev`:

```bash
cd agents/cli/python/deepagents
DEXCLI=/path/to/dexcli ./ci.sh
```

The tests run the command in a pseudo-terminal with a private home. The chat
model calls the scripted Responses API of
[`dex-agents-cli-testing`](../testing) on 127.0.0.1; a refused proxy stops
any other request, and no provider variable of the host reaches the command.
A fake connectors MCP server serves a connector read and write. The tests
cover a chat turn and `--resume`; `read_file`, `write_file`, and `edit_file` on
real workspace files without approval; `execute` in the workspace; a path
outside the workspace, which is refused; `write_file` waiting for approval
when `approval.always` names it, and rejected file and connector writes that
never run; a connector read and an approved connector write that runs once; a
connector tool named `read_file`, which the model sees as
`connector_connector_read_file` beside a connector tool named
`connector_read_file`; `write_todos`; a `task` subagent that writes a
workspace file inside one tool Step, and a `task` that waits for approval
when `approval.always` names `read_file`; and a large `execute` result that
lands in the session's own artifacts directory. Backend tests cover links to
files outside the workspace, which `ls`, `glob`, and `grep` leave out with
and without ripgrep and which reads and writes refuse, and two sessions that
write the same large-result path without seeing each other's file.
They also cover the offered tools, which are Deep Agents' own and none of
the CLI core's file and shell tools; switched-off file and shell tools, which
are not offered; a model change and a `system_prompt` in the session
settings, which reach the next model request; and a key in the `deepagents`
section. The command also checks that its graph has no summarization, that
its tool rules cover every tool of the graph, and that a model from the
session settings reaches every model call.
