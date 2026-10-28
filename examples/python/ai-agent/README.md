# AI Agent

This Python application is a general durable AI Agent. Dex owns its conversation
state, plans, tool queue, approvals, context summaries, and timers. LiteLLM
provides the model adapter, while the official MCP Python SDK connects trusted
local or remote tool servers.

The application runs without external credentials by default with the `mock/dex`
model. Its local model echoes normal messages and understands `/wait <seconds>
<reason>`, which makes the durable timer easy to test. `/tool <name> <json>`
calls one tool, and `/slow <seconds> <text>` makes the model call take that long.

The snapshot endpoint reads retained state through the unlocked, non-transactional
**get_snapshot** RPC, which returns no durable effects. Missing or retention-unavailable
targets return HTTP 404 directly. Reads use no lifecycle probe, short timeout, or
historical output fallback. The response sets **flow_status** to **unknown** because
a successful query does not establish whether the Flow is active.

## Architecture

- `AgentMessages` is an AttributeMap. Each value stores one message and its creation time.
- `AgentPlan` atomically stores the current revision and ordered task list.
- `ContextSummary` keeps the cumulative compaction summary.
- `QueuedUserMessages` keeps user messages in a durable FIFO queue.
- `SteeredUserMessages` carries explicit Steer messages to safe boundaries.
- Other Channels carry plan execution requests and tool approvals.
- `ReasoningSummary` uses a buffered text writer for OpenAI reasoning summaries.
- `AssistantText` uses a separate buffered writer for visible response text.
- `AgentActivity` is a best-effort Stream for tool and lifecycle progress.
- The `durable_wait` tool uses a Dex Timer and can be interrupted by Steer.
- Write, destructive, and unclassified MCP tools wait for explicit approval.
- Recovery Steps report a tool call or model call that a crash cut off. See
  [Crashes and restarts](#crashes-and-restarts).

The current AttributeMap retains 2,000 messages by default. Dex compacts older
context before deleting summarized map instances. Deleted instances remain in the
Flow history until the configured history retention expires.

The model adapter receives separate buffered callbacks for reasoning summaries
and assistant text. The SDK combines token-sized chunks for up to one second or
16 KiB and flushes each final batch when the Step invocation finishes.

OpenAI models use LiteLLM's Responses API adapter with stateless requests and an
automatic reasoning summary. Dex still rebuilds the complete provider-neutral
input for every call. Encrypted OpenAI reasoning items are retained with their
durable assistant message and replayed when that provider requires them. The UI labels official reasoning-summary events as
**Thinking**, visible output as **Response**, and structured tool or lifecycle
events as **Agent activity**. Providers without reasoning-summary events never
show inferred or fabricated thinking.

## Plan before execution

Enable **Plan mode** beside the message composer to create or revise a plan. The
Agent can only call `write_todos` during that turn. MCP tools and the durable timer
remain unavailable until the user clicks **Execute plan**.

The plan card survives page refreshes and Worker restarts. It shows pending, in
progress, and completed tasks. If the model stops with unfinished work, the card
remains active and offers **Continue plan**. A waiting Agent is not necessarily a
completed plan.

## Queue and Steer

Submitting while the Agent loop is active adds a visible **Queued** message. The
Agent does not consume it until the loop returns to **AwaitUser**. Before then,
the UI can edit it, delete it, or choose **Steer**. Editing first deletes the
pending message and puts its content back in the composer; submitting it again
creates a new message at the queue tail.

Steer sends only the selected message ID. A transactional RPC loads
**QueuedUserMessages**, finds the original Value, stages its deletion, and
publishes that Value to **SteeredUserMessages**. A concurrent consumption makes
the entire operation fail without a destination publication. Dex applies the
steered message before the next model call, tool, approval wait, or Timer
continuation. It does not cancel an LLM or MCP request already running. A Steer
clears unexecuted tool calls and records durable cancellation results. Only Steer
interrupts a tool approval or durable wait.

The queue panel remains above the composer. It collapses to a compact status row
when both queues are empty and expands automatically when a pending message appears.
The browser page is the conversation scroll surface. The queue, user-input request,
and composer stay fixed at the bottom while new activity remains visible above them.
The UI treats the Server queue as canonical. Its snapshot endpoint invokes one
read-only RPC that loads **AgentMessages**, **QueuedUserMessages**, and
**SteeredUserMessages**, then returns the application history, Agent description,
run ID, and both queues. It keeps a submitted item visible until the snapshot
reports it as pending or the Flow consumes it into history. It refreshes
immediately after each mutation, refreshes from Agent activity events and browser
reconnection, and uses an eight-second fallback poll while the page is visible or
the Agent is active.

## Run locally

Start Dex on server-zd, install the Python dependencies, and build the React
application. **dexcli dev --engine zd** needs **dex-server-zd** from a server-zd
release on your PATH, or **--zd-binary**. It keeps its state in
`~/.dex/zd/8801`, so starting it again resumes the same Flows.

```bash
dexcli dev --engine zd
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
The first page is the Agent Portal. Choose a configured LiteLLM provider and
model, then select the registered MCP servers and tools available to the new
session. Providers without their required Worker environment variable remain
visible but cannot be selected.

The local MCP configuration starts credential-free search, Slack, Google Docs,
and web demo servers before the Portal loads. Read operations run automatically.
Demo Slack posts and Google Doc creation still require approval. The web
server's read-only `fetch_page` tool downloads a real public page and reports
its progress in three paced stages, so the chat page can show a running tool. Use
[`mcp-servers.example.yaml`](./mcp-servers.example.yaml) when connecting real
providers.

The chat page shows reasoning summaries when the provider supplies them and
buffers visible response text separately while a model call is running. Press
**Command/Ctrl+Enter** or **Alt+Enter** to send; plain Enter inserts a line break.
When work needs information that is missing, the Agent uses
**request_user_input** to expand a durable input panel in the conversation and
wait for a reply. The tool may provide known choices, which the UI renders as
selection buttons; otherwise the panel renders a free-form text box.

## Configure a real model

Add the provider credential required by LiteLLM to `examples/.env`:

```bash
OPENAI_API_KEY="..."
```

Restart the Python examples after changing the file. The Portal shows every
supported provider, but providers without their environment variable are disabled.
It never sends credentials to the browser. Shell environment variables take
precedence over values in `examples/.env`. The selected credential is used for
normal and context-compaction model calls and is never stored in a Dex Attribute,
message, or Flow history.

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
before it fires, then choose **Steer** to interrupt the wait and let the Agent
replan.

## Crashes and restarts

Dex stores the conversation, plan, pending approval, timer, and question in the
Flow, so you can hard-kill the engine and the Worker at any point:

```bash
kill -9 $(pgrep -f 'dex-server-zd dev')   # the engine; dexcli dev exits with it
kill -9 $(pgrep -f 'python main.py')      # the Worker
dexcli dev --engine zd
uv run --frozen python main.py
```

- **While the Agent waits** for a message, a tool approval, a durable timer, or
  an answer to **request_user_input**, the same run resumes with everything it
  had. A timer that came due while the engine was down fires after it starts.
- **A Worker crash during a tool call** ends that call's only attempt as soon as
  Dex sees the Worker disconnect. When a Worker is running again,
  **ReportInterruptedTool** records an `interrupted` tool result and the model
  decides what to do. Read-only tools are marked `retry_safe`; write tools
  report `outcome_unknown`, because the write may have happened. The tool Step
  heartbeats every 10 seconds, so a Worker that hangs without disconnecting is
  detected within 30 seconds.
- **An engine crash during a tool call** sends the call to the Worker again after
  the engine starts, because Dex delivers Step work at least once. A write tool
  can therefore run twice. Make write tools idempotent, for example by using the
  tool call ID as an idempotency key.
- **A model call** gets three attempts. When all of them fail,
  **RecoverFailedModelCall** returns the Agent to the user instead of failing
  the run. Agent activity says which failure it was: `model_api_failed` names
  the provider's error class and HTTP status, and `model_unavailable` means the
  call did not finish, for example because no Worker was running. Send a message
  or choose **Continue plan** to try again. Context compaction follows the same
  rules through **RecoverFailedCompaction**.

## Change code while a run waits

Dex does not replay a Flow's history. A waiting run executes its next Step with
whatever code the Worker has when that Step starts, so most changes need no
versioning. To try it with the local MCP configuration:

1. Send `/tool slack__post_message {"channel":"#eng","message":"hello"}` and leave
   the approval card waiting.
2. Stop the Worker and run
   `patch -p1 < ai-agent/changes/save-task-summaries.patch`. The change adds a
   **SaveTaskSummary** Step after every tool call and stores its result in a new
   **TaskSummaries** AttributeMap.
3. Start the Worker and approve. The waiting run executes **SaveTaskSummary**,
   which did not exist when it started. `dexcli flow state <flow-id>` shows the
   new `TaskSummaries/<call-id>` entry next to the conversation.
4. Run `patch -p1 -R < ai-agent/changes/save-task-summaries.patch` to undo it.

These changes are safe while runs wait:

- Change the code inside a Step, RPC, or helper. Waiting runs use it from their
  next Step.
- Add a Step and route to it from an existing Step.
- Put new data in a new Attribute, AttributeMap, Channel, or Stream, and add it
  to the persistence schema. Do not repurpose existing data.
- Add a dataclass field with a default to a stored type.

These changes need care:

- Keep every Flow, Step, Attribute, Channel, Stream, and RPC name that a waiting
  run can still reach. A rename is a removal plus an addition; the run waiting in
  **AwaitToolApproval** still needs a Step with that name.
- Do not remove a stored field or change its type. Existing values must still
  decode.
- A wait that is already registered keeps its conditions. Changed **wait_for**
  code applies the next time a Step starts waiting.
- Step options, such as retries and timeouts, apply from the next Step
  transition. Flow options are fixed when the run starts.

## Test crashes and code changes

`tests/integ/test_ai_agent_restarts.py` starts a private server-zd through
**dexcli dev** and runs `main.py` as the only Worker, then hard-kills them. Set
**DEX_SERVER_ZD_BINARY** to a `dex-server-zd` executable; without it the tests
are skipped. Set **DEXCLI_PATH** to a `dexcli` executable, or the tests build one
from `cli/` with Go.

```bash
DEX_SERVER_ZD_BINARY=/path/to/dex-server-zd \
  uv run --frozen pytest tests/integ/test_ai_agent_restarts.py
```
