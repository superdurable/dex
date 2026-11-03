# AI Agent

This Python application is a general, long-running AI Agent built on
[Dex Agents](../../../agents/python/core/README.md) (`dex-agents`). It is a
Stepwise agent: the library's built-in agent loop, with no agent framework.
Every model call is one **RunModel** Step and every tool call is its own Step,
so a crash repeats at most the one call that was running.

The application code is short because the library owns the session: the
conversation, the message queue, steering, approvals, questions, durable
waits, plans, context compaction, and live events. The example adds four
things:

- **Worker configuration**: the models a session may pick, the system prompt,
  the context window, and the trusted MCP servers. The start form picks one
  allowed model, and the session's settings carry only its name.
- **MCP tools**: the library's MCP tool source, passed to the agent as a tool
  source, offers each server's tools and three tools that read MCP resources
  and prompts.
- **A local demo model**, `mock/dex`, a script for the library's
  `ScriptedModelClient`, so the example and its tests run without a provider
  key.
- **HTTP routes and a React UI**. Each route is one call on
  **AgentSessionClient** or the library's browser stream helpers.

The code is in
[`dex_examples/products/ai-agent`](../dex_examples/products/ai-agent):
`ai_agent_session.py` builds the Flow and `http_routes.py` serves the UI.

## The session Flow

The Flow type is **AIAgentSession**. One Flow execution is one session, and the
Flow ID is the session ID. The Steps, persistence, RPCs, and events are the
ones every Dex Agents session uses; see the library README for the full Flow
shape. The UI calls these RPCs through **AgentSessionClient**:

| User action | RPC |
| --- | --- |
| Send a message, or queue it while a turn runs | `send_user_message` |
| Steer a queued message, or steer with new text | `steer_user_message` |
| Delete a queued message | `delete_queued_message` |
| Stop the running turn | `cancel_turn` |
| Approve or reject a tool call, or decline a question | `resolve_interrupt` |
| Answer a question | `answer_question` |
| Execute a reviewed plan | `execute_plan` |
| Load the page, with the newest 200 messages | `get_agent_snapshot` |
| Load older messages | `list_agent_messages` |

Live text, reasoning summaries, tool activity, and plan updates arrive on the
session's **AgentEvents** Stream. The `/events` route serves it as Server-Sent
Events with the library's `dex_agents.ui_stream` helpers. A reconnecting
browser resumes from the last event it saw. The Stream is best effort: the
page reloads the snapshot after every turn and every eight seconds.

## Session tokens

A session ID is not a secret: it appears in the page URL. So every route that
names a session checks the session's token first. `POST /sessions` picks a new
session ID and returns `session_id` and `session_token`. Every route that
takes a `session_id` needs that token in the `X-Agent-Session-Token` header,
and answers 403 without it or with another session's token.

The token is an HMAC-SHA256 of the session ID under `DEX_AGENT_SESSION_KEY`,
so the example stores no token, and every process with the same key accepts
it. Without the variable, the process picks a random key when it starts, so a
restart ends access to the sessions started before it. The UI keeps each
token in the browser's local storage and reads the event stream with `fetch`,
because `EventSource` cannot send a header. The AI Agent routes send no CORS
headers, so only the example's own page can call them.

A real application uses its own sign-in instead and checks that the signed-in
user may use the session before every call.

## Features

- **Queue and steer.** A message sent while a turn runs waits in the queue.
  The user can delete it, or steer with it. Steering never stops a model call
  or tool call that has started. The turn ends at its next safe Step boundary,
  calls that have not started are cancelled, and a new turn starts with the
  steered messages. **Stop** ends the turn without a new one.
- **Questions.** The `ask_user` built-in tool asks the user a question, with
  optional choices. The UI shows choice buttons or a text box, and the answer
  becomes the tool's result.
- **Durable waits.** The `durable_wait` built-in tool waits on a Dex Timer. No
  Worker holds the wait. Steering or **Stop** interrupts it; a queued message
  does not.
