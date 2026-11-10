# Changelog

Release notes for Dex Skills 1.0.0 and later. The notes for 0.1.0 through
0.69.2 are archived in [CHANGELOG-0.x.md](CHANGELOG-0.x.md).

## 1.0.5 - 2026-11-09

- Remove the Dex Web v2 product name from `dex-app-builder`. There is one Dex Web. Its Flow Definition Graph features are labeled Experimental, and Start Flow is in the Work Queue list header.

## 1.0.4 - 2026-11-09

- Correct the Flow result rule in `dex-sdk`: only a COMPLETED Flow keeps the outputs of its completing decisions. A Flow that fails, is canceled, is terminated, or times out has none.
- Add Server Channel publishes (from dexcli or FlowService) to the requests that Flow close cleanup rejects with the Flow closing error.

## 1.0.3 - 2026-11-09

- Correct the Dex Web run page description in `dex-sdk`: the event panel replaced the Details panel, and routing names the task queue.

## 1.0.2 - 2026-11-07

- Remove obsolete Server migration guidance.

## 1.0.1 - 2026-11-06

- Classify Connector as a Component alongside the other Dex Components.

## 1.0.0 - 2026-11-06

- Rewrite the Skills as the coding-agent version of the Dex stable layer. They cover the same scope as the docs Primitives, Components, and Design patterns: mental models, decision tables, rules and anti-patterns, design patterns with code skeletons, language idioms, and a self-check list. Each rule is complete without its links.
- Split the `dex-sdk` core references into one file per Primitive (`flow.md`, `step.md`, `attribute.md`, `channel.md`, `rpc.md`, `stream.md`, `timer.md`, `subflow.md`, `connector.md`, `client.md`) and per Component (`application.md`, `server.md`, `cli.md`, `web.md`, `external-attribute-sync.md`), plus `patterns.md`, `error-handling.md`, `data-handling.md`, `testing.md`, and `self-check.md`. `SKILL.md` now holds the mental model, the Primitive and mechanism tables, and the task routing table. The old files are merged into the new ones: `primitives.md` into `SKILL.md` and the Primitive files, `modeling.md` into `flow.md`, `step-options.md` into `step.md`, `read-after-write.md` into `client.md`, `pull-workers.md`, `getting-started.md`, and `versioning.md` into `application.md`, `operations.md` into `server.md`, `application.md`, `connector.md`, `cli.md`, and `error-handling.md`, `ai-agents.md` into `patterns.md`, `design-review.md` into `self-check.md`, and `troubleshooting.md` into `error-handling.md`. `bundle-baselines.md` is removed.
- Rewrite the Go, Python, Java, TypeScript, and Rust references as language idioms on top of the core rules: an entry file for setup, registration, and wiring, plus `primitives.md`, `error-handling.md`, and `testing.md`. Go, Python, and TypeScript keep a `patterns.md`; Java and Rust map the patterns in `primitives.md`. The former `data-handling`, `advanced-features`, `observability`, `versioning`, and `gotchas` files are merged into these files.
- Rewrite `dex-app-builder` as a stable workflow without version pins. Merge the business contract, application surface, and build-test-handoff stages into the remaining references, and drop volatile Dex Web and Connector details. Fix the `dex-sdk` links in `dex-connector-contributor`.
- Remove version numbers, release history, volatile parameters, and pinned source excerpts. Defaults, limits, configuration, flags, and version differences now live in SDK doc comments and the versioned docs Developer reference.
- Link only to the Developer reference and to runnable examples on the default branch. `dex-sdk` tells the agent to find the project's SDK version and open the matching version of the Developer reference. No link pins a release tag or commit.
- State these rules once and the same way in every language: WaitFor declares the wait and never calls an external system; Step execution local values are committed with the method result and survive Worker replacement; map size and key enumeration need a whole-map load; Steps a decision or RPC result schedules are never canceled by it; shutdown stops admission, drains the Worker, then closes the Client and cache; retry an UNAVAILABLE mutation only when it is idempotent or after reconciliation; keep Flow, run, and Step execution IDs in structured logs, never in metric labels. Add the inbound event loop pattern, the exact polling heartbeat bound, and the failure-path, running-Step signal, recording fake, and parallel Worker replacement testing rules.
- Replace the validators. `check-package.py` now checks packaging, front matter, the three-skill layout, local links and anchors in the standalone bundle, the product and concept boundaries, including lowercase backend names and mechanisms, and that skill content has no Dex version numbers, including "Since" and "Changed in" notes. External links go only to the Developer reference and to examples on main, plus a short list of exceptions, each with its reason: the Connector catalog file, the Connector library and its examples and READMEs, and the Skills repository and its latest-release lookup. It no longer checks exact files or sentences. The source-excerpt and upstream-baseline validators and all `DEX_*BASELINE` files are removed, so a Dex release never requires a skills change.
- Add `doc-map.yaml`, which maps each core Primitive, Component, and pattern file and the App Builder references to the docs pages that hold the same stable knowledge. Language references and skill-only files are not mapped. Check Gate CI fails a pull request that changes one side of the map without the other, unless its description states why with `doc-map: no change needed: <reason>`. Changes also pair through the base branch's mappings, every skills file and doc-map docs page must be mapped or listed as unmapped, and an empty map fails. Check Gate waits for Skills CI on docs-only changes to the Primitives and Design patterns pages. It runs the release metadata and doc-map checks itself against the description it fetches on every run, including after an edit, so a stale Skills CI result cannot approve a description.
- Release only when stable content changes. A typo, link, or wording-only change states `skills-release: no bump needed: <reason>` in its pull request instead of bumping the version. The 0.x release notes moved to `CHANGELOG-0.x.md`.
