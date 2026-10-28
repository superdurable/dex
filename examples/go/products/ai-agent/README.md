# AI Agent

A durable chat agent in one Dex Flow. It waits for the user, calls a model and
runs the tools the model asks for. The conversation and everything the agent is
waiting for live in Dex, so you can kill the app at any point and start it
again: the agent continues where it was.

The app is one process (`cmd/ai-agent`): the Dex Worker, a Dex Client and a
small chat page. Dex itself runs separately in `dexcli dev`.

## Run it

```bash
dexcli dev --engine zd                  # terminal 1: Dex and Dex Web
cd examples/go && make bins
./dex-ai-agent                          # terminal 2: the app
open http://127.0.0.1:8090/ai-agent     # the chat page
```

Without a model key the app uses a mock model that turns commands into tool
calls:

| Command | Tool call |
| --- | --- |
| `/fetch <url>` | `fetch_page` downloads a page (read-only, about 5 seconds) |
| `/publish <text>` | `publish_note` appends a note to the notes file (needs approval) |
| `/wait <seconds>` | `wait` waits on a durable Timer |
| `/ask <question>` | `ask_user` waits for your answer |

Set `OPENAI_API_KEY` to use OpenAI (`gpt-5-mini` by default; set
`DEX_AI_AGENT_MODEL` to change it). `OPENAI_BASE_URL` points the app at any
OpenAI-compatible Chat Completions API.

| Variable | Default |
| --- | --- |
| `DEX_FLOW_SERVICE_ADDRESS` | `127.0.0.1:8801` |
| `DEX_AI_AGENT_HTTP_ADDRESS` | `127.0.0.1:8090` |
| `DEX_AI_AGENT_NOTES_FILE` | `$TMPDIR/dex-ai-agent/notes.jsonl` |
| `DEX_AI_AGENT_STAGE_PAUSE` | `1.5s`, the pause between the stages of `fetch_page` and `publish_note` |

To see the Step graph and the agent's Display in Dex Web, render the Flow and
pass the folder to `dexcli dev`:

```bash
dexcli visualize products/ai-agent/workflow.go --json --schema-version 2.0 --out /tmp/dex-flows/ai-agent
dexcli dev --engine zd --flow-rendering-dir /tmp/dex-flows
```

## How it works

| Step | What it does |
| --- | --- |
| `AwaitUser` | Waits on the `UserMessages` Channel. A message starts a model turn, or answers a pending `ask_user` question. |
| `CallModel` | Replays the `Messages` Attribute to the model and stores the reply. Tool calls go to `PendingToolCalls`. |
| `RouteTool` | Takes the next pending tool call and sends it to its wait, its approval or its execution. With none left, it calls the model again. |
| `AwaitToolApproval` | Waits on the `ToolApprovals` ChannelMap, keyed by tool call ID. |
| `AwaitTimer` | Waits on a durable Timer for the `wait` tool. No process sleeps. |
| `ExecuteTool` | Runs `fetch_page` or `publish_note` and records the result. It heartbeats between stages. |
| `RecordModelFailure` | Failure route of `CallModel`. Records why the model failed and waits for the user. |
| `RecordToolFailure` | Failure route of `ExecuteTool`. Records the failure as the tool's result, so the model decides what to do next. |

The chat page reads everything through one RPC, `GetAgentSnapshot`. Dex Web
shows the same state through `GetDexSummary` and `GetDexDisplay`.

## Waits

The agent waits in three ways. Each wait is a Step whose WaitFor returns a Dex
condition, so no process holds it.

- **User input**: `AwaitUser` waits for the next message. When the model called
  `ask_user`, the reply becomes that call's result.
- **Approval**: `publish_note` changes data, so `RouteTool` stores the call in
  `PendingApproval` and `AwaitToolApproval` waits for the decision for that
  exact call ID. `DecideToolApproval` deletes `PendingApproval` when it
  publishes the decision, so a second click is refused.
- **Timer**: `AwaitTimer` waits on `dex.Timer`. The Timer fires on time even
  when no app is running; the agent continues as soon as an app is back.

## Failure handling

- **Model calls** get 3 attempts. After the last one, the failure route
  `RecordModelFailure` stores `LastModelFailure` and returns to the user. It
  flags a failure of the model provider's API (an HTTP error or an unreachable
  API) with `isProviderApiError`, apart from a timeout or an app that was down.
  The chat page shows it in red, and the next successful call clears it.
- **Read-only tools** (`fetch_page`) get 3 attempts. When the app is killed
  during the call, Dex runs it again on the restarted app.
- **Writes** (`publish_note`) get one attempt, because a repeat could publish
  twice. When the app is killed during the call, `RecordToolFailure` records
  the outcome as unknown and tells the model to check before trying again.
  `publish_note` also uses the tool call ID as its idempotency key.

A Go Worker reports every Step error type as `*errors.joinError` (Go SDK
v1.25.0), so `RecordModelFailure` recognizes a provider failure by the fixed
prefix of `ModelProviderError` messages, not by `RecoveryErrorInfo.ErrorType`.

## Demo: kill the app

1. Start Dex and the app as above, open the chat page and click **New agent**.
   With a model key, ask: *Read https://example.com, wait 30 seconds, then
   publish a one-line summary.* With the mock model, send the commands one at
   a time.
2. While the page shows **Running fetch_page…**, kill the app from another
   terminal: `pkill -9 dex-ai-agent`. The page says the app is unreachable.
   Start `./dex-ai-agent` again. The page reconnects by itself with the same
   conversation, and the result notes that Dex ran the read-only call again.
3. While the agent waits on the Timer, kill the app again. Wait until the
   countdown passes, then start the app. The agent continues right away.
4. When it asks to publish, kill and restart the app once more: the approval is
   still there. Approve it, then `cat $TMPDIR/dex-ai-agent/notes.jsonl`. The
   note is there once.
5. Open Dex Web at http://127.0.0.1:8802 and find the Flow: its Step history
   shows each restart, and its Display shows the conversation stored in Dex.

## Change the code while a run waits

Dex does not replay a Flow. A waiting agent runs the code of the app that
takes its next Step. Stop the app, change a Step, start it, and the waiting
run follows the new code. Keep stored data readable: put new data in a new
Attribute and handle a missing value, and keep the names of Steps, Attributes,
Channels and RPCs that open runs use.

## Tests

`integ/ai_agent_test.go` starts the app as its own process and kills it with
SIGKILL. Dex keeps running. It checks that a question, an approval and a Timer
survive the kill, that a read-only tool runs again, that a write is reported
as an unknown outcome and is not repeated, and that a provider API failure is
flagged.

```bash
dexcli dev --engine zd --dex-port 19801 --web-port 19901
cd examples/go
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:19801 GOWORK=off go test -count=1 -v -run TestAIAgent ./integ
```

Run them against an engine where no other app serves `AIAgentFlow`, or that
app may take the test's Steps. `make e2eTests` runs them with the other
examples.

## Limits

The example stays short on purpose. The conversation is one Attribute; a
long-lived agent would store messages in an AttributeMap and compact old ones.
There is no streaming output, no steering of a running turn and no parallel
tool calls. The mock model is for demos and tests only.
