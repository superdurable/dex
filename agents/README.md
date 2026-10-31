# Agent framework adapters

These packages run agents built with popular agent frameworks as durable Dex
agent sessions. One Flow execution is one session. The session runs in Step
mode: every model call is one Dex Step and every tool call is its own Step,
with tool calls running in parallel, so a crash repeats at most the one call
that was running and never repeats a write silently. The plan is
[agent-framework-adapters.md](../docs/design/plan/agent-framework-adapters.md).

Each language has a core package with the shared session Flow and one adapter
package per framework. Every package has its own manifest and lockfile,
depends on its language core by path, and depends on the released Dex SDK from
the public registry.

| Package | Distribution | Framework | Status |
| --- | --- | --- | --- |
| [`python/core`](python/core) | `dex-agents` (`dex_agents`) | Shared session Flow and `dex_agents.testing` | Available |
| `python/openai-agents` | `dex-agents-openai-agents` | OpenAI Agents SDK | Planned |
| `python/pydantic-ai` | `dex-agents-pydantic-ai` | Pydantic AI | Planned |
| `python/langgraph` | `dex-agents-langgraph` | LangChain `create_agent` and LangGraph | Planned |
| `python/google-adk` | `dex-agents-google-adk` | Google ADK | Planned |
| `python/claude-agent-sdk` | `dex-agents-claude-agent-sdk` | Claude Agent SDK | Planned |
| `typescript/core` | `@superdurable/dex-agents` | Shared session Flow | Planned |
| `typescript/vercel-ai` | `@superdurable/dex-agents-vercel-ai` | Vercel AI SDK | Planned |
| `typescript/openai-agents` | `@superdurable/dex-agents-openai-agents` | OpenAI Agents SDK | Planned |
| `typescript/mastra` | `@superdurable/dex-agents-mastra` | Mastra | Planned |
| `typescript/claude-agent-sdk` | `@superdurable/dex-agents-claude-agent-sdk` | Claude Agent SDK | Planned |

## Adding a package

1. Create `agents/<language>/<package>/` with its manifest, lockfile, README,
   and tests. Python packages use uv 0.12.2 and depend on the core with
   `dex-agents = { path = "../core", editable = true }`.
2. Implement the runner contract in the
   [Python core README](python/core/README.md#the-runner-contract).
3. Test against a real Dex Server with the core's testing utilities and a
   scripted model, so no model key is needed. Cover the plan's test matrix.
4. Add an executable `ci.sh` that installs locked dependencies, runs lint and
   type checks, and runs the integration tests. It reads `DEXCLI`, the
   `dexcli` executable; `dex-server-zd` sits in the same directory.

`Agents CI` finds every executable `agents/*/*/ci.sh` and runs each in its own
job, so a new package needs no workflow change. Source files use the
Sustainable Use License header; `make copyright-check` verifies it.
