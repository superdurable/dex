# Agent harness examples (Python)

One order support agent, built five times: once with each Python agent
framework that Dex Agents supports. Each agent runs as a durable Dex agent
session. One Flow execution is one session, and the Flow ID is the session ID.

Every agent has the same two tools on the same in-memory order system
([`order_desk.py`](agent_harness_examples/order_desk.py)):

| Tool | Policy | What Dex does |
| --- | --- | --- |
| `look_up_order(order_id)` | `ToolPolicy(ToolEffect.READ)` | Runs without approval and retries on failure. |
| `cancel_order(order_id, reason)` | `ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE)` | Waits in `AwaitToolApproval`, then runs at most once. |

| Framework | Package | Agent | Model variable | Provider key |
| --- | --- | --- | --- | --- |
| Claude Agent SDK | `dex-agents-claude-agent-sdk` | [`claude_agent_sdk_order_support.py`](agent_harness_examples/claude_agent_sdk_order_support.py) | `DEX_CLAUDE_AGENT_SDK_MODEL` (default `claude-opus-5-5`) | `ANTHROPIC_API_KEY` |
| OpenAI Agents SDK | `dex-agents-openai-agents` | [`openai_agents_order_support.py`](agent_harness_examples/openai_agents_order_support.py) | `DEX_OPENAI_AGENTS_MODEL`, such as `gpt-5.2` | `OPENAI_API_KEY` |
| Pydantic AI | `dex-agents-pydantic-ai` | [`pydantic_ai_order_support.py`](agent_harness_examples/pydantic_ai_order_support.py) | `DEX_PYDANTIC_AI_MODEL`, such as `openai:gpt-5.2` | `OPENAI_API_KEY` |
| LangChain `create_agent` | `dex-agents-langgraph` | [`langchain_order_support.py`](agent_harness_examples/langchain_order_support.py) | `DEX_LANGCHAIN_MODEL`, such as `openai:gpt-5.2` | `OPENAI_API_KEY` |
| Google ADK | `dex-agents-google-adk` | [`google_adk_order_support.py`](agent_harness_examples/google_adk_order_support.py) | `DEX_GOOGLE_ADK_MODEL`, such as `gemini-2.5-flash` | `GOOGLE_API_KEY` |

Except for the Claude Agent SDK, each model variable defaults to
`mock/order-support`, a local demo model that needs no key. It calls
`look_up_order` for "look up order 1001" and `cancel_order` for "cancel order
1002", then answers with the tool results. The Claude Agent SDK runs the
Claude Code CLI, which calls the Anthropic API, so its agent needs
`ANTHROPIC_API_KEY` in the Worker's environment. Without that key, the app
leaves the Claude Agent SDK agent out and serves the other four. The agent passes the CLI an
environment that clears every other Claude sign-in setting, such as
`ANTHROPIC_AUTH_TOKEN` or `CLAUDE_CODE_USE_BEDROCK`, so only the API key signs in.

## Why a separate project

The OpenAI Agents SDK needs `openai` 3, and the LiteLLM release that the
[AI Agent example](../ai-agent/) uses needs `openai` 2. One environment cannot
hold both, so these examples have their own `pyproject.toml` and `uv.lock`.

## Run

Start `dexcli dev`, then:

```bash
cd examples/python/agent-harnesses
uv sync --locked
uv run --frozen python -m agent_harness_examples
```

The app serves every agent's session routes on `127.0.0.1:8081`
(`DEX_AGENT_HARNESS_HTTP_ADDRESS`), with one Worker for every session Flow
([`app.py`](agent_harness_examples/app.py)). The routes are the same for each
framework ([`http_routes.py`](agent_harness_examples/http_routes.py)), with
`<harness>` one of `claude-agent-sdk`, `openai-agents`, `pydantic-ai`,
`langchain`, or `google-adk`:

| Route | Body or query | `AgentSessionClient` call |
| --- | --- | --- |
| `POST /products/agent-harnesses/<harness>/sessions` | `{}` | `start_session` |
| `POST .../messages` | `{"session_id", "text"}` | `send_user_message` |
| `POST .../interrupts/resolve` | `{"session_id", "interrupt_id", "approved", "rejection_message"?}` | `resolve_interrupt` |
| `GET .../snapshot` | `?session_id=` | `read_agent_event_stream_snapshot` |

Starting a session returns a session token. Send it in the
`X-Agent-Session-Token` header of every other route.

```bash
base=http://127.0.0.1:8081/products/agent-harnesses/openai-agents
session=$(curl -s -X POST "$base/sessions" -H 'Content-Type: application/json' -d '{}')
id=$(echo "$session" | jq -r .session_id)
token=$(echo "$session" | jq -r .session_token)
curl -s -X POST "$base/messages" -H 'Content-Type: application/json' \
  -H "X-Agent-Session-Token: $token" -d "{\"session_id\": \"$id\", \"text\": \"Cancel order 1002\"}"
curl -s "$base/snapshot?session_id=$id" -H "X-Agent-Session-Token: $token" | jq .snapshot.pending_interrupts
```

## Test

The tests start one app against a running Dex server and drive each agent
through the HTTP routes: a turn that reads an order, a cancellation that runs
nothing before approval and exactly once after it, and a rejected cancellation
of another order that never runs. No test needs
a model key. The four in-process frameworks use the demo model. The Claude
Agent SDK test runs the real bundled Claude Code CLI against a local Messages
API stub, with a dummy `ANTHROPIC_API_KEY` and a temporary `HOME`.

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 uv run --frozen pytest -vv tests/
uv run --frozen mypy --strict agent_harness_examples tests
```

`../run-integ-tests.sh` runs them with the other Python example tests when it
gets no test arguments. `../run-integ-tests.sh --agent-harnesses [pytest args]`
runs only these tests.
