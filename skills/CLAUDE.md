# Dex Skills Repository Instructions

The source of truth is superdurable/dex `skills/`. The read-only
`superdurable/dex-skills` mirror accepts no pull requests. Dex runs the
validators in `.github/workflows/skills-ci.yml`.

This repository publishes one `superdurable-dex` plugin for Codex, Claude Code,
and Cursor. Its only public skills are `dex-sdk`, `dex-app-builder`, and
`dex-connector-contributor`, stored directly at the repository root.

## Skill boundaries

- Public Skills contain reusable decision rules and platform guidance, not
  application-specific scenarios, product names, or named regression examples.
  When a real application exposes a modeling failure, extract the general
  decision criteria and validate those invariants generically. Keep
  application-specific architecture and regression fixtures in that
  application's repository or its issue/PR context, not in the published
  Skills.
- All three Skills may be selected by matching natural-language descriptions.
- `dex-app-builder` is the default end-to-end product workflow for business
  discovery, Flow and FDG modeling, an explicit no-custom-UI or custom-UI
  decision, Go backend implementation, and local verification with source-available
  Dex tooling.
- `dex-sdk` is the specialist for implementing, debugging, testing, and
  operating applications through the public Dex SDK.
- `dex-connector-contributor` is the thin bootstrap for official
  connector-library work. It resolves the correct checkout, then defers to that
  repository's agent rules and documentation.
- If App Builder receives a standalone SDK or official connector-library
  request, route it to the matching specialist before product discovery. A
  product or application that uses a released connector remains in App Builder.
- If the user explicitly invokes a specialist, honor that choice and do not
  expand it into the App Builder workflow.
- During backend implementation, `dex-app-builder` loads `dex-sdk` and follows
  only its Core and Go guidance. Do not duplicate those references.
- `dex-connector-contributor` loads `dex-sdk` Core and Go guidance when needed.
  The target connector repository owns connector-specific implementation,
  acceptance, release, and pull-request rules; `dex-app-builder` owns
  application use of released connectors.
- App Builder constraints are stricter: Go only, strict FDG 2.0, provider effects
  only in `Execute`, and no provider or Dex mutations in `WaitFor`.

## Skill content: the stable layer for agents

The Skills are the coding-agent version of Dex's stable docs layer. They cover
the same scope as the docs Primitives, Components, and Design pattern pages.
Write that content for agents: mental models, decision tables, rules,
anti-patterns, code skeletons, and checklists. Keep every stable fact an agent
needs to write correct code. Each rule must be complete without its links.

A fact belongs here only if it will still hold in the next minor release.
Skills contain no Dex version numbers and no release-tag pins. APIs,
parameters, defaults, limits, configuration, flags, error codes, and version
differences belong in SDK doc comments and the versioned docs Developer
reference. Link to the Developer reference and to `examples/` for those
details. Never link to the docs Primitives, Components, or Design pattern
pages.

Change the Skills only when a stable programming rule, a Primitive concept or
relation, a Component, or a design pattern changes. Update the matching docs
pages in the same pull request, or state in the pull request why they need no
change. A skills change does not wait for a Dex release.

## Public product boundary

Dex Skills is public Dex material and contains no information about any
product built on Dex (no product names, template contracts or platform
concepts). Describe only source-available Dex: the Dex SDKs, `dexcli` including
`dexcli dev`, Dex Server (server-zd), Dex Web, the FDG analyzer, and the
official connectors. A product built on Dex keeps its own platform-specific guidance in
its own repository; nothing here refers to it.

## Dex concept boundary

Skills describe Dex concepts and guarantees only. Never mention
Continue-As-New in any skill file, including its threshold and trigger. Do not
describe Temporal or Cadence internals (activity timeout names, Signals,
Queries, Updates, workflow history mechanics) in application guidance; state the
Dex behavior and guarantee instead. When a guarantee differs by backend, say so
in Dex terms without explaining the backend mechanism. Deployment and operations
guidance may name server-zd and its configuration, such as object storage, the
PostgreSQL Attribute Store, and the search backend. Do not name Temporal or
Cadence; Dex Server (server-zd) is the only Dex Server. `check-package.py`
rejects these terms.

## Packaging and release

Keep Codex, Claude Code, and Cursor manifests synchronized on plugin ID,
version, repository, skill paths, and display metadata. Do not reintroduce a
`plugins/` wrapper or an unplanned public skill.

The Plugin and standalone Skills are mutually exclusive installation paths.
Standalone installation always copies all three Skills together. Every local
Markdown link must resolve using only those three copied directories; never
depend on a repository-root file that an Agent Skills installer omits.

Never add a skill submodule or a floating branch reference to an application
repository; the coding-agent host supplies the installed Dex Skills release.

Bump `VERSION` and add a `CHANGELOG.md` entry only when stable content
changes: a rule, a Primitive concept or relation, a Component, a design
pattern, skill routing, or packaging. A typo, link, or wording-only change
needs no bump; its pull request description contains a line
`skills-release: no bump needed: <reason>`, which Check Gate CI accepts
instead of the bump. A Dex release never requires a skills change.

`doc-map.yaml` maps each skills file to the docs pages that hold the same
stable knowledge. Check Gate CI fails a pull request that changes a mapped
file on one side but none of its counterparts, unless its description contains a line
`doc-map: no change needed: <reason>`. Keep the map current when you add, move,
or delete a mapped file. A new skills file or doc-map docs page needs a mapping
or an `unmapped` line.
