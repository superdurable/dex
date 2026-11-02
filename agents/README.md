# Agent framework adapters

These packages run agents built with popular agent frameworks as durable Dex
agent sessions. One Flow execution is one session. The session runs in Step
mode: every model call is one Dex Step and every tool call is its own Step,
with tool calls running in parallel, so a crash repeats at most the one call
that was running and never repeats a write silently. The plan is
[agent-framework-adapters.md](../docs/design/plan/agent-framework-adapters.md).

Each language has a core package with the shared session Flow and one adapter
package per framework. Every package has its own manifest and lockfile and
depends on the released Dex SDK from the public registry. Inside this
repository an adapter depends on its language core by path; the published
adapter depends on the published core instead.

| Package | Distribution | Framework |
| --- | --- | --- |
| [`python/core`](python/core) | [`dex-agents`](https://pypi.org/project/dex-agents/) (`dex_agents`) | Shared session Flow and `dex_agents.testing` |
| [`python/openai-agents`](python/openai-agents) | [`dex-agents-openai-agents`](https://pypi.org/project/dex-agents-openai-agents/) | OpenAI Agents SDK |
| [`python/pydantic-ai`](python/pydantic-ai) | [`dex-agents-pydantic-ai`](https://pypi.org/project/dex-agents-pydantic-ai/) | Pydantic AI |
| [`python/langgraph`](python/langgraph) | [`dex-agents-langgraph`](https://pypi.org/project/dex-agents-langgraph/) | LangChain `create_agent` and LangGraph |
| [`python/google-adk`](python/google-adk) | [`dex-agents-google-adk`](https://pypi.org/project/dex-agents-google-adk/) | Google ADK |
| [`python/claude-agent-sdk`](python/claude-agent-sdk) | [`dex-agents-claude-agent-sdk`](https://pypi.org/project/dex-agents-claude-agent-sdk/) | Claude Agent SDK |
| [`typescript/core`](typescript/core) | [`@superdurable/dex-agents`](https://www.npmjs.com/package/@superdurable/dex-agents) | Shared session Flow and `@superdurable/dex-agents/testing` |
| [`typescript/vercel-ai`](typescript/vercel-ai) | [`@superdurable/dex-agents-vercel-ai`](https://www.npmjs.com/package/@superdurable/dex-agents-vercel-ai) | Vercel AI SDK |
| [`typescript/openai-agents`](typescript/openai-agents) | [`@superdurable/dex-agents-openai-agents`](https://www.npmjs.com/package/@superdurable/dex-agents-openai-agents) | OpenAI Agents SDK |
| [`typescript/mastra`](typescript/mastra) | [`@superdurable/dex-agents-mastra`](https://www.npmjs.com/package/@superdurable/dex-agents-mastra) | Mastra |
| [`typescript/claude-agent-sdk`](typescript/claude-agent-sdk) | [`@superdurable/dex-agents-claude-agent-sdk`](https://www.npmjs.com/package/@superdurable/dex-agents-claude-agent-sdk) | Claude Agent SDK |

## UI streams

Both cores ship UI stream adapters, so a browser can render a live session
from its `AgentEvents` Stream and resume after a reconnect with no store
besides Dex. `@superdurable/dex-agents/ui-stream` serves an AI SDK UI message
stream for `useChat`, with `reset-step` for a retried model call and tool
approvals through `tool-approval-request` and `tool-approval-response`.
Both `@superdurable/dex-agents/ui-stream` and `dex_agents.ui_stream` serve
plain Server-Sent Events whose `id` is the Dex resume token, so an
`EventSource` resumes through `Last-Event-ID`, and a snapshot to load on
every connection. See the
[TypeScript core](typescript/core/README.md#ui-streams) and the
[Python core](python/core/README.md#browser-streams).

## Installation

Install an adapter with its framework. The Python adapter brings in
`dex-agents` and the Dex SDK it supports:

```bash
pip install dex-agents-pydantic-ai
```

On npm, the core and the Dex SDK are peer dependencies, so the application
owns exactly one copy of each and errors such as `ToolExecutionError` keep one
identity. Install them next to the adapter:

```bash
npm i @superdurable/dex-agents-vercel-ai @superdurable/dex-agents @superdurable/dex ai zod
```

## Versions and compatibility

All eleven packages share one version line, starting at 0.1.0. It is
independent of the Dex SDK versions and of the Dex Skills: frameworks move
faster than Dex, and the adapter API is still before 1.0, so a minor release
may change it. One release publishes the whole family under one tag,
`agents/v<version>`. Each package pins the Dex SDK it supports and keeps the
framework range it was tested against.

| Agents version | Dex SDK | Package | Framework range | Tested with |
| --- | --- | --- | --- | --- |
| 0.1.0 | `dex-python-sdk==1.30.0` | `dex-agents-openai-agents` | `openai-agents>=0.24,<0.25` | 0.24.0 |
| 0.1.0 | `dex-python-sdk==1.30.0` | `dex-agents-pydantic-ai` | `pydantic-ai-slim>=2.64,<3` | 2.64.0 |
| 0.1.0 | `dex-python-sdk==1.30.0` | `dex-agents-langgraph` | `langchain>=1.4.7,<2`, `langchain-core>=1.7.3,<2`, `langgraph>=1.2.19,<2` | 1.4.7, 1.7.3, 1.2.19 |
| 0.1.0 | `dex-python-sdk==1.30.0` | `dex-agents-google-adk` | `google-adk>=2.13,<3` | 2.13.0 |
| 0.1.0 | `dex-python-sdk==1.30.0` | `dex-agents-claude-agent-sdk` | `claude-agent-sdk>=0.2.176,<0.3` | 0.2.176 |
| 0.1.0 | `@superdurable/dex@1.30.0` | `@superdurable/dex-agents-vercel-ai` | `ai@^7.0.159` | 7.0.159 |
| 0.1.0 | `@superdurable/dex@1.30.0` | `@superdurable/dex-agents-openai-agents` | `@openai/agents@^0.21.0` | 0.21.0 |
| 0.1.0 | `@superdurable/dex@1.30.0` | `@superdurable/dex-agents-mastra` | `@mastra/core@^1.81.0` | 1.81.0 |
| 0.1.0 | `@superdurable/dex@1.30.0` | `@superdurable/dex-agents-claude-agent-sdk` | `@anthropic-ai/claude-agent-sdk@^0.3.316` | 0.3.316 |

A Python adapter requires `dex-agents==<same version>`. An npm adapter
peer-depends on `@superdurable/dex-agents@^<same version>`. Add a row for
every release. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Releases

Publishing a GitHub Release with the tag `agents/v<version>` from a commit on
`main` publishes all eleven packages: the core first, then the adapters.
[`agents-python-publish.yml`](../.github/workflows/agents-python-publish.yml)
uploads to PyPI and
[`agents-typescript-publish.yml`](../.github/workflows/agents-typescript-publish.yml)
uploads to npm with provenance, both through trusted publishing. A prerelease
version such as `0.2.0-rc.1` gets the npm dist-tag `next`. Running either
workflow by hand stamps, builds, and smoke-tests without uploading unless
`publish` is selected. **Release changed components** releases the family
when `agents/**` changed and its `agents_version` input is set.

Both workflows stamp the release version first with
[`script/stamp_agents_release_version.py`](script/stamp_agents_release_version.py).
It sets one version in all six `pyproject.toml` and five `package.json`
files, their lockfiles, and the adapters' constraints on their core. Run it
locally to bump the committed version, and pass `--check` to verify it.

[`script/smoke-test-built-packages.sh`](script/smoke-test-built-packages.sh)
builds every package with `uv build` and `npm pack`, checks the artifacts,
and installs them into fresh projects without path dependencies: the core
first, then each adapter. It then runs one Pydantic AI session and one Vercel
AI SDK session against `dexcli dev --engine zd` with a scripted model, using
the adapters' own test fixtures. Agents CI runs it on every change, so
packaging regressions fail CI:

```bash
DEXCLI=/path/to/dexcli agents/script/smoke-test-built-packages.sh all
```

## Adding a package

1. Create `agents/<language>/<package>/` with its manifest, lockfile, README,
   LICENSE, and tests. Python packages use uv 0.12.2, require
   `dex-agents==<version>`, and resolve it locally with
   `dex-agents = { path = "../core", editable = true }` in `[tool.uv.sources]`.
   npm packages list `@superdurable/dex-agents` as a `^<version>` peer
   dependency and as a `file:../core` devDependency, with `install-links=true`
   in their `.npmrc` so the local core is copied, not linked.
2. Implement the runner contract in the
   [Python core README](python/core/README.md#the-runner-contract).
3. Test against a real Dex Server with the core's testing utilities and a
   scripted model, so no model key is needed. Cover the plan's test matrix.
4. Add an executable `ci.sh` that installs locked dependencies, runs lint and
   type checks, and runs the integration tests. It reads `DEXCLI`, the
   `dexcli` executable; `dex-server-zd` sits in the same directory.

`Agents CI` finds every executable `agents/*/*/ci.sh` and runs each in its own
job, so a new package needs no workflow change. Add a new package to the
package lists in the stamping script, the smoke test, both publish workflows,
and the tables above. Source files use the
Sustainable Use License header; `make copyright-check` verifies it.
