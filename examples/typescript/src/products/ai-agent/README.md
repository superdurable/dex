# AI Agent

A general, long-running AI Agent built on the Dex Agents library
([`@superdurable/dex-agents`](../../../../../agents/typescript/core)). One agent session is one Flow
execution, and the session ID is the Flow ID. The library owns the session's Steps, Attributes,
Channels, RPCs, and events; this example only declares the agent, configures the Worker, and mounts
HTTP routes and a browser UI. It is the TypeScript counterpart of the
[Python AI Agent example](../../../../python/dex_examples/products/ai-agent), with the same Flow type,
`AIAgentSession`, so run the two against different Dex Servers.

| Feature | How the example uses the library |
| --- | --- |
| Send, queue, and steer messages; delete a queued message; stop a turn | `send_user_message`, `steer_user_message`, `delete_queued_message`, `cancel_turn` |
| Questions | The `ask_user` built-in tool: choice buttons or a text box call `answer_question`; **Decline** calls `resolve_interrupt` |
| Durable waits | The `durable_wait` built-in tool, a Dex Timer that survives Worker restarts |
| Plan mode | `write_todos` drafts a plan in a planning turn; **Execute plan** or **Continue plan** calls `execute_plan` |
| Tool approvals | MCP tools without a trusted read-only or idempotent annotation wait for approval through `useChat` |
| MCP tools | `McpToolSource` in `toolSources`, with its resource and prompt tools; each tool call is its own Step |
| Connector tools | `ConnectorToolSource` in `toolSources` offers the Slack Connector's `postThreadReply` as `slack_post_thread_reply`, which waits for approval; see the [agent harnesses](../agent-harnesses#slack-connector-tools) |
| Compaction | A compaction policy summarizes older messages before the context window fills |
| Browser UI | `@superdurable/dex-agents/ui-stream` routes for the AI SDK `useChat` |
| Snapshot and history | `get_agent_snapshot` and `list_agent_messages` |

Files:

- [`ai-agent-flow.ts`](./ai-agent-flow.ts): the Stepwise agent definition.
- [`ai-agent-config.ts`](./ai-agent-config.ts): Worker configuration and the MCP configuration loader.
- [`scripted-model.ts`](./scripted-model.ts): the demo model `mock/dex`.
- [`notes-mcp-server.ts`](./notes-mcp-server.ts): a credential-free demo MCP server over stdio.
- [`../../shared/slack-connector-tools.ts`](../../shared/slack-connector-tools.ts): the Slack
  Connector tool, shared with the agent harness examples.
- [`controller.ts`](./controller.ts): HTTP routes.
- [`ui/`](./ui): the React page that uses `useChat`.

## Worker configuration

The allowed models, the system prompt, the context window, and the MCP servers are Worker
configuration, shared by every session. Each session picks one allowed model when it starts; its
session settings hold only `{"model": <name>}`, and the Worker maps the name to a model client with
`resolveModel`. A session cannot choose anything else, and a browser can never register an MCP
command, URL, or credential. The names match the Python example.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEX_AGENT_MODEL` | `mock/dex` | The default model. A `mock/<name>` model is the demo model, which needs no key. `openai/<model>`, such as `openai/gpt-5-mini`, calls OpenAI through the AI SDK with `OPENAI_API_KEY` from the Worker's environment |
| `DEX_AGENT_MODELS` | `mock/dex-mini` with the default demo model, else none | Comma-separated models a session may pick besides the default |
| `DEX_AGENT_SYSTEM_PROMPT` | a short general prompt | System instructions |
| `DEX_AGENT_CONTEXT_TOKENS` | `32000` | Context window for compaction |
| `DEX_AGENT_MCP_CONFIG` | the demo `notes` server | Path to a YAML file of MCP servers, in the Python example's format |
| `DEX_AGENT_SESSION_KEY` | random per process | Signs session tokens; a random key ends every token when the process restarts |
| `SLACK_BOT_TOKEN`, `SLACK_USER_TOKEN` | none | Slack credentials for `slack_post_thread_reply`. Without either, the process logs one line and the agent offers no Slack tool |

The MCP file is the Python example's format, so one file serves both examples:

```yaml
servers:
  - name: tickets
    transport: streamable_http
    url: https://tickets.example.com/mcp
    headers:
      Authorization: TICKETS_MCP_AUTHORIZATION   # the name of an environment variable, not the secret
    trust_read_only_annotations: true
  - name: files
    transport: stdio
    command: mcp-server-files
    args: ["/srv/reports"]
    env:
      FILES_TOKEN: FILES_TOKEN
