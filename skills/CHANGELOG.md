# Changelog

Release notes for Dex Skills 1.0.0 and later. The notes for 0.1.0 through
0.69.2 are archived in [CHANGELOG-0.x.md](CHANGELOG-0.x.md).

## 1.2.1 - 2026-11-12

- Move release-specific UI details and edge-case semantics out of the Skills into the Developer reference, and keep one short rule with a link in each place. The Developer reference now holds Step cancellation, completion requests, and Flow outputs (new Step and Flow close page), Attribute lock order, Channel waiter order, Join edge cases, and SubFlow reuse by later parent runs and parent close.
- Shorten the Flow ID reuse policy, StartFlow retry dedupe, Flow closing request, canceled Step message, Channel delete, and SubFlow reuse policy rules in `dex-sdk`, and point to the existing reference pages.
- Remove Dex Web page layout, Experimental labels, Start Flow placement, routing details, local Connector connection details, and Flow Definition Graph replacement details from `dex-sdk` and `dex-app-builder`. The Dex Web and Server references hold them.

## 1.2.0 - 2026-11-11

- Dex Web reads Flow Definition Graphs from the Dex Server. A Worker publishes the graphs it is given when it starts, and `dexcli` publishes them from a script. The Server keeps one graph per Flow type, and a graph with a later generation time, recorded in the graph, replaces an older one, whatever the upload order. A definition directory for Dex Web is optional and wins for its Flow types. `dex-sdk` `web.md`, `application.md`, `server.md`, and `cli.md` describe the relation.
- Correct `dex-app-builder` packaging in `verification-handoff.md`: ship validated definitions with the Worker instead of requiring a Dex Web directory. Dex Web marks an invalid definition on its own and keeps the others working.

## 1.1.1 - 2026-11-11

- Add the SubFlow naming rule to `dex-sdk` `subflow.md`: a Step whose Step type contains `-` cannot start SubFlows, because the child Flow ID joins its parts with `-`.

## 1.1.0 - 2026-11-10

- Join parallel branches with the Join in `dex-sdk`. The Await parallel Steps skeleton in `patterns.md` and the Go, Python, and TypeScript pattern guidance now fork a Join, end each branch with endBranch, and wait forAllBranches, instead of counting Channel signals.
- Add the Partial join and reset pattern: the join waits forBranches or forFirstBranch, the next round's split Step resets the previous round with forRemainingBranches, and a cancelling join cancels its sibling branches.
- Add the Join rules: each branch reports exactly once per round on every exit path; a multi-Step branch passes the round token to its last Step; fork one Join at most once per Step method, while explicit-round-key forks may repeat with distinct keys and are what an RPC uses; reset every looping round whose other branches keep running, but never a cancelling join's round, whose canceled branches never report; branches of an RPC-forked round are siblings of every Step that RPC name started, so cancel them by Step type; a first-branch or n-branch wait on an empty round fails; a canceled branch never reports, so cancel the join with the branches or add a Timer; never publish to the Join's ChannelMap.
- `dex-app-builder` maps the n8n Merge node and fan-out to a Join instead of a Channel count.
- `channel.md`, `step.md`, the Java and Rust primitives tables, and `dex-app-builder` product discovery point joins to the Join, which is built on a ChannelMap.

## 1.0.13 - 2026-11-10

- Correct the ID reuse table in `dex-sdk` `flow.md`: allow-if-previous-failed starts a new run after a failed, canceled, or terminated run, not only a failed one.

## 1.0.12 - 2026-11-10

- Correct the start identity rule in `dex-sdk` `error-handling.md`: a StartFlow retry whose Request ID matches the latest run's start returns that run under every ID reuse policy and never starts a second run or terminates the first. A rejecting policy still keeps the original run the latest one.

## 1.0.11 - 2026-11-10

- Remove the internal handler timeout rule from the `dex-sdk` Client wait guidance. Waits no longer have that setting.

## 1.0.10 - 2026-11-10

- Classify Attribute Store sync as an Attribute feature, not a Component. Its rules move from `external-attribute-sync.md` into the Attribute Store sync section of `dex-sdk` `attribute.md`; the rules themselves are unchanged.
- Classify Connector as a concept, not a Component, in `dex-sdk` `SKILL.md`. The Components are the application, server, dexcli, and Dex Web.
- Add Connector operation effect classes to `connector.md`: a Query is a read, a Mutation marked safe to repeat is an idempotent write, and every other Mutation is a non-idempotent write.
- Say Flow run instead of Flow execution.

## 1.0.9 - 2026-11-10

- Correct the local Connector setup rule in `dex-sdk` and `dex-app-builder`: Dex Web in `dexcli dev` no longer configures only the connections that loaded Flow definitions use. It can also add a named connection that no Flow uses, such as one an agent tool opens by name.

## 1.0.8 - 2026-11-10

- Add a control structure quick reference to `dex-sdk` `patterns.md`. It maps sequence, choice, parallel split, fork and join, several waiters, first result, first external event, mutual exclusion, critical section, task and case cancellation, loops, schedules, and multiple instances to their Dex recipes. Fork and join requires every branch to publish its signal on every exit path, including failure recovery, and a canceled branch never publishes, so the join must not count branches that may be canceled.
- Add three design patterns: Deferred choice (route on the first of several events with AnyOf over Channels and a Timer), Critical section (a one-message Channel token taken by a pure handoff Step and returned on every exit of a multi-Step section; never cancel a Step that does protected work), and Cancel with child Flows (list the cancel condition first in every wait, stop each child from the cancel-Channel path, and give each child its own Flow timeout as a backstop).

## 1.0.7 - 2026-11-10

- Add the commit isolation rule to `dex-sdk`: a Step method or RPC commits atomically but reads a snapshot, so a read-modify-write without a shared Attribute lock can lose an update, also in a transactional RPC.
- Add Attribute lock order: Steps get a lock in the order they started waiting, while an RPC takes a free lock ahead of queued Steps.
- Add Channel waiter order: waiting Steps get new messages in the order they started waiting, and a wait that needs more messages than are pending is skipped, so it can be starved.
- Add that a GracefulComplete request stays in force until no Step is active, that cancellation keeps what earlier methods committed, and that a canceled handler's external calls take effect even after the Flow closed.
- Add SubFlow lifecycle rules: a later run of the same parent Flow ID reuses the earlier run's running or completed child under the default reuse policy, and a child keeps running when its parent Step is canceled or its parent Flow closes.
- Correct the Channel message deletion rule: a missing message fails only a transactional RPC or a direct delete from dexcli or Dex Web; from a Step or a non-transactional RPC it is a no-op.

## 1.0.6 - 2026-11-10

- Correct the Channel and Step cancellation rules in `dex-sdk`: a Step canceled after its wait consumed messages, before its Execute committed, returns them to the front of their Channels in their original order. Dex drops them only when the Flow close itself cancels the Step, such as a StopFlow request or a Flow timeout; after any other cancellation they stay pending, even in a run that GracefulComplete then closes. A Step that a commit cancels never takes that commit's own publications, unless the commit also closes the Flow at once.

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
