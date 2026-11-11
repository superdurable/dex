# Agent harnesses

One order-support agent, built five times: once with each agent framework that the Dex Agents
library adapts. Each adapter registers the same durable agent session Flow from
[`@superdurable/dex-agents`](https://www.npmjs.com/package/@superdurable/dex-agents). One session is
one Flow execution, and the session ID is the Flow ID. Dex keeps the conversation, waits for tool
approvals for as long as they take, and after a Worker crash reruns only the Step that was running.

| Harness | Adapter | Flow type | Files |
| --- | --- | --- | --- |
| Claude Agent SDK | `@superdurable/dex-agents-claude-agent-sdk` 0.4.0 | `ClaudeAgentSdkOrderSupportAgent` | [`claude-agent-sdk/`](./claude-agent-sdk) |
| OpenAI Agents SDK | `@superdurable/dex-agents-openai-agents` 0.4.0 | `OpenAIAgentsOrderSupportAgent` | [`openai-agents/`](./openai-agents) |
| Vercel AI SDK | `@superdurable/dex-agents-vercel-ai` 0.4.0 | `VercelAiOrderSupportAgent` | [`vercel-ai/`](./vercel-ai) |
| Mastra | `@superdurable/dex-agents-mastra` 0.4.0 | `MastraOrderSupportAgent` | [`mastra/`](./mastra) |
| Pi | `@superdurable/dex-agents-pi`, with the core, from [`agents/typescript`](../../../../../agents/typescript) until agents 0.5.0 is on npm | `PiOrderSupportAgent` | [`pi/`](./pi) |

Shared by all five:

- [`order-support/order-desk.ts`](./order-support/order-desk.ts): the domain. An in-memory order
  desk with order `1001` (2 notebooks, shipped) and order `1002` (1 desk lamp, processing), and the
  system instructions.
- [`order-support/order-support-routes.ts`](./order-support/order-support-routes.ts): the HTTP
  routes, the same for every harness.
- [`../../shared/slack-connector-tools.ts`](../../shared/slack-connector-tools.ts): the Slack
  Connector tool, shared with the [AI Agent](../ai-agent) example. See
  [Slack connector tools](#slack-connector-tools).

The agent, its tools, and its instructions match the Python agent harness examples in
`examples/python/agent-harnesses`, so docs can show both languages side by side. The Flow types
differ from the Python ones, so both can run against one Dex Server.

## The agent

| Tool | Arguments | Effect | Approval | What it does |
| --- | --- | --- | --- | --- |
| `look_up_order` | `order_id` | `read` | No | Returns the order's items and status |
| `cancel_order` | `order_id`, `reason` | `non_idempotent_write` | Yes, by default | Cancels an order that is still processing |

`cancel_order` waits in `AwaitToolApproval` until a person approves or rejects it. After approval it
runs at most once, in its own single-attempt tool Step. A rejected call never runs; the model reads
the rejection.

With a `support-slack` Slack connection, the agent also has `slack_post_thread_reply`; see
[Slack connector tools](#slack-connector-tools).

How each harness runs a turn:

- **OpenAI Agents SDK, Vercel AI SDK, Mastra, Pi.** Every model call is a `RunModel` Step, and
  every tool call is its own Step. A turn that looks up an order runs `RunModel`, `CallTool`,
  `ResumeTurn`, `RunModel`.
- **Claude Agent SDK.** One `RunModel` Step runs the Claude Code CLI for a turn segment. The read
  tool `look_up_order` runs inside that segment. `cancel_order` ends the segment, waits for
  approval, runs in its own Step, and the next segment resumes the Claude session.

The Pi example differs from the other adapters: Dex runs the agent loop itself, with the built-in
agent loop of `@superdurable/dex-agents`. Pi supplies the model call, through pi-ai, and the tools,
as Pi `AgentTool` objects. `cancel_order` declares `replay: "never"`, so it becomes a
`non_idempotent_write`.

## Slack connector tools

Each agent can also post a reply in a Slack thread through the Slack Connector
([`@superdurable/connector-slack`](https://www.npmjs.com/package/@superdurable/connector-slack)
0.2.0). The Connector's operations reach the agent through `ConnectorToolSource` from
`@superdurable/dex-agents/connectors`, passed to each framework adapter as `toolSources`. The
wiring is in [`slack-connector-tools.ts`](../../shared/slack-connector-tools.ts); each agent file
passes `toolSources: createSlackConnectorToolSources(slack)`.

| Tool | Slack operation | What Dex does |
| --- | --- | --- |
| `slack_post_thread_reply(channelId, threadTimestamp, text)` | `postThreadReply` | Waits in `AwaitToolApproval`, then runs once in its own tool Step under the Connector's Call ID. |

The Slack manifest decides what an agent may call: only operations marked `agent.exposed` can
become tools. The Slack Connector exposes only `postThreadReply`, so the agents cannot read a thread
through it. The manifest does not ask for approval, so the example passes `requiresApproval: true`:
every post waits for a person to approve it. `connectorTool` takes the package's
`dex-connector.json` manifest and its `PostThreadReplyInputSchema` validator.

The Slack connection comes from the Dex project configuration, like any Connector connection. At
startup the example calls `loadFromEnvironment` from `@superdurable/connectors-sdk/projectconfig`
and opens the connection named `support-slack` with the package's `newProjectConnection`:

- In local development, set `DEX_CONNECTOR_CONFIG_FILE` to the `connections.json` file that Dex Web
  in `dexcli dev` writes, by default `$HOME/.dex/connectors/connections.json`. It must hold a Slack
  connection named `support-slack`. To create it, open **Experimental > Connectors** in Dex Web,
  choose **+ Add**, pick **Slack**, name the connection `support-slack`, and authorize it. See
  [Add a connection](https://docs.superdurable.io/reference/web/local-connector-setup#add-a-connection).
- In a deployment, the `DEX_PROJECT_*` settings select the project configuration that the hosting
  platform or operator writes.

The Connector resolves the Slack credentials before each call, so they never enter Flow state. The
bot credential posts the reply. The user credential is used only when a Worker stopped during a
post: the Connector reads the thread to learn whether the reply was sent, and without it such a post
records an unknown outcome. Without a project configuration, with a `DEX_CONNECTOR_CONFIG_FILE`
that does not exist, or without a `support-slack` connection, the app logs one line and the agents
run without the Slack tool.

A post can wait for approval for a long time, and any Worker may run it after approval. Keep the
same Connector configuration, with the `support-slack` connection, on every Worker that runs these
Flow types while sessions wait for approval. A Worker without it offers no Slack tool and cannot
run an approved post.

Ask the agent to reply in a Slack thread and give it the channel ID and the thread timestamp, such
as "Reply in Slack thread C0123456789 1710000000.000100: Order 1002 ships on Thursday." The model
calls `slack_post_thread_reply`, and the post waits for approval like `cancel_order`. The
[AI Agent](../ai-agent) example's demo model takes the call directly:
`/tool slack_post_thread_reply {"channelId":"C0123456789","threadTimestamp":"1710000000.000100","text":"Order 1002 ships on Thursday."}`.

An invalid project configuration stops the process at startup, so a broken Slack setup is never
silent. Examples are a `connections.json` that is not mode `0600`, `DEX_CONNECTOR_CONFIG_FILE` set
together with `DEX_PROJECT_*` variables, or an incomplete set of `DEX_PROJECT_*` variables.

## Configuration

Each harness reads its model from the environment. The provider key stays in the Worker's
environment and never enters Flow state.

| Harness | Variable | Default | Provider key |
| --- | --- | --- | --- |
| Claude Agent SDK | `DEX_CLAUDE_AGENT_SDK_MODEL` | `claude-opus-5-5` | `ANTHROPIC_API_KEY` |
| OpenAI Agents SDK | `DEX_OPENAI_AGENTS_MODEL` | `gpt-5-mini` | `OPENAI_API_KEY` |
| Vercel AI SDK | `DEX_VERCEL_AI_MODEL` | `gpt-5-mini`, through `@ai-sdk/openai` | `OPENAI_API_KEY` |
| Mastra | `DEX_MASTRA_MODEL` | `openai/gpt-5-mini`, a Mastra model router ID | The router's key, such as `OPENAI_API_KEY` |
| Pi | `DEX_PI_MODEL` | `openai/gpt-5-mini`, a `<provider>/<model ID>` of pi-ai's built-in catalog | pi-ai reads the provider's key, such as `OPENAI_API_KEY` |

The Claude Agent SDK example turns off every built-in Claude Code tool and passes
`settingSources: []`, so no settings file can approve a tool before Dex asks for approval.

`DEX_AGENT_SESSION_KEY` signs session tokens, as in the [AI Agent](../ai-agent) example. Set
`MASTRA_TELEMETRY_DISABLED=1` to turn off Mastra's telemetry.

## Run it

```bash
dexcli dev
# Until agents 0.5.0 is on npm, build the core and the Pi package from this repository first.
(cd agents/typescript/core && npm ci && npm run build)
(cd agents/typescript/pi && npm ci && npm run build)
cd examples/typescript
npm install
ANTHROPIC_API_KEY=... OPENAI_API_KEY=... npm start
# Optional: also offer slack_post_thread_reply from the support-slack connection.
DEX_CONNECTOR_CONFIG_FILE=$HOME/.dex/connectors/connections.json ANTHROPIC_API_KEY=... OPENAI_API_KEY=... npm start
```

The Claude Agent SDK example authenticates only with `ANTHROPIC_API_KEY`:

- The Claude Code CLI gets its own environment, built by `buildClaudeCliEnvironment`. It holds
  `ANTHROPIC_API_KEY` and only the variables the CLI needs to run: `PATH`, `HOME`, `TMPDIR`, and
  the proxy variables when they are set. Other Claude credentials or provider settings in the
  Worker's environment never reach the CLI.
- Without `ANTHROPIC_API_KEY`, the process logs one line and does not register the Claude Flow or
  its routes.

The other harnesses still register without their keys; only their model calls fail.

## Routes

Each harness mounts the same routes under `/products/agent-harnesses/<harness>`, where `<harness>`
is `claude-agent-sdk`, `openai-agents`, `vercel-ai`, `mastra`, or `pi`:

| Route | Does |
| --- | --- |
| `POST /sessions` | Starts a session, with an optional first `{ message }`; returns `session_id` and `session_token` |
| `GET /sessions/:id/snapshot` | `get_agent_snapshot`: messages, pending approvals, and turn state |
| `POST /sessions/:id/messages` | `send_user_message` with `{ text }` |
| `POST /sessions/:id/interrupts/:interruptId/approve` | `resolve_interrupt`, approved |
| `POST /sessions/:id/interrupts/:interruptId/reject` | `resolve_interrupt`, rejected, with an optional `{ reason }` the model reads |

Every route under `/sessions/:id` checks the `X-Agent-Session-Token` header from `POST /sessions`
first, and answers 403 when it is missing or wrong. A route then answers 400 when a required field,
such as `text`, is missing or empty, and 409 when the session refuses the request or is closed.
Replace the token with your application's authentication.

```bash
base=http://localhost:8080/products/agent-harnesses/vercel-ai
curl -s -X POST $base/sessions -H 'content-type: application/json' -d '{"message":"Please cancel order 1002"}'
# {"session_id":"order-support-...","session_token":"..."}
curl -s $base/sessions/$ID/snapshot -H "X-Agent-Session-Token: $TOKEN"
curl -s -X POST $base/sessions/$ID/interrupts/$INTERRUPT_ID/approve -H "X-Agent-Session-Token: $TOKEN"
```

## Tests

`test/integ/agent-harness-<harness>.test.ts` drives each harness through these routes against a
real Dex Server, with no model key. Each runs a turn that calls `look_up_order`, a cancellation of
order `1002` that is rejected and never runs while the model reads the rejection, one that is
approved and runs exactly once, and the 403 and 400 responses.

Each also posts a Slack thread reply through the Slack Connector's mock,
`@superdurable/connector-slack/mock`, with no Slack credentials. The post waits for approval, and the
mock sees no call before it. After approval the mock sees exactly one call, in the session's Flow,
with no retry or replay and an idempotency key derived from its Call ID. A second approval answers
409 and changes nothing.

`test/workflow/slack-connector-tools.test.ts` checks the connection wiring: no tool without a
project configuration or without a `support-slack` connection, and one tool source from a local
connections file that has it.

- The scripted models follow the Python demo model's script: "cancel order <id>" calls
  `cancel_order`, other text with "order <id>" calls `look_up_order`, and the answer repeats the
  tool results. "reply in Slack thread <channel ID> <thread timestamp>: <text>" calls
  `slack_post_thread_reply`.
- The OpenAI Agents SDK test uses a scripted `Model`. The Vercel AI SDK and Mastra tests use a
  scripted AI SDK language model. The Pi test points a pi-ai Anthropic Messages model at the same
  scripted Messages API as the Claude Agent SDK test.
- The Claude Agent SDK test runs the real Claude Code CLI bundled with `@anthropic-ai/claude-agent-sdk`
  against a scripted Messages API on 127.0.0.1, with a placeholder `ANTHROPIC_API_KEY`.
