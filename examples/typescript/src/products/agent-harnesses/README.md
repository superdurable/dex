# Agent harnesses

One order-support agent, built four times: once with each agent framework that the Dex Agents
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

Shared by all four:

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

With Slack credentials, the agent also has `slack_post_thread_reply`; see
[Slack connector tools](#slack-connector-tools).

How each harness runs a turn:

- **OpenAI Agents SDK, Vercel AI SDK, Mastra.** Every model call is a `RunModel` Step, and every
  tool call is its own Step. A turn that looks up an order runs `RunModel`, `CallTool`,
  `ResumeTurn`, `RunModel`.
- **Claude Agent SDK.** One `RunModel` Step runs the Claude Code CLI for a turn segment. The read
  tool `look_up_order` runs inside that segment. `cancel_order` ends the segment, waits for
  approval, runs in its own Step, and the next segment resumes the Claude session.

## Slack connector tools

Every harness can post a reply to a Slack thread through the Slack Connector,
[`@superdurable/connector-slack`](https://www.npmjs.com/package/@superdurable/connector-slack) 0.2.0.
`ConnectorToolSource` from `@superdurable/dex-agents/connectors` offers the Connector operation
`postThreadReply` as the tool `slack_post_thread_reply`, and each harness passes it in its own
`toolSources` option, next to the order tools.

| Tool | Arguments | Effect | Approval | What it does |
| --- | --- | --- | --- | --- |
| `slack_post_thread_reply` | `channelId`, `threadTimestamp`, `text` | `non_idempotent_write` | Yes, set by the example | Posts one reply to an existing Slack thread |

- [`slack-connector-tools.ts`](../../shared/slack-connector-tools.ts) holds the wiring that every
  harness shares. `createSlackConnectorToolSources` wraps the operation with `connectorTool`. It
  passes the package's `dex-connector.json` manifest and its `PostThreadReplyInputSchema` validator.
- The manifest decides which operations an agent may use. Slack 0.2.0 marks only `postThreadReply`
  for agent use. Its reading operations, such as `listThreadMessages`, are not offered as tools.
- The manifest also decides the effect. `postThreadReply` is a Mutation without `agent.retry: safe`,
  so each call runs once, in its own `CallSingleAttemptTool` Step. The Call ID comes from that
  Step, and the post carries an idempotency key derived from it.
- The manifest does not ask for approval of `postThreadReply`. The example sets
  `requiresApproval: true`, so a person approves every post before it reaches Slack.
- `openSlackConnectionFromEnvironment` reads `SLACK_BOT_TOKEN` and `SLACK_USER_TOKEN` into the
  Connector's credential provider. The bot token posts. The user token reads a reply back when a
  retry must confirm an earlier post. The tokens stay in the Worker's memory and never enter Flow
  state.
- Without either variable, the process logs one line and the agents offer no Slack tool. Everything
  else runs as before.

## Configuration

Each harness reads its model from the environment. The provider key stays in the Worker's
environment and never enters Flow state.

| Harness | Variable | Default | Provider key |
| --- | --- | --- | --- |
| Claude Agent SDK | `DEX_CLAUDE_AGENT_SDK_MODEL` | `claude-opus-5-5` | `ANTHROPIC_API_KEY` |
| OpenAI Agents SDK | `DEX_OPENAI_AGENTS_MODEL` | `gpt-5-mini` | `OPENAI_API_KEY` |
| Vercel AI SDK | `DEX_VERCEL_AI_MODEL` | `gpt-5-mini`, through `@ai-sdk/openai` | `OPENAI_API_KEY` |
| Mastra | `DEX_MASTRA_MODEL` | `openai/gpt-5-mini`, a Mastra model router ID | The router's key, such as `OPENAI_API_KEY` |

The Claude Agent SDK example turns off every built-in Claude Code tool and passes
`settingSources: []`, so no settings file can approve a tool before Dex asks for approval.

`DEX_AGENT_SESSION_KEY` signs session tokens, as in the [AI Agent](../ai-agent) example. Set
`MASTRA_TELEMETRY_DISABLED=1` to turn off Mastra's telemetry.

## Run it

```bash
dexcli dev
cd examples/typescript
npm install
ANTHROPIC_API_KEY=... OPENAI_API_KEY=... npm start
# Optional: also offer slack_post_thread_reply.
SLACK_BOT_TOKEN=xoxb-... SLACK_USER_TOKEN=xoxp-... ANTHROPIC_API_KEY=... OPENAI_API_KEY=... npm start
```

To post a Slack reply, ask, for example, "Reply in Slack thread C0123456789 1710000000.000100:
Order 1001 has shipped." The call waits for approval like `cancel_order`.

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
is `claude-agent-sdk`, `openai-agents`, `vercel-ai`, or `mastra`:

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

- The scripted models follow the Python demo model's script: "cancel order <id>" calls
  `cancel_order`, other text with "order <id>" calls `look_up_order`, and the answer repeats the
  tool results. "reply in slack thread <channel> <timestamp>: <text>" calls
  `slack_post_thread_reply`.
- The OpenAI Agents SDK test uses a scripted `Model`. The Vercel AI SDK and Mastra tests use a
  scripted AI SDK language model.
- The Claude Agent SDK test runs the real Claude Code CLI bundled with `@anthropic-ai/claude-agent-sdk`
  against a scripted Messages API on 127.0.0.1, with a placeholder `ANTHROPIC_API_KEY`.
