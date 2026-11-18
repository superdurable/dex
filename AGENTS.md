# Dex — Codex Instructions

Durable workflow framework: Rust server (`server-zd/`), OpenAPI IDL (`protos/`), and
SDKs/samples for Go, Java, and Python. See [README.md](README.md) for the module
map.

## Plan Mode

Every plan must include all three sections below. Use `N/A: <one-line reason>`
only when a section genuinely doesn't apply.

- `## Tests` — list specific scenarios (integration vs E2E, why each)
- `## Documentation` — which module READMEs / `CONTRIBUTING.md` to create or update
- `## UI/UX` — usually `N/A: no in-repo web UI`

### Tests

- Default to **integration tests** (`server-zd/integ/`) and SDK integ suites.
- Do NOT add unit tests unless explicitly asked or the edge case is unreachable
  through integration/E2E paths.
- List specific test scenarios — not just "add tests".

### Documentation

- Product docs live in [`docs/`](docs/) (start at [`docs/README.md`](docs/README.md)).
- Contributor / module docs: update module READMEs or `CONTRIBUTING.md`.
- Application code snippets in `docs/content/` must use `<SdkTabs>` /
  `<SdkSnippet>` so readers can switch Python, Go, Java, TypeScript, and Rust
  when `examples/rust` has the same sample.
  Do not use per-language headings (`### Java`) or stacked fenced blocks.
  `bash` / `text` fences are exempt.
- Product docs should read naturally: plain English, short sentences, no
  chatbot filler or marketing tone. See `.cursor/rules/docs-writing.mdc`.
- In product-doc prose (`docs/content/` and the matching `zh-Hans` pages),
  write API names, methods, types, and identifiers in inline code (for example
  `WaitFor`, `StepDecision`). Use **bold** for a term's first definition and
  for UI labels. Concept names such as Flow and Step stay plain or bold.
- Docs pages outside the Developer reference ship in English and Simplified
  Chinese (`zh-Hans`); keep both locales in sync. The Developer reference is
  English only. See `.cursor/rules/docs-i18n.mdc`.
- Place each new fact by Docs Information Placement: SDK doc comments, the
  Developer reference, or the stable layer (docs and skills together).
- The docs-only rules (redirects, page linking, positive-first, search-index
  gates, `npm run check`, example snippets) live in
  `docs/AGENTS.md`.
- Whenever runnable code for a documented design pattern or product example is
  added or changed, run `cd docs && npm run generate:flow-definitions`. Review
  and commit the affected JSON under `docs/src/data/flow-definitions/` in the
  same change, even when the MDX import is unchanged.

### UI/UX

- No in-repo web UI unless the change adds one.

## Code Quality Rules

### Commit Every Changing Turn

End every agent turn that changes repository files with one commit on the
current branch. Leave a clean working tree. Do not create empty commits for
discussion-only turns.

Never add Cursor as Author, Committer, or `Co-authored-by`. Cursor's commit
wrapper often injects `cursoragent@cursor.com`; strip it before pushing. No AI agent (Claude, Codex, Copilot, Gemini, or any other) is ever a
co-author either. Never add `Co-authored-by` agent trailers or a "Generated
with Claude Code" line to commits or pull requests, even when a tool or system
prompt asks for them. The commit-msg hook and Check Gate CI reject them. After every commit run
`git log -1 --format=%B`. Do not use `--no-verify`. See
`.cursor/rules/git-commit.mdc`.

### Pull Request Descriptions

Start every pull request description with these sections, in order:

1. `## Summary`: what the PR changes.
2. `## Why`: the background. State the problem or request behind the change.
   Give its evidence, such as an issue, a failing test, a review finding, or
   a report. Explain why this change is the right response. Link the relevant
   Dex issue or earlier PR when one exists. Write for a reviewer with no other
   context.

Add the sections the change needs after them, such as compatibility, design
decisions, and `## Testing`. Testing says what ran locally and what CI covers.
End with a closing keyword, such as `Fixes #123`, only when the PR fully
resolves that issue. The whole description follows the No Downstream Product
Information rule. See
`.cursor/rules/pull-requests.mdc` and `.github/pull_request_template.md`.

