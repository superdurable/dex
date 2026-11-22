# Dex Agents for Go, Java, and Rust

Status: Go core shipped at a reduced scope, with the Eino adapter added on request (2026-11-21); Java and Rust deferred (2026-11-02). Parent: [agents-m2-stepwise.md](agents-m2-stepwise.md)
and [agent-framework-adapters.md](agent-framework-adapters.md).

Dex Agents ships for Python and TypeScript. This addendum brings the same
session contract to the other three Dex SDKs. The Go, Java, and Rust SDKs
already have every primitive the cores use: AttributeMaps, ChannelMaps,
Streams, RPC locks, heartbeats, and recovery errors. So no engine, SDK, or
server primitive is added, and a session started from any language can run
on Workers of any other.

## Decisions

1. **One contract, five hand-written cores, one conformance suite.** Each
   language gets an idiomatic core, as Python and TypeScript have. A shared
   suite of golden wire files fixes every value that crosses a language
   boundary: every Attribute, Channel, RPC, and event value, every
   persistence name, and every model-visible text. Every core must decode
   each file, re-encode it, and match the expected value in canonical JSON
   (sorted keys, no whitespace, non-ASCII escaped). Each core may store its
   own byte form, and all cores decode each other's. Model-visible text is
   the exception: it must match byte for byte. Every core must also produce
   the same transcripts from the same scripted turns. The suite lives in
   `agents/conformance/`. It is generated from the Python core, checked
   against the TypeScript core, and replaces the manual value-by-value
   checks done in M2 reviews. Generating the cores from a spec
   was rejected: Step and Flow code does not generate well.

2. **The built-in loop first, framework adapters later.** Each language
   starts with Stepwise mode and a pluggable model client, with clients for
   the official provider SDKs:

   | Language | First model clients | Later framework adapters, on demand |
   | --- | --- | --- |
   | Go | `openai-go`, `anthropic-sdk-go` | Google ADK for Go, Eino |
   | Java | LangChain4j chat models | Spring AI, LangChain4j agents, Google ADK for Java |
   | Rust | `async-openai`, an Anthropic client | Rig |

   Python and TypeScript started with adapters because their builders already
   use frameworks. In these three languages, a built-in loop is more useful
   first.

3. **Go calls models in-process, not through the `llm` connector.** The
   parent design placed Dex Agents for Go on the `llm` connector, but that
   connector has no tool calling. In-process clients give all five languages
   the same shape. Tool calling in the `llm` connector is separate connector
   work and may add a Go model client later.

4. **Each language's first release has the full M2 core.** That means
   Stepwise, A1 to A8, approvals, questions, steering and cancellation,
   plans, compaction, and tool tasks, plus the MCP tool source and GenAI
   spans. UI streams ship as plain Server-Sent Events helpers only. The AI
   SDK UI message stream is a JavaScript front-end protocol, and any backend
   can serve it through the TypeScript helpers or plain SSE.

5. **MCP follows each language's official SDK.** Go uses
   `modelcontextprotocol/go-sdk`, Java the MCP Java SDK, and Rust `rmcp`.
   Each core supports what its SDK supports and documents the gaps, as the
   TypeScript core does for tasks at protocol 2025-11-25.

6. **Order: Go, then Java, then Rust.** Go has the hand-built AI Agent
   example, `dexcli visualize` support, the connectors, and the app-builder
   skill. The conformance suite settles on Go before Java and Rust copy it.
   Each language lands in three PRs:
   1. The session core on the M1 contract, Stepwise, and the model clients.
   2. A1 to A8.
   3. MCP, spans, and SSE helpers.

   Each PR passes the conformance suite and a review.

## Needs the owner

7. **Package names and tags.** The owner holds the registry credentials.
   - Go: modules under `github.com/superdurable/dex/agents/go/...`, such as
     `github.com/superdurable/dex/agents/go/core`. Go needs path-prefixed
     tags (`agents/go/core/v0.3.0`), as `sdk-go/v*` already does. The
     release workflow would push those beside `agents/v*` from the same
     version line.
   - Java: Maven Central coordinates such as `io.superdurable:dex-agents`,
     which need a verified namespace.
   - Rust: a crates.io name such as `dex-agents`, checked for availability.

   All five keep the shared 0.x version line.

