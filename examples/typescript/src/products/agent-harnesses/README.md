# Agent harnesses

One order-support agent, built four times: once with each agent framework that the Dex Agents
library adapts. Each adapter registers the same durable agent session Flow from
[`@superdurable/dex-agents`](https://www.npmjs.com/package/@superdurable/dex-agents). One session is
one Flow execution, and the session ID is the Flow ID. Dex keeps the conversation, waits for tool
approvals for as long as they take, and after a Worker crash reruns only the Step that was running.

| Harness | Adapter | Flow type | Files |
| --- | --- | --- | --- |
| Claude Agent SDK | `@superdurable/dex-agents-claude-agent-sdk` 0.3.0 | `ClaudeAgentSdkOrderSupportAgent` | [`claude-agent-sdk/`](./claude-agent-sdk) |
| OpenAI Agents SDK | `@superdurable/dex-agents-openai-agents` 0.3.0 | `OpenAIAgentsOrderSupportAgent` | [`openai-agents/`](./openai-agents) |
| Vercel AI SDK | `@superdurable/dex-agents-vercel-ai` 0.3.0 | `VercelAiOrderSupportAgent` | [`vercel-ai/`](./vercel-ai) |
| Mastra | `@superdurable/dex-agents-mastra` 0.3.0 | `MastraOrderSupportAgent` | [`mastra/`](./mastra) |

Shared by all four:

- [`order-support/order-desk.ts`](./order-support/order-desk.ts): the domain. An in-memory order
  desk with order `1001` (2 notebooks, shipped) and order `1002` (1 desk lamp, processing), and the
  system instructions.
- [`order-support/order-support-routes.ts`](./order-support/order-support-routes.ts): the HTTP
  routes, the same for every harness.

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

How each harness runs a turn:

- **OpenAI Agents SDK, Vercel AI SDK, Mastra.** Every model call is a `RunModel` Step, and every
  tool call is its own Step. A turn that looks up an order runs `RunModel`, `CallTool`,
  `ResumeTurn`, `RunModel`.
- **Claude Agent SDK.** One `RunModel` Step runs the Claude Code CLI for a turn segment. The read
  tool `look_up_order` runs inside that segment. `cancel_order` ends the segment, waits for
  approval, runs in its own Step, and the next segment resumes the Claude session.

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
```

The Claude Agent SDK example authenticates only with `ANTHROPIC_API_KEY`. Without it, the process
logs one line and does not register the Claude Flow or its routes, so the Claude Code CLI never
falls back to another login. The other harnesses still register without their keys; only their
model calls fail.

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

Every route under `/sessions/:id` needs the `X-Agent-Session-Token` header from `POST /sessions`, and
answers 403 without it. A route answers 400 for a malformed body, and 409 when the session refuses
the request or is closed. Replace the token with your application's authentication.

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
order `1002` that is rejected and never runs, one that is approved and runs once, and the 403 and
400 responses.

- The scripted models follow the Python demo model's script: "cancel order <id>" calls
  `cancel_order`, other text with "order <id>" calls `look_up_order`, and the answer repeats the
  tool results.
- The OpenAI Agents SDK test uses a scripted `Model`. The Vercel AI SDK and Mastra tests use a
  scripted AI SDK language model.
- The Claude Agent SDK test runs the real Claude Code CLI bundled with `@anthropic-ai/claude-agent-sdk`
  against a scripted Messages API on 127.0.0.1, with a placeholder `ANTHROPIC_API_KEY`.