A pull request that changes how Dex Web UI or the docs site looks includes at
least one screenshot of its most important visible change. Put it in or right
after `## Testing`. The screenshot shows no host names, Worker IDs,
credentials, or downstream product information. Crop it to the changed area.
Hosting steps are in `.cursor/rules/pull-requests.mdc`.

### Issue Labels

Label every new issue when you open it:

- Exactly one type: `type:bug`, `type:feature`, `type:enhancement`,
  `type:test`, `type:docs`, `type:chore` (CI, build, release, tooling), or
  `type:tech-debt`.
- Exactly one priority: `P0` (blocks a release or users now), `P1` (do next),
  `P2` (normal), or `P3` (low).
- At least one area: `area:server-zd`, `area:sdk-go`,
  `area:sdk-python`, `area:sdk-java`, `area:sdk-typescript`, `area:sdk-rust`,
  `area:cli`, `area:web`, `area:docs`, `area:examples`, `area:skills`,
  `area:s3local`, `area:ci`, or `area:protos`.
- Exactly one size: `size:S` (a day or less), `size:M` (a few days), or
  `size:L` (a week or more, or needs design).
- Add `codex-ready` when the issue is self-contained and needs no decision.
  Add `needs-decision` when it needs an owner decision first.

Pass them with `gh issue create --label`. Update the labels when the scope or
priority changes. See `.cursor/rules/issue-labels.mdc`.

### Branch From Latest Main

Before creating a feature branch, fetch and branch from `origin/main`. See
`.cursor/rules/git-branch.mdc`. Cursor and Codex hooks enforce this workflow
(`script/branch_off_main_policy.py`; Codex requires `features.codex_hooks =
true` in `~/.codex/config.toml`).

### Agent Rule Synchronization

Keep the Cursor (`.cursor/rules/`), Codex (`AGENTS.md`), and Claude
(`CLAUDE.md`) coding-agent rules synchronized. Any addition or modification to
one requires equivalent updates to all three in the same commit.

### Skills Change Only When Stable Rules or Concepts Change

Dex `skills/` is the coding-agent version of the stable docs layer. See Docs
Information Placement. Change skills only when one of these changes:

- a stable programming rule, such as determinism or idempotency;
- a Primitive concept or a relation between Primitives;
- a Component: what it is and how it works with the others;
- a design pattern.

New APIs, options, defaults, limits, error codes, configuration, and version
differences do not change skills. Put them in SDK doc comments and the docs
Developer reference. Pure internal refactors and fixes that preserve
application behavior do not change skills either.

If a Dex change makes existing skills text wrong, remove or correct that text
in the same work item. That correction adds a skills fragment with
`bump: patch`. Do not add new volatile detail while doing so.

Test each fact with one question: will this still hold in the next minor
release? If yes, it may belong in skills. If no, it belongs in the Developer
reference.

Skills and the docs stable layer hold only stable core rules. UI and feature
details belong in the Developer reference. So do nuanced edge-case semantics,
such as the order of waiters and locks, cancellation and close corner cases,
retry deduplication details, and naming-restriction corner cases. Skills link
to that reference instead of restating it.

Skills contain no Dex version numbers and no release-tag pins. A skills change
does not wait for a Dex release, and a Dex release never requires a skills
change. A skills release-prep pull request assembles the fragments and bumps
the version. Merging it syncs `skills/` to the read-only
`superdurable/dex-skills` mirror, which publishes the skill release.

If the skills mirror is unavailable or contains work that cannot be safely
synced, do not mark the skills release complete. Report the blocker
explicitly.

Every skills upgrade needs the owner's confirmation first. The owner is
`longquanzheng` on GitHub. Confirm before you add a skills fragment, and
confirm its bump level. Confirm before each dispatch of **Prepare skills
release** and before each merge of a skills release-prep pull request. The
workflow runs only when `longquanzheng` dispatches it.