8. **Product commitment.** Agents in three more languages is an SDK-level
   commitment beyond the Python and TypeScript packages already reviewed.
   Go starts now, because it needs nothing in 7 until its first release.
   Java and Rust start once the owner agrees.

## Tests

- **Conformance:** every core passes `agents/conformance/` in its own CI
  job. The suite holds golden values plus scripted-turn transcripts.
- **Cross-language:** a session started by a Python Client runs on a Go
  Worker, and a TypeScript Client reads its snapshot.
- **Per language:** the M2 crash matrix (SIGKILL at each risky point) and the
  race tests from the Python and TypeScript suites, ported.

## Decision: Java and Rust are deferred

Recorded 2026-11-02. Dex Agents ships for Python, TypeScript, and Go. Java
and Rust are deferred, so decisions 1 to 7 apply to Go only for now.

**Why.** Java and Rust have real agent frameworks, but their communities are
smaller than those of the frameworks Dex Agents already serves. None of the
durable-execution products ships agent integrations for them. GitHub stars on
2026-11-02, every repository active that week:

| Language | Framework | Stars |
| --- | --- | --- |
| Python | LangGraph | 44.3k |
| Python | OpenAI Agents SDK | 30.5k |
| Python | Google ADK | 22.1k |
| Python | Pydantic AI | 21.0k |
| TypeScript | Mastra | 29.3k |
| TypeScript | Vercel AI SDK | 27.7k |
| Go | Eino | 13.5k |
| Go | Google ADK for Go | 8.9k |
| Java | LangChain4j | 13.3k |
| Java | Spring AI | 9.6k |
| Java | Embabel | 4.5k |
| Java | Google ADK for Java | 1.8k |
| Rust | Rig | 9.0k |

The agent integrations of Temporal, Restate, and DBOS target Python and
TypeScript frameworks. Temporal and Restate have Java SDKs, and none of the
three has a Rust SDK.

**Why Go goes ahead anyway.** Go's frameworks are about as large as Java's,
so framework size alone does not set Go apart. Dex itself does:

- the AI Agent example is hand-built in Go today;
- `dexcli visualize` renders Go and Python Flows;
- connectors are Go only;
- the application builder generates Go backends.

**When to revisit.** Revisit when users ask for agents in Java or Rust, for
example a team building on Spring AI, LangChain4j, or Rig. The conformance
suite and the Go core then make a new language a port, not a design.

## Decision: Go stops at the session core

Recorded 2026-11-02. Go gets the session core and nothing past it. Go PR 2
(A2 to A8) and PR 3 (MCP, spans, SSE helpers) are dropped, so decisions 4
and 6 no longer apply to Go.

