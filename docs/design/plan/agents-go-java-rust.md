# Dex Agents for Go, Java, and Rust

Status: Go in progress; Java and Rust deferred (2026-11-02). Parent: [agents-m2-stepwise.md](agents-m2-stepwise.md)
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
   each file and re-encode it byte for byte, and must produce the same values
   from the same scripted turns. The suite lives in `agents/conformance/`,
   is generated from the Python and TypeScript cores, and replaces the manual
   value-by-value checks done in M2 reviews. Generating the cores from a spec
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