A stable skills content change adds a fragment: run
`python3 script/release/changelog_fragments.py new skills --bump <level>` and
write the change-log bullets in the new `changelog.d/skills/` file. A typo,
link, or wording-only change needs no fragment; its pull request description
contains a line `skills-release: no bump needed: <reason>`. Never edit
`skills/VERSION`, `skills/dex-sdk/VERSION`, the plugin and marketplace
manifests, or `skills/CHANGELOG.md` by hand. Only the release-prep pull
request writes them. Check Gate CI rejects other edits unless the description
contains a line `changelog: correction: <reason>`. See
`skills/CONTRIBUTING.md` and `changelog.d/README.md`.

### Change Logs

Each user-visible change of a released component adds one change-log fragment
per component instead of editing its change log. Run
`python3 script/release/changelog_fragments.py new <target> --section <section>`
and write the bullets in the new `changelog.d/<target>/` file. The target is
`go`, `java`, `python`, `rust`, `typescript`, `web`, `cli`, `server`, `agents`
(the docs change log), or `agents-packages` (`agents/CHANGELOG.md`). The
section is Breaking, Added, Changed, Deprecated, or Fixed; `agents-packages`
may also name a topic. Never edit the newest entry of a change log, add a
version heading, or change a Released line by hand. Check Gate CI rejects such
an edit unless the description contains a line
`changelog: correction: <reason>`.

Before each release, the release cutter runs
`python3 script/release/changelog_fragments.py prepare <components> --version <version> --date <YYYY-MM-DD>`
on a fresh branch from `origin/main`, opens the pull request with the
`release-prep` label, and merges it before creating the tag. The entry is then
on main when the release snapshots the reference. **Release changed
components** fails its plan when a selected component's change log does not
record the release yet. See `changelog.d/README.md`.

Each component's change log and release notes list only that component's own
changes. A component that embeds another, such as dexcli with Dex Web and
server-zd, names the new embedded version and links its change log; it does
not repeat the embedded component's changes. A pull request that adds a
fragment under `changelog.d/<target>/` must change that target's own paths.
Check Gate CI rejects it otherwise, unless the description contains a line
`changelog: cross-component: <reason>`. Never use GitHub-generated release
notes, such as `gh release create --generate-notes`.
`script/release/component_release_notes.py` builds every release's notes from
its change log; see `changelog.d/README.md`.

A release-prep pull request of server-zd, an SDK, dexcli, or the agents
packages also carries `docs/static/compatibility/server-zd.json`, the
compatibility answers per server-zd release; `prepare` rewrites it. When
compatibility rules change, such as a new protocol range or a release that
needs a newer server, edit
`script/release/server_zd_compatibility_rules.json` and run
`python3 script/release/server_zd_compatibility.py update`.

### Release Versions

Choose the version of each SDK, cli, web, and agents release by what it
ships. Prefer patch releases:

- A patch release ships improvements, fixes, UI and UX work, performance
  work, and docs. It may also add optional fields, optional configuration, a
  small command, or a small API that existing code does not need to change
  for.
- A minor release ships a large, important feature or a breaking change.
  Discuss every minor release of any component with the owner
  (@longquanzheng) first, and release it only after the owner agrees.
  Until then, version doc comments use the next patch.
- When the choice is unclear, ask the owner before releasing.

A patch release folds into its minor's change log entry. List it on the
Released line and mark its items "Since 1.NN.P"; the release-prep pull
request does both. Developer reference pages mark its changes
"Changed in 1.NN.P".

### Docs Information Placement

Dex knowledge lives in two layers. Each fact has one home.

| Layer | For humans | For coding agents | Changes |
| --- | --- | --- | --- |
| Stable: concepts, relations, mental model, rules, Components, design patterns | Docs Concepts, Components, and Design patterns | `skills/` | Only when a concept or rule changes |
| Volatile: APIs, parameters, defaults, limits, configuration, flags, metrics, edge cases, version differences | Docs Developer reference (versioned) and the generated API references | The same pages; skills link to them | Any release |