- **Plans.** With **Plan mode**, the agent can only call `write_todos`, and it
  drafts a plan for review. **Execute plan** runs the reviewed revision with
  every tool. A plan that stops with unfinished tasks stays active and offers
  **Continue plan**. While a plan is active, other turns can call every tool;
  only a draft plan limits a turn to `write_todos`.
- **Approvals.** MCP tools that are not read-only or idempotent by their
  annotations wait for **Approve** or **Reject** before they run, and they run
  at most once.
- **Compaction.** At 85 percent of the context window, a **CompactContext**
  Step summarizes older messages. The model then sees the summary and the
  recent messages. Every message stays in the session: compaction shortens
  the model input, not the stored history, so a long session's state grows
  toward the Dex Server's run budget. The
  [agent deployment guide](https://docs.superdurable.io/production/agents#limits)
  gives the bound.

## Run locally

Start Dex on server-zd, install the Python dependencies, and build the React
application. **dexcli dev --engine zd** needs **dex-server-zd** from a server-zd
release on your PATH, or **--zd-binary**.

```bash
dexcli dev --engine zd
cd examples/python
uv sync --locked
cd ai-agent
npm ci
npm run build
cd ..
export DEX_AGENT_MCP_CONFIG="$PWD/ai-agent/mcp-servers.local.yaml"
export DEX_AGENT_SESSION_KEY="$(openssl rand -hex 32)"
uv run --frozen python main.py
```

Open [http://127.0.0.1:8080/products/ai-agent/](http://127.0.0.1:8080/products/ai-agent/).
The first page shows the Worker's configuration. Choose **Start AI Agent** to
start a session.

The local MCP configuration starts credential-free search, Slack, Google Docs,
and web demo servers. Reads run at once. Demo Slack posts and Google Doc
creation wait for approval. The web server's read-only `fetch_page` tool
downloads a public page and reports its progress in three paced stages.

The `mock/dex` model replies `Local demo response: <text>` and reads slash
commands, so you can try every feature without a key:

| Command | What the agent does |
| --- | --- |
| `/tool <name> <json>` | Calls one tool, such as `/tool search__web_search {"query":"dex"}` |
| `/ask <question>` | Asks you a question |
| `/choose <question> \| <choice> \| <choice>` | Asks with choice buttons |
| `/wait <seconds> <reason>` | Waits on a durable Timer |
| `/slow <seconds> <text>` | Makes the model call take that long |
| `/plan-stop <objective>` | In plan mode, drafts a plan whose execution stops early |
| `/plan-clear` | In plan mode, clears the plan |

Press **Command/Ctrl+Enter** or **Alt+Enter** to send.

## Worker configuration

The allowed models, the prompt, and the MCP servers are Worker configuration,
shared by every session. A session picks one allowed model when it starts:
the start form sends its name, the server checks it against the list, and the
session stores `{"model": "<name>"}` as its session settings. The Worker maps
the name to a model client with `AgentDefinition.resolve_model`, so no
credential or server command ever comes from a browser or from Flow input.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEX_AGENT_MODEL` | `mock/dex` | The default model: a LiteLLM model name, such as `openai/gpt-5-mini`, or a `mock/` demo model |
| `DEX_AGENT_MODELS` | `mock/dex-mini` with the demo default, else none | More models a session may pick, separated by commas |
| `DEX_AGENT_SYSTEM_PROMPT` | a general prompt | The agent's instructions |
| `DEX_AGENT_CONTEXT_TOKENS` | `32000` | The context window that compaction keeps the model input under |
| `DEX_AGENT_MCP_CONFIG` | none | The MCP server file |
| `DEX_AGENT_SESSION_KEY` | random at each start | The key that signs session tokens; keep it set so tokens survive a restart |

Add the provider credential that LiteLLM reads to `examples/.env`, for example
`OPENAI_API_KEY="..."`, and restart the Worker. The credential stays in the
Worker's environment. It is never stored in an Attribute, a message, or the
Flow history.

A changed prompt or model takes effect at the next model call of every session,
including turns that are waiting, because Dex does not replay a Flow.

## Configure MCP servers

Copy [`mcp-servers.example.yaml`](./mcp-servers.example.yaml), edit the trusted
servers, and point the Worker at it:

```bash
export BRAVE_API_KEY="..."
export SLACK_MCP_AUTHORIZATION="Bearer ..."
export GOOGLE_DOCS_MCP_AUTHORIZATION="Bearer ..."
export DEX_AGENT_MCP_CONFIG="$PWD/ai-agent/mcp-servers.yaml"
```

The Brave entry uses
[`@brave/brave-search-mcp-server`](https://github.com/brave/brave-search-mcp-server).
The Slack and Google Docs entries are transport examples: replace their URLs
with servers from the [official MCP Registry](https://registry.modelcontextprotocol.io/).

Configuration keys. The Worker refuses any other key, including the per-tool
`tools` settings of earlier versions (`read_only`, timeouts, and retries):
Dex Agents picks retries and approval from each tool's effect.

- `transport`: `stdio` or `streamable_http`.
- `command`, `args`, `cwd`: the stdio server's process.
- `url`: the Streamable HTTP endpoint.
- `env`: child-process variable to Worker environment-variable name.
- `headers`: HTTP header to Worker environment-variable name.
- `trust_read_only_annotations`: let the server's `readOnlyHint` and
  `idempotentHint` annotations make a tool `read` or `idempotent_write`, so it
  runs without approval and is retried after a crash. Off by default, so every
  tool of the server waits for approval and runs at most once.

Tools are named `<server>__<tool>`. The tool source's
`mcp_list_resources_and_prompts`, `mcp_read_resource`, and `mcp_get_prompt`
tools read a server's resources and prompts. A server that cannot be listed
is left out of the model call and tried again on the next one; when every
server is down, the agent answers without MCP tools. MCP sampling and roots
are not enabled.

## Crashes and restarts

Dex stores the conversation, the plan, and each pending approval, timer, and
question in the session, so you can hard-kill the engine and the Worker at any
point. Restart the Worker in the same shell, so `DEX_AGENT_SESSION_KEY` stays
the same and the page keeps its session:

```bash
kill -9 $(pgrep -f 'dex-server-zd dev')   # the engine; dexcli dev exits with it
kill -9 $(pgrep -f 'python main.py')      # the Worker
dexcli dev --engine zd
uv run --frozen python main.py
```

- **While the agent waits** for a message, an approval, a durable timer, or an
  answer, the same session resumes with everything it had. A timer that came
  due while the engine was down fires after it starts.
- **A model call** cut off by a crash runs again; nothing else repeats. After
  three failed attempts the turn ends, the session records the error in
  `last_turn_error`, and the next message starts a new turn.
- **A read-only tool call** cut off by a crash runs again.
- **A write tool call** cut off by a crash never runs again. The model sees that
  its outcome is unknown and decides what to do with the user. Each MCP call
  carries an idempotency key, so a server can drop a duplicate.

## Change code while a session waits

Dex does not replay a Flow. A waiting session runs its next Step with the code
the Worker has when that Step starts. To try it with the local MCP
configuration:

1. Send `/tool slack__post_message {"channel":"#eng","message":"hello"}` and
   leave the approval waiting.
2. Stop the Worker and run
   `patch -p1 < ai-agent/changes/report-tool-results.patch`. The change makes
   the demo model report tool results as `Done: <result>`.
3. Start the Worker and approve. The waiting session's next model call uses the
   new code and replies `Done: ...`.
4. Run `patch -p1 -R < ai-agent/changes/report-tool-results.patch` to undo it.

Keep the Flow type, `AIAgentSession`, stable: running sessions resume by it.

## Tests

`tests/integ/test_ai_agent.py` runs every feature against a real Dex engine
with the demo model. `tests/integ/test_ai_agent_restarts.py` starts a private
server-zd through **dexcli dev**, runs `main.py` as the only Worker, and
hard-kills them. Set **DEX_SERVER_ZD_BINARY** to a `dex-server-zd` executable;
without it the restart tests are skipped. Set **DEXCLI_PATH** to a `dexcli`
executable, or the tests build one from `cli/` with Go.

```bash
DEX_SERVER_ZD_BINARY=/path/to/dex-server-zd \
  uv run --frozen pytest tests/integ/test_ai_agent_restarts.py
```
