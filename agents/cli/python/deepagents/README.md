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

The agent runs on the LangGraph adapter, so every model call is a `RunModel`
Step and every tool call runs once in its own tool Step, after approval when
its policy asks for it.

| Deep Agents tool | In this command |
| --- | --- |
| `ls`, `read_file`, `write_file`, `edit_file`, `glob`, `grep` | Removed. The CLI core's local tools provide `read_file`, `write_file`, `edit_file`, `list_directory`, and `search_files` on the real workspace, with the CLI core's rules: reads run at once, writes wait for approval. |
| `execute` | Removed. The CLI core's `run_shell_command` runs in the workspace and waits for approval unless `local_tools.shell.allow` has the command. |
| `task` (subagents) | Excluded with the adapter's `excluded_tool_names`. A subagent would make its own model and tool calls inside one tool Step, without their own Steps or approvals. |
| `write_todos` | Kept, as a read-only tool that runs at once. Its result repeats the plan, so the plan stays in the conversation. |

- The CLI core's web fetch, browser, and connector tools are offered as well.
  `local_tools`, `approval.always`, and session settings work as for every
  command.
- Deep Agents' own file tools work on a virtual file system in the agent
  state, and their names are the CLI core's. The command replaces Deep
  Agents' `FilesystemMiddleware` with one that adds no tools. The session
  Flow excludes `task`, defers each tool call before any middleware tool
  wrapper, and gives every model call the turn's model through the adapter's
  `select_turn_chat_model`.
- LangChain's own todo tool updates the agent state with a graph `Command`.
  A tool Step returns only content, so this command's `write_todos` returns
  the plan as text instead.
- Deep Agents' summarization is replaced too. It would make an extra model
  call with the configuration file's model inside a `RunModel` Step, so a
  model from the session settings would not reach it. The session does not
  compact either: Dex compaction needs the model's context window, which the
  configuration does not set. A long session ends when its input no longer
  fits the model's context window; start a new session then.

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
cover a chat turn and `--resume`, `read_file` without approval and
`write_file` after approval, a connector read and an approved connector write
that runs once, a model change in the session settings, a key in the
`deepagents` section, the offered tools without Deep Agents' file and
subagent tools, and `write_todos`.
They also cover a `system_prompt` set in the session settings, which reaches
the next model request, and a rejected file write and connector write, which
never run.
The Deep Agents command also checks that its graph has no summarization and
that a model from the session settings reaches every model call.