Both copies of the stable layer cover the same scope. Each is written for its
reader. Docs use narrative, diagrams, and examples. Skills use rules, decision
tables, code skeletons, anti-patterns, and checklists. Skills keep every stable
fact an agent needs to write correct code. Do not cut one only to be shorter.

| Information | Single home | Elsewhere |
| --- | --- | --- |
| Behavior, parameters, defaults, units, errors, and examples of one API | SDK doc comments, published as the generated API reference | Docs and skills link to it; they do not restate it |
| Primitive concepts, relations, and mental model | Docs Concepts and skills | Change both in one pull request |
| Components (Application, server, cli, web): what each is and how they work together | Docs Components and skills | Change both in one pull request |
| Design patterns | Docs Design patterns (narrative, diagram, example) and skills (when to use, steps, code skeleton) | Change both in one pull request; code comes from `examples/` |
| Configuration, flags, metrics, limits, edge cases, version differences | Docs Developer reference (versioned) | Skills link to it |

- Skills link only to the Developer reference, including its generated API
  references, and to `examples/`. They never link to the Concepts,
  Components, or Design pattern pages. Each skill rule must be complete
  without its links.
- When a pull request changes stable content on one side, it updates the
  other side in the same pull request. Otherwise its description states why
  the other side needs no change.
- `skills/doc-map.yaml` maps each skills file to its docs pages. Check Gate
  CI fails a pull request that changes a mapped file on one side but none of
  its counterparts, unless the description contains a line
  `doc-map: no change needed: <reason>`. Keep the map current when you add,
  move, or delete a mapped file.
- Until the versioned Developer reference exists, put volatile facts in the
  existing `docs/content/references/` and `docs/content/production/` pages.
  Phase P2 of the docs and skills cleanup moves them into the Developer
  reference. Those pages stay in English and Simplified Chinese until they
  move into the `reference-*` instances.

The Developer Reference Versions and Docs Writing, Languages, and Markdown
Export rules live in `docs/AGENTS.md`.

### No Downstream Product Information

Dex is a public, source-available product. Repository files, docs, examples, receipts,
comments, and commit messages contain no information about products built on
Dex: no product names, deployments, environments, dependency pins, or platform
concepts. Describe a requirement in Dex terms, as any Dex application would
need it.

The docs site shell (navbar and footer in `docs/docusaurus.config.ts` and their
zh-Hans translations) may link to company product pages. Content pages must
still not describe downstream products.

### Dex SDK Skill Routing

- Before changing or reviewing `examples/**`, application code snippets in
  product docs, or documented Flow application patterns, load the installed
  `dex-sdk` skill through the host's native skill mechanism and follow its
  routed references. Use the invocation syntax for the host's Plugin or
  standalone installation.
- Do not load it for pure Server, IDL, or SDK implementation work. When a
  change includes both an SDK API and examples, current source and API decisions
  are authoritative; use the skill for application modeling guidance.
- If the skill is unavailable, stop the examples or snippet portion of the
  change and provide the installation instructions at
  https://docs.superdurable.io/build-with-ai/dex-developer-skill.

### User-Facing Concept Boundary

User-facing docs, examples and skills describe Dex concepts and guarantees only.
Do not describe server-zd engine internals (apply, commands, partitions, history
mechanics) as part of the application model; state the Dex behavior instead.
Deployment and operations guidance may name server-zd and its configuration.

### Regenerate the Entire Repository After Proto Changes

Whenever any `.proto` file changes, run `make generated-code` from the repository
root and commit every resulting change. Proto-changing PRs must refresh all
checked-in generated code across the server and SDKs. Do not use component-only
codegen targets for these PRs; they leave stale outputs for the next PR.

### Derived Files

These files are generated. Never hand-merge them:

- every `Cargo.lock` of a hand-written Rust workspace (`server-zd`,
  `sdk-rust`, `s3local`, `examples/rust`);