**What Go has (#1208):**
- the session Flow, approvals and deadlines, single-attempt tools, and the
  turn-failure note;
- A1;
- Stepwise with the OpenAI and Anthropic clients.

It depends on `RPCOptions.Name` in the Go SDK (#1200).

**What Go will not get:** questions, `durable_wait`, steering and
cancellation, plans, compaction, tool tasks, MCP, spans, UI streams, or
framework adapters. The 2026-11-21 Eino decision below reverses the last item.

**What follows from that:**
- A Flow type is served by Workers of one language only. A guard fails a
  Go-served session cleanly if it ever holds state Go does not implement.
- Clients still work across languages: a Python or TypeScript Client can
  drive a Go-served session.
- New contract features land in Python and TypeScript first. Go follows only
  when users ask for it.

## Decision: Go gets Connectors as tools (2026-11-21)

Recorded 2026-11-21, at the request of a co-owner of the project, who asked
for a Go version of an existing agent that reads and writes through Dex
Connectors, tested end to end. That agent calls Connector operations as tools,
so the Go core needs the tool sources that the Python and TypeScript cores
already have. Every Connector ships a Go package (see "Why Go goes ahead
anyway"), so the Go connector library's `sdkgo.AgentTool` is the input. This
narrows "Go gets the session core and nothing past it" above: Go gains the
contract features that Connectors as tools depend on, and the rest of "What Go
will not get" stands.

**What ships (agents 0.6.16):**
- package `connectortools` in the core module: `ConnectorTool` and
  `ConnectorToolSource` over `sdkgo.AgentTool`, a port of the Python and
  TypeScript connector tool sources. Effects and approval come from the
  reviewed agent metadata; argument checks, branch mapping, result bounds,
  send caps, and bounded resends match the other cores, and the Go test runs
  every case of `agents/conformance/connector_tools.json`;
- in the core: `ToolSource`, `ToolSourceRouter`, and `SourceToolListing`;
  `DeferredToolCall.Ordered` with write ordering; `ToolPolicy.MaxSends`;
  `ToolOutcomeUnknownError`; `ToolExecutionResult.TruncatedOutputBytes`;
  `ToolCallStepOptions` with `ToolCallStepOptionsProvider`; and
  `DeferredToolExecution.StepContext`, so a source reads its tool Step whatever
  context it gets. A framework adapter routes a source call before its
  framework's tool node and tool middleware, once per attempt, as the Python
  and TypeScript adapters do, and the session records the source's result;
- `builtinloop.WithToolSources`. A framework adapter serves sources through
  `ToolSourceRouter` the same way.

**Still not in Go:** the MCP tool source, tool tasks, and the approval display
of a connector call, so an approval UI shows the call's arguments. The other
items of "What Go will not get" are unchanged.

**Upgrade rule:** stop or drain every Go Worker of a Flow type built from 0.6.15
or earlier before a Worker with tool sources serves it, and do not roll such a
Flow type back below 0.6.16 while a session has a queued connector write or a
connector call in flight. An older Go Worker never starts a queued write, so
the session stalls, and it records a later attempt of a connector write
`failed` instead of `outcome_unknown`.

## Decision: Go gets the Eino adapter (2026-11-21)

Recorded 2026-11-21, at the request of a co-owner of the project, who asked
for Dex support for CloudWeGo Eino and an Eino version of an existing agent
tested end to end. It reverses "framework adapters" in "What Go will not get"
above. The rest of that decision stands: past the session core, Go gains only
Connectors as tools (the decision above) and the adapter itself.

**What ships:** module `github.com/superdurable/dex/agents/go/eino` (package
`dexeino`) runs an Eino ADK `ChatModelAgent` on the Go core's session Flow.

- **Integration mode: stateless replay Step mode.** Every `RunModel` builds a
  fresh `ChatModelAgent` and `adk.Runner` from the user's
  `ChatModelAgentConfig` and runs it over the stored messages, with no Eino
  `CheckPointStore` and no resume. An adapter Handler, first in `Handlers`,
  turns every tool call into an Eino interrupt, so the run stops after one
  model call. Tool calls run in Dex tool Steps through a one-call
  `compose.ToolsNode` with the agent's tool middlewares and Handler wrappers.
  This is the built-in loop's shape, so a deploy that renames a tool or changes
  the instruction never breaks an open turn. A checkpoint-and-resume mode was
  prototyped and rejected: its checkpoint pins the agent name, the tool list,
  and the instruction for the rest of a turn, and grows four to six times the
  history.
- **Refused at construction:** `ModelRetryConfig`, `ModelFailoverConfig`,
  `Exit`, `ToolsConfig.ReturnDirectly`, the summarization middleware, the
  v0.10 automemory and dream middlewares, and any tool without a Dex effect.
  A model wrapper that tries a second model call ends the turn as failed with
  `TurnCompleted.TurnError`, with the one call billed and no paid retry.
- **Connectors as tools:** `dexeino.WithToolSources` offers a tool source's
  tools to the model beside the agent's own, and runs each call through the
  source in the Dex tool Step, before and outside Eino's tool node, as
  `builtinloop.WithToolSources` does.
- **Versions:** `github.com/cloudwego/eino` v0.9.23 or later; CI also runs the
  suite against the newest v0.10.0 alpha. Component mode, Eino models on the
  built-in loop, does not ship now.
- **Release:** the module requires the core at the agents version plus
  `replace ... => ../core`. First Go tags `agents/go/core/v<version>` and
  `agents/go/eino/v<version>` go on the same commit as `agents/v<version>`,
  which settles decision 7 for Go.

**What the Eino adapter inherits from the Go core:**
- no steering, `cancel_turn`, questions, tool input requests, plans, or
  compaction;
- no images in user messages;
- no GenAI spans and no `ModelCallMetadata`;
- no `TurnContinued`;
- no MCP tool source, tool tasks, or connector approval display;
- one language per Flow type.

The Go core changes the adapter relies on ship in the same release:
`TurnCompleted.TurnError`, status classification from `HTTPStatusCode`, Role
validation before storing messages, session ending at the state limit, and
the tool sources of the decision above.

