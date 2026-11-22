# Agent framework adapters

These packages run agents built with popular agent frameworks as durable Dex
agent sessions. One Flow run is one session. The session runs in Step
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
| [`python/core`](python/core) | [`dex-agents`](https://pypi.org/project/dex-agents/) (`dex_agents`) | Shared session Flow, the built-in agent loop (`dex_agents.builtin_loop`), the LiteLLM model client (`dex_agents.litellm`, extra `dex-agents[litellm]`), connector tools (`dex_agents.connectors`, extra `dex-agents[connectors]`), and `dex_agents.testing` |
| [`python/openai-agents`](python/openai-agents) | [`dex-agents-openai-agents`](https://pypi.org/project/dex-agents-openai-agents/) | OpenAI Agents SDK |
| [`python/pydantic-ai`](python/pydantic-ai) | [`dex-agents-pydantic-ai`](https://pypi.org/project/dex-agents-pydantic-ai/) | Pydantic AI |
| [`python/langgraph`](python/langgraph) | [`dex-agents-langgraph`](https://pypi.org/project/dex-agents-langgraph/) | LangChain `create_agent` and LangGraph |
| [`python/google-adk`](python/google-adk) | [`dex-agents-google-adk`](https://pypi.org/project/dex-agents-google-adk/) | Google ADK |
| [`python/claude-agent-sdk`](python/claude-agent-sdk) | [`dex-agents-claude-agent-sdk`](https://pypi.org/project/dex-agents-claude-agent-sdk/) | Claude Agent SDK |
| [`cli/python/core`](cli/python/core) | `dex-agents-cli-core` (`dex_agents_cli_core`), not published yet | Shared core of the Python CLI commands: configuration, `dexcli dev`, connector and local tools, terminal chat |
| [`cli/python/claude-sdk`](cli/python/claude-sdk) | `dex-agents-claude-sdk-cli`, not published yet | The `dex-agents-claude-sdk` command: a Claude Agent SDK agent in the terminal |
| [`cli/python/openai-sdk`](cli/python/openai-sdk) | `dex-agents-openai-sdk-cli`, not published yet | The `dex-agents-openai-sdk` command: an OpenAI Agents SDK agent in the terminal |
| [`cli/python/pydantic-ai`](cli/python/pydantic-ai) | `dex-agents-pydantic-ai-cli`, not published yet | The `dex-agents-pydantic-ai` command: a Pydantic AI agent in the terminal |
| [`cli/python/deepagents`](cli/python/deepagents) | `dex-agents-deepagents-cli`, not published yet | The `dex-agents-deepagents` command: a Deep Agents agent on the LangGraph adapter in the terminal |
| [`cli/python/google-adk`](cli/python/google-adk) | `dex-agents-google-adk-cli`, not published yet | The `dex-agents-google-adk` command: a Google ADK agent in the terminal |
| [`cli/python/builtin`](cli/python/builtin) | `dex-agents-builtin-cli`, not published yet | The `dex-agents-builtin` command: the Dex built-in agent loop on LiteLLM in the terminal |
| [`cli/python/testing`](cli/python/testing) | `dex-agents-cli-testing`, never published | Test support of the Python commands: model API stubs, fixtures, and shared terminal scenarios |
| [`typescript/core`](typescript/core) | [`@superdurable/dex-agents`](https://www.npmjs.com/package/@superdurable/dex-agents) | Shared session Flow, the built-in agent loop (`/builtin-loop`), the AI SDK model client (`/ai-sdk`, optional peer `ai`), connector tools (`/connectors`, optional peer `@superdurable/connectors-sdk`), and `/testing` |
| [`go/core`](go/core) | `github.com/superdurable/dex/agents/go/core` (package `dexagents`), not tagged yet | Shared session Flow, the built-in agent loop (`builtinloop`), the OpenAI and Anthropic model clients (`openaimodel`, `anthropicmodel`), and `agentstest` |
| [`typescript/vercel-ai`](typescript/vercel-ai) | [`@superdurable/dex-agents-vercel-ai`](https://www.npmjs.com/package/@superdurable/dex-agents-vercel-ai) | Vercel AI SDK |
| [`typescript/openai-agents`](typescript/openai-agents) | [`@superdurable/dex-agents-openai-agents`](https://www.npmjs.com/package/@superdurable/dex-agents-openai-agents) | OpenAI Agents SDK |
| [`typescript/mastra`](typescript/mastra) | [`@superdurable/dex-agents-mastra`](https://www.npmjs.com/package/@superdurable/dex-agents-mastra) | Mastra |
| [`typescript/claude-agent-sdk`](typescript/claude-agent-sdk) | [`@superdurable/dex-agents-claude-agent-sdk`](https://www.npmjs.com/package/@superdurable/dex-agents-claude-agent-sdk) | Claude Agent SDK |
| [`cli/typescript/core`](cli/typescript/core) | `@superdurable/dex-agents-cli-core`, not published yet | Shared core of the TypeScript CLI commands: configuration, `dexcli dev`, connector and local tools, terminal chat |
| [`cli/typescript/claude-sdk`](cli/typescript/claude-sdk) | `@superdurable/dex-agents-claude-sdk-cli`, not published yet | The `dex-agents-claude-sdk` command: a Claude Agent SDK agent in the terminal |
| [`cli/typescript/openai-sdk`](cli/typescript/openai-sdk) | `@superdurable/dex-agents-openai-sdk-cli`, not published yet | The `dex-agents-openai-sdk` command: an OpenAI Agents SDK agent in the terminal |
| [`cli/typescript/vercel-ai`](cli/typescript/vercel-ai) | `@superdurable/dex-agents-vercel-ai-cli`, not published yet | The `dex-agents-vercel-ai` command: a Vercel AI SDK agent in the terminal |
| [`cli/typescript/mastra`](cli/typescript/mastra) | `@superdurable/dex-agents-mastra-cli`, not published yet | The `dex-agents-mastra` command: a Mastra agent in the terminal |
| [`cli/typescript/builtin`](cli/typescript/builtin) | `@superdurable/dex-agents-builtin-cli`, not published yet | The `dex-agents-builtin` command: the Dex built-in agent loop without a framework in the terminal |
| [`typescript/pi`](typescript/pi) | `@superdurable/dex-agents-pi`, not published yet | Pi: pi-ai models and Pi tools on the built-in agent loop, so Dex runs the loop, not Pi |
| [`cli/typescript/pi`](cli/typescript/pi) | `@superdurable/dex-agents-pi-cli`, not published yet | The `dex-agents-pi` command: a Pi coding agent in the terminal |

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

Agents without a framework use the built-in agent loop in the core package. Install
the core with the LiteLLM model client:

```bash
pip install 'dex-agents[litellm]'
```

New features ship as modules of the existing packages. A dependency that only
one module needs is an extra, such as `dex-agents[litellm]`, so no new
package name is registered.

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

The twenty-six PyPI and npm packages share one version line, starting at
0.1.0, and the Go modules under [`go/`](go) take the same versions. The line is
independent of the Dex SDK versions and of the Dex Skills: frameworks move
faster than Dex, and the adapter API is still before 1.0, so a minor release
may change it. One release publishes the whole family under the tag
`agents/v<version>`, and tags each Go module `agents/go/<module>/v<version>`
on the same commit. Each package pins the Dex SDK it supports and keeps the
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
| 0.2.0 | `dex-python-sdk==1.35.0` | `dex-agents-openai-agents` | `openai-agents>=0.24,<0.25` | 0.24.0 |
| 0.2.0 | `dex-python-sdk==1.35.0` | `dex-agents-pydantic-ai` | `pydantic-ai-slim>=2.64,<3` | 2.64.0 |
| 0.2.0 | `dex-python-sdk==1.35.0` | `dex-agents-langgraph` | `langchain>=1.4.7,<2`, `langchain-core>=1.7.3,<2`, `langgraph>=1.2.19,<2` | 1.4.7, 1.7.3, 1.2.19 |
| 0.2.0 | `dex-python-sdk==1.35.0` | `dex-agents-google-adk` | `google-adk>=2.13,<3` | 2.13.0 |
| 0.2.0 | `dex-python-sdk==1.35.0` | `dex-agents-claude-agent-sdk` | `claude-agent-sdk>=0.2.176,<0.3` | 0.2.176 |
| 0.2.0 | `@superdurable/dex@1.35.0` | `@superdurable/dex-agents-vercel-ai` | `ai@^7.0.159` | 7.0.159 |
| 0.2.0 | `@superdurable/dex@1.35.0` | `@superdurable/dex-agents-openai-agents` | `@openai/agents@^0.21.0` | 0.21.0 |
| 0.2.0 | `@superdurable/dex@1.35.0` | `@superdurable/dex-agents-mastra` | `@mastra/core@^1.81.0` | 1.81.0 |
| 0.2.0 | `@superdurable/dex@1.35.0` | `@superdurable/dex-agents-claude-agent-sdk` | `@anthropic-ai/claude-agent-sdk@^0.3.316` | 0.3.316 |
| 0.3.0 | `dex-python-sdk==1.38.0` | `dex-agents-openai-agents` | `openai-agents>=0.24,<0.25` | 0.24.0 |
| 0.3.0 | `dex-python-sdk==1.38.0` | `dex-agents-pydantic-ai` | `pydantic-ai-slim>=2.64,<3` | 2.64.0 |
| 0.3.0 | `dex-python-sdk==1.38.0` | `dex-agents-langgraph` | `langchain>=1.4.7,<2`, `langchain-core>=1.7.3,<2`, `langgraph>=1.2.19,<2` | 1.4.7, 1.7.3, 1.2.19 |
| 0.3.0 | `dex-python-sdk==1.38.0` | `dex-agents-google-adk` | `google-adk>=2.13,<3` | 2.13.0 |
| 0.3.0 | `dex-python-sdk==1.38.0` | `dex-agents-claude-agent-sdk` | `claude-agent-sdk>=0.2.176,<0.3` | 0.2.176 |
| 0.3.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-vercel-ai` | `ai@^7.0.159` | 7.0.159 |
| 0.3.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-openai-agents` | `@openai/agents@^0.21.0` | 0.21.0 |
| 0.3.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-mastra` | `@mastra/core@^1.81.0` | 1.81.0 |
| 0.3.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-claude-agent-sdk` | `@anthropic-ai/claude-agent-sdk@^0.3.316` | 0.3.316 |
| 0.4.0 | `dex-python-sdk==1.38.0` | `dex-agents-openai-agents` | `openai-agents>=0.24,<0.25` | 0.24.0 |
| 0.4.0 | `dex-python-sdk==1.38.0` | `dex-agents-pydantic-ai` | `pydantic-ai-slim>=2.64,<3` | 2.64.0 |
| 0.4.0 | `dex-python-sdk==1.38.0` | `dex-agents-langgraph` | `langchain>=1.4.7,<2`, `langchain-core>=1.7.3,<2`, `langgraph>=1.2.19,<2` | 1.4.7, 1.7.3, 1.2.19 |
| 0.4.0 | `dex-python-sdk==1.38.0` | `dex-agents-google-adk` | `google-adk>=2.13,<3` | 2.13.0 |
| 0.4.0 | `dex-python-sdk==1.38.0` | `dex-agents-claude-agent-sdk` | `claude-agent-sdk>=0.2.176,<0.3` | 0.2.176 |
| 0.4.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-vercel-ai` | `ai@^7.0.159` | 7.0.159 |
| 0.4.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-openai-agents` | `@openai/agents@^0.21.0` | 0.21.0 |
| 0.4.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-mastra` | `@mastra/core@^1.81.0` | 1.81.0 |
| 0.4.0 | `@superdurable/dex@1.38.0` | `@superdurable/dex-agents-claude-agent-sdk` | `@anthropic-ai/claude-agent-sdk@^0.3.316` | 0.3.316 |
| 0.5.0 | `dex-python-sdk==1.45.0` | `dex-agents-openai-agents` | `openai-agents>=0.24,<0.25` | 0.24.0 |
| 0.5.0 | `dex-python-sdk==1.45.0` | `dex-agents-pydantic-ai` | `pydantic-ai-slim>=2.64,<3` | 2.64.0 |
| 0.5.0 | `dex-python-sdk==1.45.0` | `dex-agents-langgraph` | `langchain>=1.4.7,<2`, `langchain-core>=1.7.3,<2`, `langgraph>=1.2.19,<2` | 1.4.7, 1.7.3, 1.2.19 |
| 0.5.0 | `dex-python-sdk==1.45.0` | `dex-agents-google-adk` | `google-adk>=2.13,<3` | 2.13.0 |
| 0.5.0 | `dex-python-sdk==1.45.0` | `dex-agents-claude-agent-sdk` | `claude-agent-sdk>=0.2.176,<0.3` | 0.2.176 |
| 0.5.0 | `@superdurable/dex@1.45.0` | `@superdurable/dex-agents-vercel-ai` | `ai@^7.0.159` | 7.0.159 |
| 0.5.0 | `@superdurable/dex@1.45.0` | `@superdurable/dex-agents-openai-agents` | `@openai/agents@^0.21.0` | 0.21.0 |
| 0.5.0 | `@superdurable/dex@1.45.0` | `@superdurable/dex-agents-mastra` | `@mastra/core@^1.81.0` | 1.81.0 |
| 0.5.0 | `@superdurable/dex@1.45.0` | `@superdurable/dex-agents-claude-agent-sdk` | `@anthropic-ai/claude-agent-sdk@^0.3.316` | 0.3.316 |
| 0.5.0 | `@superdurable/dex@1.45.0` | `@superdurable/dex-agents-pi` | `@earendil-works/pi-ai@^1.5.1` | 1.5.1 |

A Python adapter requires `dex-agents==<same version>`. An npm adapter
peer-depends on `@superdurable/dex-agents@<same version>`. Add a row for
every release. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

0.2.0 uses Dex SDK v1.35.0 in every language, including the Go core
(`github.com/superdurable/dex/sdk-go` `v1.35.0`). That SDK needs a Dex Server
with protocol 4: server-zd v0.1.17 or later. Upgrade the server before the
Workers.

0.3.0 (released 2026-11-09 as `agents/v0.3.0`) and 0.4.0 (released 2026-11-10
as `agents/v0.4.0`) use Dex SDK v1.38.0 in every language, including the Go
core (`github.com/superdurable/dex/sdk-go` `v1.38.0`). That SDK also needs a
Dex Server with protocol 4: server-zd v0.1.17 or later. Upgrade the server
before the Workers.

0.5.0 uses Dex SDK v1.45.0 in every language, including the Go core
(`github.com/superdurable/dex/sdk-go` `v1.45.0`). Before upgrading, read the
breaking changes of SDK 1.43 and 1.45 in the SDK change logs. That SDK also needs a Dex Server with protocol 4: server-zd v0.1.17
or later.

## Releases

One release publishes all twenty-six PyPI and npm packages at one version, in
dependency order: the core, the adapters, the CLI core, then the CLI commands.
`dex-agents-cli-testing` is never published. The same release tags each Go
module under [`go/`](go), such as `github.com/superdurable/dex/agents/go/core`,
at that version. The Go modules are counted apart from the twenty-six: no
workflow uploads them, because `go get` fetches each one from its tag
`agents/go/<module>/v<version>` through the Go module proxy.
[`agents-python-publish.yml`](../.github/workflows/agents-python-publish.yml)
uploads to PyPI and
[`agents-typescript-publish.yml`](../.github/workflows/agents-typescript-publish.yml)
uploads to npm with provenance, both through trusted publishing. A prerelease
version such as `0.2.0-rc.1` gets the npm dist-tag `next`.

Both workflows run only when dispatched. Creating or publishing a GitHub
Release never uploads packages. A dispatch stamps, builds, and smoke-tests
without uploading unless `publish` is selected. A run that uploads starts
from `main` or from the tag `agents/v<version>`, which must be on `main`.
Dispatch it on the tag, so it builds the released commit even if `main` has
moved on.

### Release steps

1. Make every Go module require the other agents Go modules at the release
   version on `main`. Go builds the `go.mod` of the tagged commit and skips
   its `replace` directives, so a module that requires the core must
   require `v<version>` there. The PyPI and npm versions need no such
   commit, because the publishers stamp them on the runner. Check it on a
   fresh `main`:

   ```bash
   python3 agents/script/stamp_agents_release_version.py <version> --go --check
   ```

   When the check fails, stamp the requires and merge the change in its own
   pull request before the release-prep pull request, so the tag can follow
   that merge. The stamp keeps each `replace ... => ../<module>`, so the
   repository still builds the local modules:

   ```bash
   python3 agents/script/stamp_agents_release_version.py <version> --go
   ```

2. Merge the release-prep pull request. It writes the `## <version>` entry of
   `agents/CHANGELOG.md` and the entry of the Developer reference change log.
   See [`changelog.d/README.md`](../changelog.d/README.md#release):

   ```bash
   python3 script/release/changelog_fragments.py prepare agents --version <version> --date <YYYY-MM-DD>
   ```

3. On the release commit, a commit on `main`, check the Go modules. Then push
   `agents/v<version>` and one tag per Go module on that same commit:

   ```bash
   git switch --detach <commit on main>
   python3 script/release/check_agents_go_modules.py --version <version>
   ```

   The check prints the Go tag of each module under [`go/`](go), such as
   `agents/go/core/v<version>`. Tag the commit with `agents/v<version>` and
   every printed tag, and push them together:

   ```bash
   git tag agents/v<version>
   git tag agents/go/core/v<version>
   git push origin agents/v<version> agents/go/core/v<version>
   ```

   Add a `git tag` line and a push argument for each other printed tag. Then
   confirm that the Go module proxy serves each module at the release
   version:

   ```bash
   GOPROXY=https://proxy.golang.org go list -m github.com/superdurable/dex/agents/go/core@v<version>
   ```

   It prints the module path and `v<version>`. The proxy keeps a version
   once it has served it, so never move or delete a pushed Go tag. Fix a
   wrong one with the next patch release.

4. Publish the npm packages:

   ```bash
   gh workflow run agents-typescript-publish.yml --ref agents/v<version> \
     -f version=<version> -f publish=true
   ```

5. Publish the PyPI packages. A release with no new PyPI project needs one
   run with `skip_packages` empty:

   ```bash
   gh workflow run agents-python-publish.yml --ref agents/v<version> \
     -f version=<version> -f publish=true
   ```

   A release with new PyPI projects needs one run per new project, each
   skipping the new commands that are not registered yet; see
   [Releasing new PyPI projects](#releasing-new-pypi-projects):

   ```bash
   gh workflow run agents-python-publish.yml --ref agents/v<version> \
     -f version=<version> -f publish=true \
     -f skip_packages="<new commands not yet registered>"
   ```

6. Create the GitHub Release by hand, after the day's publisher runs succeed. Its notes
   come from [`script/release/component_release_notes.py`](../script/release/component_release_notes.py).
   Pass the previous `agents/v*` tag. Attach the wheels of CLI commands that
   are not on PyPI yet, so users can install them before a later day's upload.
   Take them from the `agents-python-distributions` artifact of a Python
   publisher run on the tag.

   Write the notes:

   ```bash
   python3 script/release/component_release_notes.py agents <version> \
     --previous-tag agents/v<previous version> --output agents-release-notes.md
   ```

   When every package is on PyPI, create the Release:

   ```bash
   gh release create agents/v<version> --verify-tag --title "Dex agents <version>" \
     --notes-file agents-release-notes.md --latest=false
   ```

   When some CLI commands still wait, download the wheels and attach one per
   waiting command:

   ```bash
   gh run download <Python publisher run ID> --name agents-python-distributions --dir agents-dist
   gh release create agents/v<version> --verify-tag --title "Dex agents <version>" \
     --notes-file agents-release-notes.md --latest=false \
     agents-dist/dex_agents_<command>_cli-<version>-*.whl
   ```

7. Snapshot the agents Developer reference on `main` with
   `node docs/scripts/snapshot-reference.mjs agents <version>`, run
   `npm run check --prefix docs`, and open its docs pull request. See
   [Release automation](../docs/README.md#release-automation).

Both workflows stamp the release version first with
[`script/stamp_agents_release_version.py`](script/stamp_agents_release_version.py).
It sets one version in all fourteen `pyproject.toml` files (the thirteen
published ones and `dex-agents-cli-testing`) and thirteen `package.json`
files, their lockfiles, and every constraint between agents packages: the
adapters' on their core, and the CLI packages' on the core, their adapter,
and the CLI core. Python pins `==<version>`; npm peer-depends on
`<version>` exactly. Run it locally to bump the committed version, and pass
`--check` to verify it. With `--go`, it sets only the requires between the Go
modules (`go/<module>/go.mod`) to `v<version>`. It fails on such a require
without its `replace ... => ../<module>`, and on a version that Go cannot
tag. Commit that change before the tag, as step 1 says.
[`check_agents_go_modules.py`](../script/release/check_agents_go_modules.py)
also checks that each Go module requires the other Dex modules at released
versions, never at a pseudo-version of an unreleased commit. It also rejects
any other `replace` or `exclude` directive, because a consumer's build skips
them. Agents CI runs it on every change under `agents/`.

[`script/smoke-test-built-packages.sh`](script/smoke-test-built-packages.sh)
builds every package with `uv build` and `npm pack`, checks the artifacts
including exact npm sibling peer versions, and installs them into fresh projects
without path dependencies: the core first, then each adapter. It imports every TypeScript core subpath with and
without the optional peer `ai`. It then runs one Pydantic AI session and one Vercel
AI SDK session against `dexcli dev --engine zd` with a scripted model, using
the adapters' own test fixtures. Last, it installs the CLI core and each
CLI command from the built artifacts alone and runs each command's `--help`
and `init --yes` with a scratch `HOME`; neither needs a model key or a Dex
server. Agents CI runs it on every change, so packaging regressions fail CI:

```bash
DEXCLI=/path/to/dexcli agents/script/smoke-test-built-packages.sh all
```

### Releasing new PyPI projects

A new PyPI project needs a pending trusted publisher before its first
upload. PyPI allows one pending publisher at a time, and it accepts only
about five new projects per day. So a release with new PyPI projects uploads
them one per run, and may span several days.

The Python workflow's `skip_packages` input leaves CLI command distributions
out of one upload. It accepts only command names, because nothing else in the
release depends on a command. So a release can add at most one new project
that is not a CLI command, and that project uploads in the first run. Every
run builds and smoke-tests all thirteen distributions, and skips each file
PyPI already has. A run that reaches a new project without a pending
publisher fails there; the next run uploads the rest.

For each new project, in turn:

1. Register its pending trusted publisher on PyPI for
   `agents-python-publish.yml`.
2. Dispatch the Python workflow on the tag, with `skip_packages` naming every
   new command that is not registered yet:

   ```bash
   gh workflow run agents-python-publish.yml --ref agents/v<version> \
     -f version=<version> -f publish=true \
     -f skip_packages="<new commands not yet registered>"
   ```

3. After the run succeeds, the project exists and its publisher is no longer
   pending. Register the next one.

The last run leaves `skip_packages` empty. When the daily limit stops the
uploads, create the GitHub Release anyway and attach the wheels of the
commands still waiting.

### Release changed components

**Release changed components** can release the agents packages together with
the other components: set its `agents_version` input. It creates the tag and
the Release, pushes the tag of each Go module on the same commit, then
dispatches both workflows on the tag once, passing
`agents_python_skip_packages` as `skip_packages`. Its plan runs
`check_agents_go_modules.py` for `agents_version` first, so merge the Go
stamp of step 1 before you dispatch it. Release the remaining new
PyPI projects with the steps above. It releases every component that changed
since its last release at the shared `version`. So it cannot release the
agents packages alone while other components have changed; use the release
steps above instead.

## Adding a package

1. Create `agents/<language>/<package>/` with its manifest, lockfile, README,
   LICENSE, and tests. Python packages use uv 0.12.2, require
   `dex-agents==<version>`, and resolve it locally with
   `dex-agents = { path = "../core", editable = true }` in `[tool.uv.sources]`.
   npm packages list `@superdurable/dex-agents` as an exact `<version>` peer
   dependency and as a `file:../core` devDependency, with `install-links=true`
   in their `.npmrc` so the local core is copied, not linked. Go modules
   declare `github.com/superdurable/dex/agents/go/<module>`, require another
   agents Go module at the release version with
   `replace github.com/superdurable/dex/agents/go/<module> => ../<module>`,
   and require the other Dex modules at released versions.
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
and the tables above, in dependency order. A Go module needs no list entry:
the stamp, the Go module check, and the release find every
`agents/go/*/go.mod`. In the scripts, the package in
`agents/cli/<language>/<name>` is `cli/<name>`. A new package name needs its trusted publishing set up
on PyPI or npm before the first release that publishes it. Source
files use the
Sustainable Use License header; `make copyright-check` verifies it.