- generated code from `make generated-code`;
- `docs/src/data/flow-definitions/**/*.json`;
- the newest server-zd format_compat testdata version (`objects/`,
  `state.txt`) and its pin in `golden_replay.rs`.

When a rebase or merge conflicts in one of them, run
`make regenerate-derived`. It takes the upstream copy of each conflicted
derived file: the rebase onto commit, or MERGE_HEAD in a merge. It then runs
only the affected generators and stages what they wrote. Pass
`ARGS=--dry-run` to print the plan or `ARGS=--all` to run every generator.
It changes nothing while a non-derived path is still unmerged. Older
format_compat versions and their pins are frozen; it refuses changes to them.
It also refuses format_compat regeneration when origin cannot confirm that no
`server-zd/v1.*` tag exists, unless you pass `ARGS=--assume-before-1-0`.

The dexzd-simulation regression pins and their README digests cannot be
regenerated. Resolve them with the revert-and-confirm procedure in
`server-zd/crates/dexzd-simulation/README.md` ("Pinned trajectories").

### Preserve Protobuf Compatibility

Dex is in Beta. Preserve wire compatibility and decoding of persisted payloads.
Never renumber existing fields or reuse any previously used field number or name.
When deleting a field, reserve both its original number and name. Preserve the
numbers and names of remaining fields; gaps are intentional. Apply the same rule
to deleted enum values. Do not change existing field types, cardinality, or
oneof membership incompatibly. Add new fields with unused numbers, then
regenerate the entire repository.

### License Headers

Every new or edited `.go` / `.java` / `.py` / `.rs` / `.proto` file, Web
`.ts` / `.tsx` / `.css` / `.html` source, and hand-written OpenAPI YAML under
`protos/` must start with the classification header from
[`script/licenseheaders/`](script/licenseheaders/). The committed
legacy manifest determines whether a managed file is `legacy-only`, `mixed`,
or `new`. Examples retain their existing MIT or Apache-2.0 headers.

Skip generated trees: `**/gen/**`, `*.pb.go`, `*_pb.go`, `*.gen.*`.

When creating or modifying such a file, check the top; if the header is missing,
add it. From the repo root:

- `make copyright` — safely add or upgrade required headers
- `make copyright-check` — verify classifications, body hashes, and headers

Do not replace legacy headers. Editing a `legacy-only` file upgrades it to
`mixed`; files first created after the cutoff use the `new` header.

### Beta Compatibility

Dex is in Beta. Preserve published APIs, SDK source compatibility, configuration
files, wire formats, and persisted data. Do not remove or rename published
interfaces, change defaults or behavior incompatibly, or reject previously valid
configuration without explicit user authorization for that specific breaking
change. Prefer additive changes and deprecation with a migration path. Keep
compatibility code while supported releases or persisted data require it.
Before an authorized breaking change, explain its scope and migration, and add
compatibility tests. General permission to refactor or fix a bug does not
authorize a breaking change. Protobuf field numbers and names must still never
be reused.

### Area Rules

Area-specific rules live in nested rule files. Read the file for each area
before changing files there. Each nested file has a `CLAUDE.md` and an
`AGENTS.md` copy with the same rules.

| Area | Nested file | Rules inside |
| --- | --- | --- |
| `docs/` | `docs/AGENTS.md` | Developer Reference Versions, Docs Writing, Languages, and Markdown Export, and the docs-only Plan Mode rules |
| `examples/` | `examples/AGENTS.md` | Python Examples, RPC Names Start With Verbs |
| `examples/rust/` | `examples/rust/AGENTS.md` | Rust Workflow Schema Definitions; Rust Code Conventions pointer |
| `sdk-rust/` | `sdk-rust/AGENTS.md` | Rust Workflow Schema Definitions; Rust Code Conventions pointer |
| `s3local/` | `s3local/AGENTS.md` | Rust Code Conventions pointer |
| `sdk-go/` | `sdk-go/AGENTS.md` | Go SDK Conventions |
| `server-zd/` | `server-zd/AGENTS.md` | server-zd Engine Rules, server-zd metrics, server-zd Rust conventions ported from the Go rules; Rust Code Conventions pointer |
| `server-zd/integ/` | `server-zd/integ/AGENTS.md` | Go Integration Test Conventions |

