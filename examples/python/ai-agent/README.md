# AI Agent

This Python application is a general durable AI Agent. Dex owns its conversation
state, plans, tool queue, approvals, context summaries, and timers. LiteLLM
provides the model adapter, while the official MCP Python SDK connects trusted
local or remote tool servers.

The application runs without external credentials by default with the `mock/dex`
model. Its local model echoes normal messages and understands `/wait <seconds>
<reason>`, which makes the durable timer easy to test.

## Architecture

- `AgentMessages` is an AttributeMap. Each message is an independent value.
- `AgentPlan` atomically stores the current revision and ordered task list.
- `ContextSummary` keeps the cumulative compaction summary.
- Channels carry durable user messages, plan execution requests, and tool approvals.
- `AssistantText` uses the SDK buffered text writer for best-effort model output.
- `AgentEvents` is a best-effort Stream for tool and lifecycle progress.
- The `durable_wait` tool uses a Dex Timer and can be interrupted by a user message.
- Write, destructive, and unclassified MCP tools wait for explicit approval.

The current AttributeMap retains 2,000 messages by default. Dex compacts older
context before deleting summarized map instances. Deleted instances remain in the
Flow history until the configured history retention expires.

The model adapter receives `assistant_text.buffered_text(context).write` as its
delta callback. The SDK combines token-sized chunks for up to one second or 16
KiB and flushes the final batch when the Step invocation finishes.

## Plan before execution

Enable **Plan mode** beside the message composer to create or revise a plan. The
Agent can only call `write_todos` during that turn. MCP tools and the durable timer
remain unavailable until the user clicks **Execute plan**.

The plan card survives page refreshes and Worker restarts. It shows pending, in
progress, and completed tasks. If the model stops with unfinished work, the card
remains active and offers **Continue plan**. A waiting Agent is not necessarily a
completed plan.

## Run locally

Start Dex, install the Python dependencies, and build the React application:

```bash
dexcli dev
cd examples/python
uv sync --locked
cd ai-agent
npm ci
npm run build
cd ..
export DEX_AGENT_MCP_CONFIG="$PWD/ai-agent/mcp-servers.local.yaml"
uv run --frozen python main.py
```

Open [http://127.0.0.1:8080/products/ai-agent/](http://127.0.0.1:8080/products/ai-agent/).
The first page is the Agent Portal. Choose a LiteLLM provider and model, enter an
API key or use the Worker environment, and select the registered MCP servers and
tools available to the new session.

The local MCP configuration starts credential-free search, Slack, and Google
Docs demo servers before the Portal loads. Read operations run automatically.
Demo Slack posts and Google Doc creation still require approval. Use
[`mcp-servers.example.yaml`](./mcp-servers.example.yaml) when connecting real
providers.

The chat page shows buffered model text while a model call is running. Press
**Command/Ctrl+Enter** or **Alt+Enter** to send; plain Enter inserts a line break.
When work needs information that is missing, the Agent uses
**request_user_input** to show a durable question card and wait for a reply.

## Configure a real model

Set the provider credential required by LiteLLM and select its model name:

```bash
export OPENAI_API_KEY="..."
export DEX_AGENT_MODEL="openai/gpt-5-mini"
```

Other LiteLLM providers work the same way. The Portal may override the model and
system prompt per conversation. A key entered in the Portal is held only in the
Worker process for that Flow ID. It is cleared from the browser after startup and
is never stored in a Dex Attribute, message, or Flow history. Environment variables
remain the better choice for a deployed Worker or a session that must survive a
Worker process restart.

Worker defaults:

- `DEX_AGENT_MODEL`
- `DEX_AGENT_SYSTEM_PROMPT`
- `DEX_AGENT_CONTEXT_TOKENS`
- `DEX_AGENT_MESSAGE_RETENTION`
- `DEX_AGENT_MCP_CONFIG`

## Configure MCP servers

Copy [`mcp-servers.example.yaml`](./mcp-servers.example.yaml), edit the trusted
servers, and point the Worker at it:

```bash
export BRAVE_API_KEY="..."
export SLACK_MCP_AUTHORIZATION="Bearer ..."
export GOOGLE_DOCS_MCP_AUTHORIZATION="Bearer ..."
export DEX_AGENT_MCP_CONFIG="$PWD/ai-agent/mcp-servers.yaml"
```

The Brave entry uses the actively maintained
[`@brave/brave-search-mcp-server`](https://github.com/brave/brave-search-mcp-server).
The Slack and Google Docs entries are transport examples: replace their URLs with
servers selected from the [official MCP Registry](https://registry.modelcontextprotocol.io/).

Configuration keys:

- `transport`: `stdio` or `streamable_http`.
- `env`: child-process variable to Worker environment-variable name.
- `headers`: HTTP header to Worker environment-variable name.
- `trust_read_only_annotations`: allows trusted server annotations to classify a
  tool as read-only.
- `tools`: local read-only classification plus timeout and retry overrides.

MCP sampling, elicitation, and roots are not enabled. Resources, resource
templates, and prompts are exposed through model-visible broker tools.

## Try durable behavior

With `mock/dex`, send:

```text
/wait 12 remind me to check the ticket sale
```

Refresh the page while it waits. The Timer remains active. Send another message
before it fires to interrupt the wait and let the Agent replan.
