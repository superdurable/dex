# Long-running Agents: Landscape

Status: reference for [long-running-agents.md](long-running-agents.md).

Research date: 2026-10-31. Sources from May to October 2026 carry the most
weight. Vendor documentation pages are cited as read on that date. Claims that
come from a vendor's own announcement are marked as such. Counts and statuses
change quickly; re-check a source before quoting it outside this document.

## Agent frameworks

### Adoption

The Stack Overflow Developer Survey 2026 asked 4,400 respondents which agent
orchestration tools they used in the past year
([data](https://survey.stackoverflow.co/2026/ai/data)): LangChain 30.2%,
OpenAI Agents SDK 23.8%, LangGraph 18.8%, Google ADK 14.0%, Vercel AI SDK
9.0%, Pydantic AI 8.9%, CrewAI 5.5%, AutoGen 4.5%. Mastra, Claude Agent SDK,
and Microsoft Agent Framework were not in the list.

| Framework | Languages | GitHub stars (2026-10-31, change since May) | Weekly downloads (late October) |
| --- | --- | --- | --- |
| LangChain v1 `create_agent` | Python, TypeScript | 148.8k (+10%) | `langchain` 37.0M PyPI, 4.2M npm |
| LangGraph | Python, TypeScript | 44.2k (+42%) | 13.6M PyPI, 5.0M npm |
| OpenAI Agents SDK | Python, TypeScript | 30.4k and 4.0k (+17%, +36%) | 4.2M PyPI, 2.4M npm |
| Vercel AI SDK | TypeScript | 27.6k (+15%) | `ai` 41.9M npm |
| Claude Agent SDK | Python, TypeScript | 8.4k and 1.9k (+26%, +36%) | 8.0M PyPI, 17.1M npm (3.4x since early May) |
| Google ADK | Python, TypeScript, Go, Java | 22.1k (+13%) | 2.4M PyPI |
| Pydantic AI | Python | 21.0k (+25%) | `pydantic-ai-slim` 8.1M PyPI |
| Mastra | TypeScript | 29.2k (+24%) | `@mastra/core` 3.0M npm |
| CrewAI | Python | 60.3k (+20%) | 0.6M PyPI, down sharply since May |
| Microsoft Agent Framework | Python, .NET | 14.4k (+42%) | 0.4M PyPI |
| AWS Strands | Python, TypeScript | 9.0k (+57%) | 8.9M PyPI |

Stars come from the GitHub API and from Wayback Machine snapshots taken
2026-04-29 to 2026-05-14. Downloads come from pypistats and the npm downloads
API for the week ending 2026-10-30. Downloads include CI and transitive
installs, and every package, including baselines such as `requests`, dipped
about 25% in September 2026, so they show rough production use, not mindshare.

Two shifts in 2026 matter for this design. First, frameworks now call
themselves harnesses and ship durability: AI SDK 7 added a durable
`WorkflowAgent` and a `HarnessAgent` that wraps coding-agent CLIs
([changelog, 2026-06-25](https://vercel.com/changelog/ai-sdk-7)), Mastra added
durable agents ([docs](https://mastra.ai/docs/harness/durable-agents.md)),
Google ADK added resumability ([docs](https://adk.dev/runtime/resume/)), and
Pydantic AI made durability a pluggable capability
([docs](https://pydantic.dev/docs/ai/integrations/durable_execution/overview/)).
Second, builders increasingly embed a harness such as Claude Agent SDK instead
of writing a loop, which is what the Claude Agent SDK download growth shows.
The Thoughtworks Technology Radar put "ignoring durability in agent workflows"
on Hold in April 2026
([radar](https://www.thoughtworks.com/radar/techniques/ignoring-durability-in-agent-workflows)).

### Extension points

| Framework | State store | Model and tool hooks | Human approval | Sources |
| --- | --- | --- | --- | --- |
| LangGraph, LangChain | `BaseCheckpointSaver` (`put`, `put_writes`, `get_tuple`, `list`, `delete_thread`) | Middleware `wrap_model_call`, `wrap_tool_call` | `interrupt()`, resumed with `Command(resume=...)`; the node re-runs from its start | [checkpointers](https://docs.langchain.com/oss/python/langgraph/checkpointers), [middleware](https://docs.langchain.com/oss/python/langchain/middleware/custom), [interrupts](https://docs.langchain.com/oss/python/langgraph/interrupts) |
| OpenAI Agents SDK | `Session` protocol; `RunState.to_json()` | `RunConfig.model_provider` (`Model.get_response`, `stream_response`); `RunHooks` | `needs_approval` tools; `result.interruptions`; `state.approve()` | [running agents](https://openai.github.io/openai-agents-python/running_agents/), [human in the loop](https://openai.github.io/openai-agents-python/human_in_the_loop/), [sessions](https://openai.github.io/openai-agents-python/sessions/) |
| Vercel AI SDK 7 | Application-held UI messages | `LanguageModelV4Middleware` (`wrapGenerate`, `wrapStream`) via `wrapLanguageModel`; tool `execute` | Tool approval policies; approval stream parts | [middleware](https://ai-sdk.dev/docs/ai-sdk-core/middleware), [agents](https://ai-sdk.dev/docs/agents/overview) |
| Claude Agent SDK | `SessionStore` (`append`, `load`) | Hooks such as `PreToolUse`, `PostToolUse`; the loop runs in a CLI subprocess | `PreToolUse` returns `defer`; the turn ends with `tool_deferred`; resume the session later | [session storage](https://code.claude.com/docs/en/agent-sdk/session-storage), [hooks](https://code.claude.com/docs/en/agent-sdk/hooks), [defer](https://code.claude.com/docs/en/hooks) |
| Google ADK | `BaseSessionService` | `BasePlugin` before and after model and tool callbacks | `ToolConfirmation`; resume with the invocation ID; tools run at least once | [sessions](https://adk.dev/sessions/session/), [plugins](https://adk.dev/plugins/), [confirmation](https://adk.dev/tools-custom/confirmation/) |
| Pydantic AI | Explicit message history | `BaseDurabilityCapability` with `CallableOperationBackend.execute(operation_id, name, body, cache_key, config)` | Deferred tools | [backend builder](https://pydantic.dev/docs/ai/capabilities/durable_execution/backends/) |
| Mastra | Storage adapters | `ExecutionEngine.execute` | `suspend()` and `resume()` | [execution engine](https://github.com/mastra-ai/mastra/blob/main/packages/core/src/workflows/execution-engine.ts), [suspend and resume](https://mastra.ai/docs/workflows/suspend-and-resume) |
| Microsoft Agent Framework | `CheckpointStorage` | Executor checkpoint hooks | External events in the durable extension | [checkpoints](https://learn.microsoft.com/en-us/agent-framework/workflows/checkpoints) |

Every framework handles approval the same way: serialize, stop, and resume
with a decision. The wait itself, with a deadline, an escalation, and no
process holding it, is left to the application or to an engine.

### What breaks on long runs

- Duplicate side effects: LangGraph Cloud re-dispatches tool calls that run
  longer than about three minutes, with "2-3x redundant work and cost"
  ([langgraph#7417](https://github.com/langchain-ai/langgraph/issues/7417));
  CrewAI re-runs completed tools on task retry, "duplicate payments, emails,
  trades possible" ([crewAI#5802](https://github.com/crewAIInc/crewAI/issues/5802));
  the OpenAI Agents SDK states that approval does not guarantee exactly-once
  execution.
- Lost runs: a crash before LangGraph's first checkpoint can drop an accepted
  run with no failure record
  ([langgraph#8764](https://github.com/langchain-ai/langgraph/issues/8764)).
- Orphaned tool calls: after a pod restart left a function call without a
  result, ADK compaction was blocked permanently and silently
  ([adk-python#7236](https://github.com/google/adk-python/issues/7236)).
- Run ownership: Mastra decided run ownership from process-local state
  ([mastra#27364](https://github.com/mastra-ai/mastra/issues/27364)).
- Upgrades under in-flight runs: upgrading Pydantic AI across 2.54.0 "can
  break in-flight workflows" because durable operations change order
  ([pydantic-ai#11767](https://github.com/pydantic/pydantic-ai/issues/11767));
  LangGraph applies the latest graph to every thread and a renamed node fails
  on resume ([backward compatibility](https://docs.langchain.com/oss/python/langgraph/backward-compatibility)).

### Protocols

- MCP 2026-07-28: see the MCP section below. Its tasks extension requires that
  "the task must be durably created before sending the response"
  ([tasks](https://modelcontextprotocol.io/extensions/tasks/overview.md)).
- A2A 1.0.0 shipped 2026-03-12 with task states including input-required and
  auth-required, and push notifications to client webhooks
  ([specification](https://a2a-protocol.org/latest/specification/),
  [releases](https://github.com/a2aproject/A2A/releases)).
- AG-UI and the Vercel AI SDK UI message stream: see UI streaming below.

## Durable execution engines

### Integrations

| Engine | Framework integrations | Notes |
| --- | --- | --- |
| Temporal | OpenAI Agents SDK (Python, TypeScript), Google ADK (Python, Go, TypeScript, Java), Strands (Python, TypeScript), LangGraph (Python), LangSmith tracing, Vercel AI SDK (TypeScript), Gemini SDK (Python); Claude Agent SDK, Deep Agents, and an MCP client in pre-release; Pydantic AI, Mastra, and Braintrust partner-built | Announced as GA on 2026-10-30 ([blog](https://temporal.io/blog/ai-framework-integrations)); the [repository](https://github.com/temporalio/ai-integrations) still labels several as preview. The OpenAI integration moved from `temporalio.contrib.openai_agents` to the `temporalio-openai-agents` package ([PyPI](https://pypi.org/project/temporalio-openai-agents/)). |
| Restate | Vercel AI SDK middleware (`durableCalls`), OpenAI Agents SDK (`DurableRunner`), Google ADK (`RestatePlugin`), Pydantic AI (`RestateAgent`), LangChain, LiteLLM | No maturity labels on the [docs index](https://docs.restate.dev/ai). No LangGraph, Claude Agent SDK, Strands, or Mastra integration. |
| DBOS | Pydantic AI (`DBOSDurability`), OpenAI Agents SDK (`DBOSRunner`), Google ADK (`DBOSPlugin`), Vercel AI SDK (`durableCalls`, `durableTools`, `durableMCPTools`, `agentTool`), LlamaIndex Workflows; a LangGraph example | [Integrations](https://docs.dbos.dev/integrations/vercel-ai.md); Vercel AI SDK integration improved 2026-09-29. |
| Pydantic AI (hub) | One durability interface implemented for Temporal, DBOS, Prefect, Restate, AWS Lambda, Absurd, pgtask, and Airflow, plus a builder for others | [Overview](https://pydantic.dev/docs/ai/integrations/durable_execution/overview/). `TemporalAgent` and `DBOSAgent` are deprecated in favor of durability capabilities. |

### How each engine maps an agent

| Agent concept | Temporal | Restate | DBOS |
| --- | --- | --- | --- |
| Agent loop | Deterministic workflow code | Handler with a journal | Deterministic workflow function |
| Model call | Activity, added by the plugin | `ctx.run` journal entry | Step |
| Tool | Activity via `activity_as_tool`; plain tools run in workflow code and must be deterministic | `ctx.run`, or a separate durable service | Step when decorated; MCP calls are always steps |
| Session | Long-lived workflow with Continue-As-New | Virtual Object keyed by conversation | Workflow plus application tables |
| Human wait | Signal or Update with a timer | Awakeable raced against a timer | `recv` with a timeout |
| Sub-agent | Child workflow or Nexus operation | Durable RPC to another agent service | Child workflow |
| Token stream | Workflow Streams, through Signals and Updates (public preview) | Not supported for tokens yet | Every write persisted to the database |

Sources: [Temporal OpenAI Agents](https://pypi.org/project/temporalio-openai-agents/),
[Temporal Vercel AI SDK](https://docs.temporal.io/develop/typescript/integrations/ai-sdk),
[Temporal Workflow Streams](https://docs.temporal.io/develop/python/workflows/workflow-streams.md),
[Restate sessions](https://docs.restate.dev/ai/patterns/sessions.md),
[Restate streaming](https://docs.restate.dev/ai/patterns/streaming-responses.md),
[DBOS streaming](https://docs.dbos.dev/ai/streaming.md),
[DBOS human in the loop](https://docs.dbos.dev/ai/hitl.md).

### Where they struggle

- **History growth (Temporal).** An agent hit the 50 MB history limit at tool
  call 53 of 100, with model inputs at 99.8% of the history. The maintainer
  recommended external payload storage, which is in public preview
  ([sdk-python#1890](https://github.com/temporalio/sdk-python/issues/1890),
  2026-09-21). Limits are 51,200 events or 50 MB per execution
  ([limits](https://docs.temporal.io/workflow-execution/limits)).
- **Replay of streamed runs (Temporal).** A streamed OpenAI Agents run can fail
  replay on another Worker and stay running; the fix needs a patch gate
  ([ai-integrations#229](https://github.com/temporalio/ai-integrations/issues/229),
  2026-10-27).
- **Code changes mid-run.** Temporal: patching, Worker Versioning, and an
  experimental upgrade at Continue-As-New that names agent workflows waiting for
  input as the target case
  ([docs](https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning/upgrade-on-continue-as-new)).
  Restate: immutable deployments with pinned runs and restart from a journal
  prefix ([blog](https://restate.dev/blog/dealing-with-versioning-in-long-running-agents)).
  DBOS: recovery only for matching version hashes; automatic upgrades only in a
  commercial extension ([2026-10](https://dbos.dev/blog/whats-new-in-dbos-october-2026)).
- **Tool side effects.** All three are at-least-once. Temporal's MCP and Claude
  Agent SDK integrations ask for idempotent tools
  ([README](https://github.com/temporalio/ai-integrations/tree/main/python/claude_agent_sdk)).
  Cloudflare's Pi harness re-runs an interrupted tool only when it is marked
  `replay: "safe"` and otherwise tells the model
  ([docs](https://developers.cloudflare.com/agents/harnesses/pi/index.md)).
- **Model rate limits.** DBOS has queues with concurrency and rate limits.
  Restate added concurrency-only flow control and lists rate limits as planned
  ([1.8](https://restate.dev/blog/announcing-restate-1-8)). None throttles on
  provider tokens per minute.
- **Framework checkpointing alone.** LangGraph resumes at a super-step
  boundary, and nodes after the checkpoint re-execute, including their model
  calls ([checkpointers](https://docs.langchain.com/oss/python/langgraph/checkpointers.md)).

### Market signals

- Replit's agent runs on Restate ([Restate, 2026-09-30](https://restate.dev/blog/announcing-series-a));
  Temporal had named it as a Temporal user in November 2025
  ([Temporal](https://temporal.io/blog/of-course-you-can-build-dynamic-ai-agents-with-temporal)).
- Temporal frames its 2026-09-14 funding round around agents
  ([announcement](https://temporal.io/news/temporal-raises-550m-at-a-12-55b-valuation)).
- Temporal previews serverless Workers on Lambda and Cloud Run
  ([blog, 2026-10-28](https://temporal.io/blog/serverless-workers-lambda-cloud-run)).

## Durable agent harnesses on a platform

| Harness | What it provides | Source |
| --- | --- | --- |
| Vercel Workflow | `DurableAgent`, resumable `getWritable` streams, hooks for human input, sleeps of months; each run tied to its deployment | [docs](https://workflow-sdk.dev/docs/ai), GA 2026-04-16 |
| Cloudflare Agents | Durable Objects; a durable chunk log with `Last-Event-ID` resume; sessions as message trees with compaction; approvals backed by Workflows | [streams](https://developers.cloudflare.com/agents/runtime/capabilities/streams/index.md), [sessions](https://developers.cloudflare.com/agents/runtime/capabilities/sessions/index.md) |
| Trigger.dev | Chat agents with per-conversation input and output streams, per-turn snapshots, upgrade to new code at a turn boundary | [how it works](https://trigger.dev/docs/ai-chat/how-it-works.md), [upgrades](https://trigger.dev/docs/ai-chat/patterns/version-upgrades.md) |
| Microsoft Agent Framework durable extension | Durable threads, waits of weeks; reliable streaming needs a broker such as Redis | [docs](https://learn.microsoft.com/en-us/agent-framework/hosting/durable-extension) |
| LangSmith Deployment | Checkpointed LangGraph runs, background runs, four double-texting strategies | [double-texting](https://docs.langchain.com/langsmith/double-texting) |

## Hosted agent runtimes

| Runtime | Session limits | Source |
| --- | --- | --- |
| AWS Bedrock AgentCore Runtime | 8 hours per session, 15 minutes idle; session state is ephemeral | [how it works](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-how-it-works.html) |
| Anthropic Claude Managed Agents (beta) | Sessions idle between events and wait indefinitely for approval; buffered messages are authoritative, deltas best effort and never replayed | [overview](https://platform.claude.com/docs/en/managed-agents/overview), [event deltas](https://platform.claude.com/docs/en/managed-agents/event-deltas) |
| OpenAI Agents API (beta, 2026-09-10) | OpenAI manages sessions and recovery; webhooks when the agent finishes or needs input | [overview](https://developers.openai.com/api/docs/guides/agents-api/overview) |

Between April and October 2026 the hosted offerings converged on one bundle: a
harness with compaction, persisted sessions, a sandbox per session, vaulted
credentials, an MCP tool gateway, approval policies, memory, schedules and
webhooks, tracing, evals, and spend limits. Runs longer than a session go back
to the caller.

## Surrounding infrastructure

### Tracing

- The OpenTelemetry GenAI conventions moved to
  [their own repository](https://github.com/open-telemetry/semantic-conventions-genai)
  in June 2026 and are marked Development. Agent spans are `invoke_agent`,
  `execute_tool`, and inference spans such as `chat`; `invoke_workflow` covers
  orchestrators ([agent spans](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-agent-spans.md)).
- `gen_ai.conversation.id` is set when the application manages history, and must
  not fall back to a trace ID.
- Langfuse needs a root span to build a trace and groups by a session ID on
  every span ([docs](https://langfuse.com/integrations/native/opentelemetry)).
  LangSmith drops children whose parent never arrives after a buffering window
  ([docs](https://docs.langchain.com/langsmith/trace-with-opentelemetry)).
  Braintrust lists only traces with a root span
  ([docs](https://docs.braintrust.dev/integrations/sdk-integrations/opentelemetry)).
  Datadog maps `gen_ai.conversation.id` to its session
  ([docs](https://docs.datadoghq.com/llm_observability/instrumentation/otel_instrumentation/)).
  No backend keeps one trace open for days.

### Evals

Langfuse, LangSmith, and Braintrust evaluate final responses, single steps, and
trajectories, and build datasets from production traces. Replaying a recorded
run with a new prompt or model while reusing its tool results is a manual
workflow in each, not a feature
([Langfuse](https://langfuse.com/resources/engineering/ai-agent-evaluation),
[LangSmith simulation](https://docs.langchain.com/langsmith/multi-turn-simulation),
[Braintrust](https://docs.braintrust.dev/core/experiments)).

### Model providers

- Anthropic rejects edited conversation history for accounts created on or
  after 2026-08-31, because thinking blocks are bound to the conversation that
  produced them, and its compaction state is lost when only text is kept.
  OpenAI compaction items are encrypted
  ([OpenAI compaction](https://developers.openai.com/api/docs/guides/compaction)).
- Prompt caches last minutes to a day: Anthropic 5 minutes or 1 hour
  ([prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)),
  OpenAI 30 minutes or 12 hours on current models
  ([prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)).
- OpenAI background mode resumes a stream from a sequence number
  ([background](https://developers.openai.com/api/docs/guides/background)),
  and its webhooks follow Standard Webhooks and retry for 72 hours
  ([webhooks](https://developers.openai.com/api/docs/guides/webhooks)).
  Anthropic and OpenAI batch APIs have 24-hour windows at half price
  ([Anthropic](https://platform.claude.com/docs/en/build-with-claude/batch-processing),
  [OpenAI](https://developers.openai.com/api/docs/guides/batch)).
- LiteLLM budgets per key, user, team, and customer, and has per-session agent
  limits with budget reservation before a call
  ([docs](https://docs.litellm.ai/docs/proxy/users)).

### MCP

The 2026-07-28 specification made MCP stateless, moved tasks to an extension
polled with `tasks/get`, replaced server-initiated elicitation with requests
that return `input_required` and are retried with the answer, and removed
stream resumption: a broken stream must be re-issued as a new request
([changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)).
Dynamic client registration is deprecated in the
[authorization spec](https://modelcontextprotocol.io/specification/latest/basic/authorization).

### UI streaming

- AG-UI defines lifecycle, text, tool call, state, reasoning, and sub-agent
  events; interrupts ride on `RunFinished`, and resync is by snapshot
  ([events](https://docs.ag-ui.com/concepts/events)).
- The Vercel AI SDK UI message stream has tool approval parts and `reset-step`
  for a retried step ([protocol](https://ai-sdk.dev/docs/ai-sdk-ui/stream-protocol)).
  Resumable streams keep the stream in Redis with an expiry, and a client abort
  is treated as a disconnect
  ([resume](https://ai-sdk.dev/docs/ai-sdk-ui/chatbot-resume-streams)).
- Neither protocol defines resume from an offset.

### Sandboxes

E2B, Modal, and Vercel cap continuous sandbox runtime at about 24 hours.
Snapshots expire after 7 to 30 days on Modal, Vercel, and Cloudflare. E2B keeps
paused sandboxes indefinitely, and Daytona VMs can fork with memory
([E2B](https://docs.e2b.dev/sandbox/persistence),
[Modal](https://modal.com/docs/guide/sandbox-snapshots),
[Vercel](https://vercel.com/docs/sandbox/concepts/persistent-sandboxes),
[Cloudflare](https://developers.cloudflare.com/sandbox/concepts/lifetime/),
[Daytona](https://www.daytona.io/docs/en/sandboxes/)).

### Identity

MCP authorization uses OAuth 2.1 with resource indicators and no token
passthrough. Token vaults such as Auth0 Token Vault, Nango, Arcade, and
Composio hold user tokens for agents acting while the user is offline
([Auth0](https://auth0.com/docs/secure/tokens/token-vault),
[Nango](https://nango.dev/docs/getting-started/intro-to-nango)). AgentCore
Policy uses Cedar.

## Gaps no one fills well

1. Cheap, resumable token streams that handle retries, outside one vendor's
   platform.
2. Conversation growth as an engine concern rather than Continue-As-New,
   external storage, or the application's database.
3. A side-effect contract for tools beyond at-least-once and idempotency keys.
4. Versioning prompts, tools, and model settings for in-flight sessions.
5. Flow control on provider tokens and budgets shared by an agent and its
   sub-agents.
6. Sandbox state for runs longer than a sandbox's lifetime.
7. Per-run cost that joins engine work and model spend.
8. Replaying a recorded run with a new prompt or model while reusing its tool
   results.

The design addresses 1 (C6), 2 (C7), 3 (C3, C4), 4 (C14), 5 (C10), 6 (C12),
7 (C9), and 8 (C11).