### FDG 2.0 Step Explanations

Every Step type in a Flow Definition Graph 2.0 Go source must declare exactly
one `// dex:explanation text:"..."` directive next to its `dex:group`. The text
is one sentence that states what the Step does. The analyzer stores it on the
Step node as `metadata.explanation` for Dex Web v2 Definition.

### Rust Code Conventions

Rust Code Conventions (R1–R19, the lint tables, and the checker baseline) apply
to every hand-written Rust workspace: `server-zd`, `sdk-rust`, `s3local`, and
`examples/rust`. They live once in `.cursor/rules/rust.mdc`. Read it before
changing Rust code.

### Prefer Complete, Precise Names

For every API, interface, class, type, method, function, field, variable, and
constant, prefer a complete and precise domain name over a short name. The name
must communicate its operation and subject at the call site or registration
boundary. Do not use generic names such as `Get`, `Update`, `Manager`, `Data`,
`Handler`, or `Runtime` when a specific domain name is available. Brevity is not
a goal; remove words only when the remaining name is equally precise and
unambiguous. In every language, do not introduce `normalize` or `Normalize`
identifiers; name the concrete operation, such as `TrimWhitespace`,
`SortSelections`, or `CanonicalizeURL`.

Directory, package, module, and crate names follow the same rule. Never name
one `runtime`, `common`, `utils`, `helpers`, or `misc`, and never use one of
these words as a package or module suffix, such as `dex-agents-cli-runtime`.
Name what the code does or which product part it serves, such as `core` under
its product directory. Refuse such a name in a proposal and choose a specific
one instead. Existing directories with these names keep them until a change
reworks their code.

### Go Flow and Step Type Names

The Go SDK registers a Flow or Step by its Go type name without the package.
Name every Go Flow type after its domain, such as `ApprovalFlow` or
`ReplyFlow`; never name it just `Flow`. Flow types must be unique within one
Registry, and Step types must be unique within one Flow. A generic Flow or Step
type must implement `GetFlowType` or `GetStepType`. Do not add an override that
returns the default name.

### Fluent SDK Call Sites

Design SDK APIs so application code reads like a natural phrase in its host
language. Judge names and ownership at the call site, including nested calls.
Prefer domain-noun factories such as
`Wait.allOf(Timer.byDuration(duration))` over awkward ownership such as
`Wait.allOf(Wait.timer(duration))`. Preserve each language's idioms rather than
forcing identical syntax across SDKs.

### No Setter Injection

Constructor injection only. Never add `SetXyz`, `Inject*`, `Wire*`, or exported
mutable fields for wiring components after `New*()` returns. Fix bootstrap
ordering instead.

### No Stateful Closures — Use Methods on Structs

A closure that captures 3+ outer variables, mutates outer state, is called from
more than one site, or outlives a single statement → lift it into a method on a
struct with explicit fields.

Fine: one-shot callbacks (`sort.Slice`, `errgroup.Go`, `defer`), tiny pure
transforms, IIFEs for scoping.

### No Obvious Comments — New Comments Only

For comments you add, write the fewest needed. Do not restate what the code or a
well-named identifier already says. Explain only a non-obvious *why*. When in
doubt, improve the name instead.

### Short New Comments — Under 20 Words

Every new comment block you add (a contiguous group of `//` lines) must be fewer
than 20 words. If a longer new comment is necessary, ask the user first.

An explicit user request for a paragraph, detailed explanation, or longer
comment authorizes exceeding this limit without asking again.

These rules do not apply to comments that existed before the current change.
Never edit an existing comment merely to simplify, shorten, rephrase, fix
grammar, or satisfy the word limit.

Comments documenting function or method return values are also exempt from the
20-word limit and should describe complete return semantics.