```

Values under `env` and `headers` name Worker environment variables, so secrets stay out of the file.
Unknown keys are refused. `trust_read_only_annotations` is off by default: every tool of such a server
then waits for approval and runs at most once. Turn it on only for a server whose read-only and
idempotent hints you trust. The demo `notes` server trusts its hints, so `search_notes` runs at once.

The Stepwise loop lists the MCP tools before every model call, so a server that is down offers no
tools until it is back.

## Run it

The example installs the published Dex Agents library, `@superdurable/dex-agents` 0.4.0, from npm:

```bash
dexcli dev
cd examples/typescript
npm install
npm start
```

Open http://localhost:8080/products/ai-agent/. Pick a model in the start form. The page starts a session, puts its ID in the URL fragment,
and keeps its token in local storage, so a copied URL does not grant access; **New session** starts
another. The demo model answers with
`Local demo response: <text>` and reads slash commands:

| Command | Does |
| --- | --- |
| `/tool <name> <json>` | Calls a tool, such as `/tool notes__search_notes {"query":"Step"}` (runs at once) or `/tool notes__post_message {"channel":"#general","text":"hi"}` (waits for approval). With Slack credentials, `/tool slack_post_thread_reply {"channelId":"C0123456789","threadTimestamp":"1710000000.000100","text":"hi"}` waits for approval and then posts to Slack |
| `/ask <question>` | Asks a free-text question |
| `/choose <question> \| <choice> \| <choice>` | Asks a question with choices |
| `/wait <seconds> <reason>` | Starts a durable wait |
| `/slow <seconds> <text>` | Makes the model call take that long, to try a Worker kill |
| `/plan-stop <objective>`, `/plan-clear` | In plan mode: a plan that stops unfinished, or clears the plan |
| `/model` | Names the session's model |

Turn on **Plan mode** for a draft plan and **Execute plan** to run it. While a turn runs, new
messages are queued; **Steer** applies one at the next safe Step boundary and **Delete** removes it.

## Session access

`POST /sessions` creates the session ID on the server and returns `session_id` and `session_token`,
an HMAC-SHA256 of the ID keyed with `DEX_AGENT_SESSION_KEY`; nothing is stored. Every other route
compares the `X-Agent-Session-Token` header in constant time before it calls Dex, as the `ui-stream`
README requires, and answers 403 on a mismatch. The scheme matches the Python example.
The AI Agent routes send no CORS headers, so only the page's own origin can call them. Replace the
token with your application's authentication and authorization.

## Routes

Under `/products/ai-agent`:

| Route | Does |
| --- | --- |
| `GET /settings` | The default model and the models a session may pick |
| `POST /sessions` | Starts a session with `{ model }`, one of `/settings`' models (400 otherwise); returns `session_id` and `session_token` |
| `GET /api/chat/:id`, `POST /api/chat`, `GET /api/chat/:id/stream` | The `useChat` routes: stored messages, a message or approval answers, and reconnects with `x-agent-resume-token` |
| `GET /sessions/:id/snapshot` | `get_agent_snapshot` |
| `GET /sessions/:id/messages?before_sequence=` | `list_agent_messages`, 100 at a time |
| `POST /sessions/:id/messages` | Queues `{ text, plan_mode }` |
| `POST /sessions/:id/steer` | Steers with `{ message_id }` or `{ text }` |
| `DELETE /sessions/:id/queued-messages/:messageId` | Deletes a queued message |
| `POST /sessions/:id/cancel` | Cancels the active turn |
| `POST /sessions/:id/questions/:interruptId/answer` | Answers a question with `{ answer }` |
| `POST /sessions/:id/questions/:interruptId/decline` | Declines a question |
| `POST /sessions/:id/plan/execute` | Runs or continues the plan `{ revision }` |

A route answers 400 for a malformed body, 403 for a wrong session token, and 409 when the session
refuses the request, is busy, is closed, or already consumed the queued message.

## Tests

[`test/integ/ai-agent.test.ts`](../../../test/integ/ai-agent.test.ts) runs the Worker as a child
process against a real Dex Server with the demo model. It drives the example's routes: a session
that runs the model its start form picked (an unlisted model is 400), an approval
and its continuation through `/api/chat`, questions with choices, free text, and a decline, a steer
of a durable wait after deleting a queued message, plan execution, a reconnect with a resume token,
and the 400, 403, and 409 responses. It also covers a rejected approval, an MCP resource read,
a Slack thread reply that the Worker posts through the Slack Connector's mock exactly once after
approval,
compaction, the MCP configuration loader, a UI stream whose Dex reads fail mid-turn without
stopping the process, and Worker kills during a model call, a read tool Step, and a durable wait.
