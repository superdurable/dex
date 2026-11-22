# Agent harness examples (Go)

The order support agent on [CloudWeGo Eino](https://github.com/cloudwego/eino),
as a durable Dex agent session. One Flow execution is one session, and the Flow
ID is the session ID. The agent is an Eino ADK `ChatModelAgent`; the Dex Agents
Eino adapter, `github.com/superdurable/dex/agents/go/eino`, runs it on the Go
core's session Flow.

The agent has the same two tools as the Python and TypeScript examples, on the
same in-memory order system ([`orderdesk`](orderdesk/order_desk.go)):

| Tool | Effect | What Dex does |
| --- | --- | --- |
| `look_up_order(order_id)` | `dexagents.ToolEffectRead` | Runs without approval and retries on failure. |
| `cancel_order(order_id, reason)` | `dexagents.ToolEffectNonIdempotentWrite` | Waits in `AwaitToolApproval`, then runs at most once. |

| File | What it holds |
| --- | --- |
| [`quickstart/eino_quick_start.go`](quickstart/eino_quick_start.go) | The quick start agent, plain (Eino alone) and durable (Dex). |
| [`quickstart/session.go`](quickstart/session.go) | The quick start runner: Registry, Worker, Client, and one turn with approvals. |
| [`quickstart/embed.go`](quickstart/embed.go) | The smallest host: one Worker, one Client, one turn. |
| [`cmd/agent-quick-start/main.go`](cmd/agent-quick-start/main.go) | The quick start entry point, which asks for approval at the prompt. |
| [`ordersupport/eino_order_support.go`](ordersupport/eino_order_support.go) | The order support agent, with the Slack Connector tool. |
| [`ordersupport/slack_connector_tools.go`](ordersupport/slack_connector_tools.go) | The Slack Connector tool and its connection. |
| [`ordersupport/app.go`](ordersupport/app.go) | The order support app's Registry, Worker, and Client. |
| [`ordersupport/http_routes.go`](ordersupport/http_routes.go) | The session routes. |
| [`cmd/order-support/main.go`](cmd/order-support/main.go) | The order support HTTP server. |
| [`demomodel/demo_model.go`](demomodel/demo_model.go) | The local demo model. |

The model is `DEX_EINO_MODEL`. It defaults to `mock/order-support`, a local
demo model that needs no key. It calls `look_up_order` for "look up order
1001" and `cancel_order` for "cancel order 1002", then answers with the tool
results. Any other name is an OpenAI model for the eino-ext OpenAI client, such
as `gpt-5-mini`, and needs `OPENAI_API_KEY`.

## Why a separate module

The Dex Agents Go modules need Go 1.26, and the [Go examples](../) build with
Go 1.24. This module has its own `go.mod`, so the Go examples keep their Go
version and their dependencies, and the quick start downloads only this
folder. It depends only on released modules:
`github.com/superdurable/dex/agents/go/core` and
`github.com/superdurable/dex/agents/go/eino` v0.6.16, `github.com/cloudwego/eino`
v0.9.23, `github.com/cloudwego/eino-ext/components/model/openai` v0.1.13, and the
Slack Connector `github.com/superdurable/dex-connectors-library/connectors/slack/go`
v1.8.1.

## Quick start

Start `dexcli dev`, then:

```bash
cd examples/go/agent-harnesses
go run ./cmd/agent-quick-start eino "Look up order 1001"
go run ./cmd/agent-quick-start eino "Cancel order 1002"
```

The second command asks before `cancel_order` runs. Pass `--approve` to
approve without asking. Stop the program at the prompt, then run it again with
`--session-id <session ID>` to resume the same session.
`DEX_ORDER_DESK_TOOL_DELAY_SECONDS` makes each tool call wait first, so you can
stop the program while a tool runs.

## Slack connector tools

The order support agent can also post a reply in a Slack thread through the
Slack Connector. The Connector's operations reach the agent through
`connectortools.ConnectorToolSource`, passed to the adapter with
`dexeino.WithToolSources`. The wiring is in
[`slack_connector_tools.go`](ordersupport/slack_connector_tools.go).

| Tool | Slack operation | What Dex does |
| --- | --- | --- |
| `slack_post_thread_reply(channelId, threadTimestamp, text)` | `postThreadReply` | Waits in `AwaitToolApproval`, then runs in `CallSingleAttemptTool` under the Connector's Call ID, with bounded resends: at most 3 sends. |

The example offers only `slack_post_thread_reply` and passes
`connectortools.WithRequiresApproval(true)`, so every post waits for a person.
The Worker opens the Slack connection `support-slack` from the project
configuration and holds no Slack token.

The Slack Connector v1.8.1 gives `postThreadReply` bounded resends. After a
Worker crash during a post, the Connector reads the thread back first and sends
again only when it cannot find the reply. So a crash can post a duplicate
reply, up to 3 posts in all.

- **Local development**: set `DEX_CONNECTOR_CONFIG_FILE` to the
  `connections.json` that Dex Web saves, by default
  `$HOME/.dex/connectors/connections.json`. It must hold an authorized Slack
  connection named `support-slack`. See
  [Add a connection](https://docs.superdurable.io/reference/web/local-connector-setup#add-a-connection).
- **Deployed**: the `DEX_PROJECT_*` variables select the project configuration
  in object storage instead.

Without a project configuration, or without a Slack connection named
`support-slack` in it, the app logs one line and the agent runs without the
Slack tool. With the demo model, send `Reply in Slack thread <channel ID>
<thread timestamp>: <text>`.

## Run the order support app

Start `dexcli dev`, then:

```bash
cd examples/go/agent-harnesses
go run ./cmd/order-support
```

The app serves the agent's session routes on `127.0.0.1:8082`
(`DEX_AGENT_HARNESS_HTTP_ADDRESS`), with one Worker for the session Flow. The
routes are the Python example's, under `/products/agent-harnesses/eino`:

| Route | Body or query | `AgentSessionClient` call |
| --- | --- | --- |
| `POST /products/agent-harnesses/eino/sessions` | `{}` | `StartSession` |
| `POST .../messages` | `{"session_id", "text"}` | `SendUserMessage` |
| `POST .../interrupts/resolve` | `{"session_id", "interrupt_id", "approved", "rejection_message"?}` | `ResolveInterrupt` |
| `GET .../snapshot` | `?session_id=` | `GetAgentSnapshot` |

Starting a session returns a session token. Send it in the
`X-Agent-Session-Token` header of every other route.

```bash
base=http://127.0.0.1:8082/products/agent-harnesses/eino
session=$(curl -s -X POST "$base/sessions" -H 'Content-Type: application/json' -d '{}')
id=$(echo "$session" | jq -r .session_id)
token=$(echo "$session" | jq -r .session_token)
curl -s -X POST "$base/messages" -H 'Content-Type: application/json' \
  -H "X-Agent-Session-Token: $token" -d "{\"session_id\": \"$id\", \"text\": \"Cancel order 1002\"}"
curl -s "$base/snapshot?session_id=$id" -H "X-Agent-Session-Token: $token" | jq .snapshot.pending_interrupts
```

## Test

The tests run against a running Dex server and need no model key and no Slack
token. The quick start tests run a turn that reads an order, a cancellation
that runs nothing before approval and exactly once after it, a rejected
cancellation that never runs, an approval that waits while the Worker stops and
runs once on a new Worker, the smallest host, and the plain Eino agent without
Dex. The order support tests drive the HTTP routes through the same turns,
check the session token, and post a Slack thread reply against the Slack
Connector's mock (`slackmock`): it posts nothing before approval and once
after it, from a `CallSingleAttemptTool` Step. The tests read the Flow history
with `dexcli` (`DEXCLI_PATH`, `DEXCLI`, or `PATH`).

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 go test -race -count=1 ./...
```

`../run-e2e-tests.sh` runs them with the other Go example tests when it gets no
test arguments.