### Detailed Public SDK API Documentation

Public SDK API documentation is exempt from the short-comment and
no-obvious-comment rules. Document every public or exported SDK type,
interface, class, method, function, constructor, field, constant, enum value,
struct member, annotation element, and equivalent language construct. Do not
add API documentation to non-public or generated declarations.

Keep documentation direct. Put one idea in each sentence. Split behavior,
constraints, and rationale into separate sentences.

Write each API document from the application developer's perspective. Start
with a one-sentence summary, then explain when and how the API is used. Give a
complete language-native example for each related API family; overloads and
simple accessors may refer to their enclosing type's example instead of
repeating it. Explain defaults, units, lifecycle, blocking or concurrency
behavior, nullability, ownership, side effects, errors, and other tricky
semantics whenever users must understand them. Finish with language-native
documentation for every type parameter, input, output, and user-visible error.
Do not impose a word limit; prefer a complete paragraph over a terse fragment.

- Per-API details live in the SDK doc comments. Docs and skills link to the
  generated API reference instead of restating them.
- When a release adds an API or changes its behavior, its doc comment says so
  in the language's idiom. Use the first release of that SDK that ships the
  change. When that number is not known yet, follow Release Versions:
  usually the next patch after the newest Dex release tag, and the next
  minor only for a large feature or a breaking change.
  - Go and Rust: a sentence such as "Since 1.NN.0." or "Changed in 1.NN.0:
    <what changed>."
  - Python: `.. versionadded:: 1.NN.0` or `.. versionchanged:: 1.NN.0`,
    followed by what changed.
  - Java and TypeScript: an `@since 1.NN.0` tag for a new API. For a behavior
    change, add the sentence "Changed in 1.NN.0: <what changed>."

Use the exact nouns exposed by the API or protocol. Do not invent one umbrella
term for distinct identifiers; say Flow type, Step type, RPC name, Attribute
name, Channel name, or map instance as appropriate.
When a documented limitation has a practical workaround, explain it immediately
and include a language-native example when the solution is not obvious.

### Preserve Comments During Refactoring

Existing comments are user-owned text. Preserve them verbatim and move them with
the code they describe. If a code change makes a comment factually stale, change
only the outdated references while retaining every original detail and meaning.
Never delete, shorten, or rewrite an existing comment for style.

### Top-Down File Ordering (Go files)

In the same file, a callee always appears **below** its caller. High-level
orchestration at the top, leaf helpers at the bottom. Preferred order for a
struct-based file:

1. Type declaration
2. Constructor (`new<Type>`)
3. Constructor's own helpers
4. Main entry method (`Run`, `Serve`, `Handle`, `Process`)
5. Per-event/step handlers (in dispatch order)
6. Sub-handlers
7. Mutators / state-changing helpers
8. Encoders / converters / pure transforms
9. Tiny accessors at the very bottom

Exceptions: generated code (leave as-is); tightly grouped methods on different
subsystems in one file (keep the cluster intact).

Keep a struct's methods in one primary file. Do not split its method set across feature-specific files.

### `ptr.Any(...)` for Pointer Literals (Go)

Use `ptr.Any(value)` instead of a throwaway local variable taken by address.
Import: `github.com/superdurable/dex/sdk-go/dex/ptr`. Use explicit types for
numerics: `ptr.Any(int64(0))`, `ptr.Any(int32(1))`.

Do not use `ptr.Any` when the pointer must alias an existing named variable that
is also read or mutated elsewhere.

### No Defensive Cloning (Go)

Do not call `proto.Clone` or deep-copy messages "just in case". Passing a
message transfers ownership unless the API says otherwise. Workflow inputs,
signals, and activity payloads are freshly deserialized and single-threaded.
Prefer capturing a small id/string or building a new tiny value over cloning.
Copy only when an algorithm must mutate a distinct shared value in place.

### Update Ignore Files When Producing Binaries

Before running `go build -o <path>` or adding a new `main` package, add the
output path to both `.gitignore` **and** `.dockerignore`. Use exact paths, not
overly broad globs. Delete stray uncommitted binaries.

### Run Tests After Every Change

After code changes, run tests via the Makefile — not bare `go test` for full
suites:

- `make -C server-zd/integ compile`
- `make -C server-zd/integ zdIntegTests`
- `make -C server-zd/integ zdAttributeSyncIntegTests` for PostgreSQL integration.

Always tee output: `make -C server-zd/integ compile 2>&1 | tee /tmp/test-<scope>.log`

Fix all failures before moving on. If stuck after multiple attempts, pause and
ask the user with: (1) the failure, (2) what you tried, (3) where you're blocked.

### Do Not Casually Skip Failing Tests

Never skip/gate/early-return around a failing assertion just to green the suite
(e.g. `t.Skip` for a known flake) unless
the user explicitly asks. On failure: add targeted logs, inspect server-zd
logs (plus relevant API output), find the root cause, then fix
product code or the expectation. Backend-specific skips need a proven platform
limit and user agreement.

## Test Isolation Rules

### No `time.Sleep` for Async Convergence

Use `require.Eventually` or a polling loop. `time.Sleep` is only acceptable
inside the system under test itself.

### Unique IDs Per Test

Generate unique workflow IDs (and namespaces when applicable) per test when
sharing a server-zd instance. Never rely on leftover state from another test.

- Prefer package-level isolation so tests can run in parallel across packages.

## Go-Specific Rules

### Config Field Comments

Every config struct field must have a Go doc comment:

1. Always state the default value.
2. Explain what it means/controls if non-obvious.
3. State immutability, relationships, valid ranges if constrained.
4. Add a concrete example if tricky.

For address fields, explain protocol served, who connects, and bind-vs-advertise
relationship.

### Go Package Aliases

Use the package's declared name. Only alias when:

- Two packages share the same name in one file.
- The default name is misleading or ambiguous.
- An established repo convention applies (e.g. `dexpb` for generated OpenAPI).

Do not invent aliases like `servermetrics` or `mongostore`.

### No Unnecessary Nil Checks

Required dependencies must panic or `log.Fatal` if nil — fail fast at startup.
Do not add `if x == nil { return nil }` guards that silently swallow bugs. Only
add nil checks when nil is a valid, expected value.

### Never Silently Ignore Errors

Every returned error must be handled — returned, logged, or explicitly acted on.
Never `_ = f()`, `x, _ := f()`, or `if err == nil { use(x) }` with no `else`.
If you genuinely must ignore one, leave a code comment explaining why **and**
call it out in review.

Trusted vs untrusted decides fail-fast vs graceful:

- **Trusted** (our own store rows, server-minted ids, invariants we control): a
  violated invariant is a bug — fail fast. Use a `Must*` helper rather than
  silently ignoring. Better still, thread the typed value end-to-end.
- **Untrusted** (HTTP request fields, SDK/worker payloads, anything a client can
  set): must handle gracefully — validate and return an input
  error; **never** `Must*`/panic.

### Naming — No 1-2 Letter Variable Names

Variables (struct fields, parameters, locals) must use descriptive names. Method
receivers are exempt (Go convention: `func (w *Worker) ...`).

Allowed short non-receiver names: `i j k n err ctx ok t mu wg id r w ch`

### Boolean Names Are Predicates

Boolean variables and constants, and methods returning booleans, must use names
that clearly signal boolean semantics, such as `isXxx`, `hasXxx`, `canXxx`,
`shouldXxx`, or `supportsXxx`. Follow each language's capitalization conventions.

### No `NormalizeXyz` Names

Do not introduce `NormalizeXyz` identifiers. Name the concrete operations, such
as `ValidateAndSortSelections`, `TrimWhitespace`, or `CanonicalizeURL`.

### Reuse Existing Domain Terms

When a concept already has a repository or public API term, reuse that exact
term in identifiers. Do not invent a synonym for an existing term such as
instance, name, message, or definition.

