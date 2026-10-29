# Dex Project Rules

Dex is a durable workflow framework with a Rust server (`server-zd/`), OpenAPI IDL
(`protos/`), and SDKs/samples for Go, Java, and Python. See `README.md` for the
module map.

## Commits

End every agent turn that changes repository files with one commit on the
current branch. Leave a clean working tree. Do not create empty commits for
discussion-only turns.

Never add Cursor as Author, Committer, or `Co-authored-by`. Cursor's commit
wrapper often injects `Co-authored-by: Cursor <cursoragent@cursor.com>`; strip
it before pushing. After every commit run `git log -1 --format=%B`. Do not use
`--no-verify`. See `.cursor/rules/git-commit.mdc`.

## Pull Request Descriptions

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

## Issue Labels

Label every new issue when you open it:

- Exactly one type: `type:bug`, `type:feature`, `type:enhancement`,
  `type:test`, `type:docs`, `type:chore` (CI, build, release, tooling), or
  `type:tech-debt`.
- Exactly one priority: `P0` (blocks a release or users now), `P1` (do next),
  `P2` (normal), or `P3` (low).
- At least one area: `area:server-zd`, `area:server`, `area:sdk-go`,
  `area:sdk-python`, `area:sdk-java`, `area:sdk-typescript`, `area:sdk-rust`,
  `area:cli`, `area:web`, `area:docs`, `area:examples`, `area:skills`,
  `area:s3mock`, `area:ci`, or `area:protos`.
- Exactly one size: `size:S` (a day or less), `size:M` (a few days), or
  `size:L` (a week or more, or needs design).
- Add `codex-ready` when the issue is self-contained and needs no decision.
  Add `needs-decision` when it needs an owner decision first.

Pass them with `gh issue create --label`. Update the labels when the scope or
priority changes. See `.cursor/rules/issue-labels.mdc`.

## Agent Rule Synchronization

Keep the Cursor (`.cursor/rules/`), Codex (`AGENTS.md`), and Claude
(`CLAUDE.md`) coding-agent rules synchronized. Any addition or modification to
one requires equivalent updates to all three in the same commit.

## Application-Facing Changes Require Skill Updates

When adding, removing, renaming, or materially changing an application-visible
API, primitive, feature, design pattern, or operational semantic, update
Dex `skills/` in the same work item. Merging `skills/` to main automatically
syncs it to the read-only `superdurable/dex-skills` mirror, which publishes the
skill release. Pure internal
refactors and fixes that preserve application behavior do not trigger this rule.

Commit Dex first. After that change is included in a published Dex release,
advance `skills/DEX_BASELINE` to that exact release tag,
refresh pinned source links, update affected core and all five language
references, and update the version, changelog, and plugin manifests. Never use
a branch name or commit SHA for `DEX_BASELINE` or pinned source links. Run every
skill validator before completion.

If no release tag contains the Dex change yet, report the release as the
blocker. Do not update `skills/` from an unreleased commit.

If the skills mirror is unavailable or contains work that cannot be safely
synced, do not mark the feature complete. Report the blocker explicitly.

## No Downstream Product Information

Dex is an open-source product. Repository files, docs, examples, receipts,
comments, and commit messages contain no information about products built on
Dex: no product names, deployments, environments, dependency pins, or platform
concepts. Describe a requirement in Dex terms, as any Dex application would
need it.

The docs site shell (navbar and footer in `docs/docusaurus.config.ts` and their
zh-Hans translations) may link to company product pages. Content pages must
still not describe downstream products.

## Dex SDK Skill Routing

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

## User-Facing Concept Boundary

User-facing docs, examples and skills describe Dex concepts and guarantees only.
Do not mention Continue-As-New except in the Flow options reference for the
Continue-As-New threshold and the TriggerContinueAsNew Client API reference. Do
not describe Temporal or Cadence internals (activity timeout names, Signals,
Queries, Updates, workflow history mechanics) as part of the application model;
state the Dex behavior instead. Deployment and operations guidance may name the
backend and its configuration.

## Beta Compatibility

- Dex is in Beta. Preserve published APIs, SDK source compatibility,
  configuration files, wire formats, and persisted data.
- Do not remove or rename published interfaces, change defaults or behavior
  incompatibly, or reject previously valid configuration without explicit user
  authorization for that specific breaking change. General permission to
  refactor or fix a bug does not authorize a breaking change.
- Prefer additive changes and deprecation with a migration path. Keep
  compatibility code while supported releases or persisted data require it.
- Before an authorized breaking change, explain its scope and migration, and
  add compatibility tests.
- Preserve protobuf wire compatibility and decoding of persisted payloads.
  Never renumber existing fields or reuse previously used field numbers or names.
  Reserve both the original number and name when deleting a field; preserve
  remaining fields, including intentional gaps. Apply the same rule to deleted
  enum values. Do not change field types, cardinality, or oneof membership
  incompatibly. Field numbers and names must never be reused, even when a
  breaking change is authorized.
- After any proto change, run `make generated-code` from the repository root
  and commit every resulting change across the server and SDKs.

## Dependency Injection

- Use constructor injection. Never add `SetXyz`, `Inject*`, `Wire*`, or exported
  mutable fields to wire dependencies after construction.
- Fix bootstrap ordering instead of post-construction wiring.
- Inject a pointer to the component's config section, not individual tunables or
  the whole `config.Config`.
- Store it as `cfg *config.XyzConfig`, panic on nil in the constructor, and read
  fields where used.

## Maintainability

- Lift stateful closures into struct methods when they capture 3+ values, mutate
  outer state, have multiple call sites, or outlive one statement.
- One-shot callbacks, tiny pure transforms, and IIFEs are acceptable.
- New comments explain only non-obvious reasons, trade-offs, invariants, or
  external constraints. Prefer clearer names over obvious new comments.
- Keep each new contiguous comment block under 20 words. Ask before exceeding
  this for a new comment.
- An explicit user request for a paragraph, detailed explanation, or longer
  comment authorizes exceeding this limit without asking again.
- These comment rules do not apply to existing comments. Never simplify,
  shorten, rephrase, grammar-fix, or delete existing comments for style.
- Preserve existing comments verbatim during refactors and move them with their
  code. If one becomes stale, update only outdated references while retaining
  every original detail and meaning.
- Comments documenting function or method return values are exempt from the
  20-word limit and should describe complete return semantics.
- Public SDK API documentation is exempt from the short-comment and
  no-obvious-comment rules. Document every public or exported SDK type,
  interface, class, method, function, constructor, field, constant, enum value,
  struct member, annotation element, and equivalent language construct. Do not
  document non-public or generated declarations.
- Keep documentation direct. Put one idea in each sentence. Split behavior,
  constraints, and rationale into separate sentences.
- Write SDK API documentation from the application developer's perspective.
  Start with a one-sentence summary, then explain when and how to use the API.
  Give a complete language-native example for each related API family;
  overloads and simple accessors may refer to that shared example. Explain
  defaults, units, lifecycle, blocking or concurrency behavior, nullability,
  ownership, side effects, errors, and other user-visible traps when relevant.
  Finish with language-native documentation for every type parameter, input,
  output, and user-visible error. Do not impose a word limit; prefer a complete
  paragraph over a terse fragment.
- Use the exact nouns exposed by the API or protocol. Do not invent one umbrella
  term for distinct identifiers; say Flow type, Step type, RPC name, Attribute
  name, Channel name, or map instance as appropriate.
- When a documented limitation has a practical workaround, explain it
  immediately and include a language-native example when the solution is not
  obvious.
- Product docs in `docs/content/` must present application code in
  `<SdkTabs>` / `<SdkSnippet>` (Python, Go, Java, TypeScript; add Rust when
  `examples/rust` has the same sample). Do not use
  per-language headings or stacked fenced blocks. `bash` / `text` fences
  are exempt.
- Treat every published docs URL as permanent. Before moving, renaming, or
  deleting a page, add direct entries to `docs/redirects.json` for every
  affected locale. Redirect to the closest successor, never an unrelated page
  or the home page. Do not create redirect chains or loops. Update internal
  links to use the destination directly.
- Every docs change must preserve all four search-index integrity gates:
  routes and internal links resolve without accidental 404s; only declared
  legacy URLs redirect; each indexable page has one unique, self-referencing
  canonical URL; and robots directives, noindex/nofollow metadata, internal
  links, and the sitemap keep canonical pages crawlable.
- After any change under `docs/`, run `cd docs && npm run check`. This command
  must typecheck, build both locales, reject broken links, anchors, and duplicate
  routes, and pass the generated-site route, redirect, canonical, robots, and
  sitemap audit. Do not merge docs changes when it fails.
- Application snippets must be copied from runnable files under `examples/`.
  Link the example (and the examples playground when the sample has HTTP and
  is catalogued). Do not invent APIs; add the example first.
  See `.cursor/rules/docs-examples.mdc`.
- Whenever runnable code for a documented design pattern or product example is
  added or changed, run `cd docs && npm run generate:flow-definitions`. Review
  and commit the affected JSON under `docs/src/data/flow-definitions/` in the
  same change, even when the MDX import is unchanged.
- Design-pattern Step graph PNGs must remain responsive. Set only a percentage
  `max-width`, never a fixed width or height. Choose each maximum so its Flow type
  name renders at the same size as `DrainInternalChannelFlow` in
  `drain-internal-channel.png`, and keep the image centered. Use an HTML `img`
  with its `/img/design-patterns/` path so Docusaurus cannot inline the PNG as a
  data URL, then match its stable filename fragment in `docs/src/css/custom.css`.
  Apply this rule when adding or replacing a Step graph.
- Before producing a binary, add its exact path to both `.gitignore` and
  `.dockerignore`, then remove any stray uncommitted binaries.

## License Headers

- Every managed source file must use its `legacy-only`, `mixed`, or `new`
  header from `script/licenseheaders/legacy-manifest.json`.
- Web TypeScript, CSS, and HTML sources use the `new` header. Examples retain
  their existing MIT or Apache-2.0 headers.
- Skip generated trees: `**/gen/**`, `*.pb.go`, `*_pb.go`, `*.gen.*`.
- When creating or modifying such a file, check the top; if the header is
  missing, add it. Or run `make copyright` from the repo root.
- Use `make copyright` to add or upgrade headers and `make copyright-check` to
  verify classifications and normalized body hashes.

# Rust Workflow Schema Definitions

In Rust examples and SDK tests under `sdk-rust/crates/dex-sdk/tests/integ/`,
define every Attribute, Channel, and Stream as a module-level `static
LazyLock<T>`. Do not use a function to construct or return one of these
definitions. Reuse the static directly, or clone its initialized value only
when an owned field is required.

# FDG 2.0 Step Explanations

Every Step type in a Flow Definition Graph 2.0 Go source must declare exactly
one `// dex:explanation text:"..."` directive next to its `dex:group`. The text
is one sentence that states what the Step does. The analyzer stores it on the
Step node as `metadata.explanation` for Dex Web v2 Definition.

# Python Examples

Do not use `del` in Python examples merely to mark parameters or local values
as unused. Keep required callback parameters unused instead.

# RPC Names Start With Verbs

Every application-example RPC handler and explicit RPC name must begin with a
concrete action verb. Use verbs such as `get`, `list`, or `describe` for reads
and domain actions such as `send`, `update`, `delete`, or `move` for writes. Do
not use noun-only names such as `queuedMessages`, `history`, or `snapshot`, and
do not use placeholders such as `exampleRPC`.

# Prefer Complete, Precise Names

For every API, interface, class, type, method, function, field, variable, and
constant, prefer a complete and precise domain name over a short name. The name
must communicate its operation and subject at the call site or registration
boundary. Do not use generic names such as `Get`, `Update`, `Manager`, `Data`,
`Handler`, or `Runtime` when a specific domain name is available. Brevity is not
a goal; remove words only when the remaining name is equally precise and
unambiguous. In every language, do not introduce `normalize` or `Normalize`
identifiers; name the concrete operation, such as `TrimWhitespace`,
`SortSelections`, or `CanonicalizeURL`.

# Go Flow and Step Type Names

The Go SDK registers a Flow or Step by its Go type name without the package.
Name every Go Flow type after its domain, such as `ApprovalFlow` or
`ReplyFlow`; never name it just `Flow`. Flow types must be unique within one
Registry, and Step types must be unique within one Flow. A generic Flow or Step
type must implement `GetFlowType` or `GetStepType`. Do not add an override that
returns the default name.

# Go Integration Test Conventions (`server-zd/integ/**/*.go`)

## File Ordering

A callee appears below its caller. Prefer:

1. Type declaration
2. Constructor and its helpers
3. Main entry method
4. Event or step handlers in dispatch order
5. Sub-handlers
6. State-changing helpers
7. Encoders, converters, and pure transforms
8. Tiny accessors

Leave generated code unchanged and keep tightly coupled subsystem clusters intact.

Keep a struct's methods in one primary file. Do not split its method set across feature-specific files.

## Pointers and Naming

- Use `ptr.Any(value)` for pointer literals. Import
  `github.com/superdurable/dex/sdk-go/dex/ptr`.
- Give numeric literals explicit types, such as `ptr.Any(int64(0))`.
- Do not use `ptr.Any` when the pointer must alias a named variable used elsewhere.
- Do not call `proto.Clone` (or other defensive deep copies) "just in case".
  Passing a message transfers ownership unless the API says otherwise. Workflow
  inputs, signals, and activity payloads are freshly deserialized and
  single-threaded. Prefer capturing a small id/string or building a new tiny
  value over cloning. Copy only when an algorithm must mutate a distinct shared
  value in place.
- Use each package's declared name. Alias only for collisions, misleading names,
  or established conventions such as `dexpb`. Do not invent aliases such as
  `servermetrics` or `mongostore`.
- Use descriptive variable names. Receivers and `i j k n err ctx ok t mu wg id r
  w ch` are the only accepted one- or two-letter names.
- Boolean variables and constants, and methods returning booleans, must use
  predicate names such as `isXxx`, `hasXxx`, `canXxx`, `shouldXxx`, or
  `supportsXxx`, following the language's capitalization conventions.
- Do not introduce `NormalizeXyz` identifiers. Name the concrete operations,
  such as `ValidateAndSortSelections`, `TrimWhitespace`, or `CanonicalizeURL`.
- When a concept already has a repository or public API term, reuse that exact
  term in identifiers. Do not invent a synonym for an existing term such as
  instance, name, message, or definition.

## Nil and Config Fields

- Required dependencies must panic or `log.Fatal` when nil. Do not silently
  return for impossible nil values.
- Check nil only when it is a valid state, such as an optional field, cache miss,
  or user-supplied callback.
- Every config struct field needs a Go doc comment stating its default and
  meaning. Include immutability, relationships, ranges, or an example as needed.
- Address fields must document the protocol, connecting party, and
  bind-versus-advertise relationship.

## Never Ignore Errors

- Every returned error must be returned, logged, or explicitly acted on.
- Never use `_ = f()`, `value, _ := f()`, or an `err == nil` branch without an
  error path.
- If an error genuinely must be ignored, explain why in a short comment and call
  it out in review.

## Trusted and Untrusted Values

- Values from store rows, server-minted IDs, and controlled invariants are
  trusted. Violations are bugs: fail fast with a `Must*` helper, or preserve the
  typed value end-to-end.
- Values from HTTP requests, SDK/worker payloads, and any client-settable field
  are untrusted, even if marked internal.
- Validate untrusted values with an error-returning helper and return an
  input error. Never allow untrusted input to reach a `Must*`
  helper or panic path.

# Go Integration Testing (`server-zd/integ/**/*`)

## Execution

- After every code change, run tests through the Makefile, never bare `go test`
  for full suites.
- Always tee output:
  `make -C server-zd/integ <target> 2>&1 | tee /tmp/test-<scope>.log`.
- Targets: `compile`; `zdIntegTests`; `zdAttributeSyncIntegTests` for PostgreSQL integration.
- Fix all failures. After multiple unsuccessful attempts, report the failure,
  attempted fixes, and exact blocker.

## Do Not Casually Skip Failing Tests

- Never skip, gate, or early-return around a failing assertion just to make the
  suite green (e.g. `t.Skip` for a known
  flake) unless the user explicitly asks to skip.
- When a test fails: add targeted logs, inspect server-zd logs
  (and relevant API output), identify the root cause, then fix
  product code or the test expectation.
- Backend-specific skips need a proven platform limitation plus user agreement;
  prefer fixing Reset/query/retry behavior over hiding the path.

## Isolation and Async

- Prefer package-level isolation so tests can run in parallel across packages.
- Use unique workflow IDs / namespaces per test when sharing a server-zd
  instance.
- Use `require.Eventually` or polling for convergence. Do not use `time.Sleep`
  except inside the behavior under test.

# Go SDK Conventions (`sdk-go/**`)

Prefer explicit domain naming, thin public APIs, apply-style options, and
Def/Impl layering. Keep design docs and examples in sync with API changes.

## Naming

- Factory/constructor names include the domain noun: `InitialAttribute`,
  `InitialAttributeMapValue` — not bare `Initial` / `InitialMapValue`.
- Schema erasure interfaces use `*Def` (`AttributeDef`, `ChannelDef`,
  `InitialAttributeDef`, `StepDef`).
- Unexported concrete implementations use `*Impl` (e.g. `conditionImpl`), not
  `*Value`, unless the type is a true value object.
- Identity types use `*ID` (`StepExecutionID`, `TimerID`), not `*Ref`.
- Optional fields that may be omitted use pointers (`ExecutionNumber *int32`).
- Names describe real semantics, not a misleading action. Prefer
  `SkipWaitImmediately` over `ExecuteImmediately` when the value means “skip
  waiting,” not “run Execute.”
- When an entry method needs a recursive or stateful helper (extra args such as
  a cycle-tracking set), name the helper `doXxx` for the same verb phrase:
  `validateStepOptions` → `doValidateStepOptions`. Keep the entry thin;
  put the real work in `doXxx`.

## API shape

- Omit parameters the SDK can default (e.g. client methods target the current
  run; do not take `runID` unless an operation truly needs a specific run).
- Do not wrap a few flat inputs in an Options struct. Prefer direct parameters
  (`SearchFlows(ctx, query, pageSize, nextPageToken)`).
- Prefer batch APIs alongside or instead of thin single-item variants when that
  matches product shape; drop unused singles (e.g. public Delete helpers) rather
  than keeping dead surface area.
- Optional overrides are pointer fields on options (`RequestID *string`), with
  SDK-generated defaults when nil.

## Functional options

Use apply methods on the option interface — never an empty marker plus
`switch option.(type)`:

```go
type ConditionOption interface {
	applyCondition(*conditionImpl)
}

func applyConditionOptions(condition *conditionImpl, options []ConditionOption) {
	for _, option := range options {
		option.applyCondition(condition)
	}
}
```

Same pattern as `AttributeOption.applyAttribute` and
`StepMoveOption.applyStepMovement`.

## Sealed Def interfaces

Def interfaces exist for SDK-internal schema erasure. Seal them with
unexported methods only. Application-facing names stay on concrete typed values
(e.g. `Attribute[T].AttributeName()`), not on the Def interface.

## Documentation

When changing public SDK types or Client signatures, update in the same change:
`docs/design/plan/go-sdk-rewrite.md`, `sdk-go/README.md`, examples, and
compile-contract tests.

# Plan Requirements

Every implementation plan must include all three sections below. Use
`N/A: <one-line reason>` only when a section genuinely does not apply.

## Tests

- List specific integration and E2E scenarios and why each is needed.
- Default to integration tests in `server-zd/integ/` and the relevant SDK integ
  suites.
- Do not propose unit tests unless explicitly requested or the edge case cannot
  be reached through integration/E2E paths.

## Documentation

- Product docs: [`docs/`](docs/) (entry: [`docs/README.md`](docs/README.md)).
- Contributor / module docs: module READMEs or `CONTRIBUTING.md`.
- Application code snippets in `docs/content/` must use `<SdkTabs>` /
  `<SdkSnippet>` (Python, Go, Java, TypeScript; add Rust when `examples/rust`
  has the same sample). Do not use per-language
  headings or stacked fenced blocks. `bash` / `text` fences are exempt.
- Application snippets must be copied from runnable files under `examples/`.
  Link the example (and the examples playground when the sample has HTTP and
  is catalogued). Do not invent APIs; add the example first.
  See `.cursor/rules/docs-examples.mdc`.
- Product docs should read naturally: plain English, short sentences, no
  chatbot filler or marketing tone. See `.cursor/rules/docs-writing.mdc`.
- In product-doc prose, do not use inline backticks for API names, methods,
  types, or identifiers; use **bold** instead. Fenced code blocks and `bash` /
  `text` fences are exempt.
- Product docs ship in English and Simplified Chinese (`zh-Hans`); keep both
  locales in sync. See `.cursor/rules/docs-i18n.mdc`.

## UI/UX

- `N/A: no in-repo web UI` unless a change adds one.

## Rust Code Conventions

These rules apply to all hand-written Rust workspaces and standalone packages.
Run `make rust-rules-check` and each package's formatting, Clippy, and test targets.
The checker discovers Cargo manifests automatically through Cargo metadata.

| Rule | Convention | Enforcement |
| --- | --- | --- |
| R1 | Non-test Rust files contain at most 1000 lines, excluding the license header. | Rust rules checker; existing oversized files use a shrink-only baseline. |
| R2 | A module directory contains at most 10 non-test Rust files and 10 submodules. Split by responsibility. | Rust rules checker; existing oversized directories use the same baseline. |
| R3 | Every crate has a README with an opening Purpose sentence of at most 30 words. Start its lib.rs and main.rs crate documentation with the same sentence. | Rust rules checker. |
| R4 | Use pub(crate) for internal items and pub only for public APIs, including in the SDK. | rustc unreachable_pub. |
| R5 | Test-only public items end in _for_test and require cfg(test) or the test-util feature. | Rust rules checker verifies the configuration of suffixed items; review verifies naming. |
| R6 | Register every crate in script/rust-crate-layers.json. Depend only on crates in the same or a lower layer. | Rust rules checker using Cargo metadata. |
| R7 | Each executable has one typed configuration. Read environment settings only in configuration modules or tests. Configuration modules are config, *_config, and *_options. | Rust rules checker verifies std::env::var and std::env::var_os locations; review verifies typed ownership. |
| R8 | Use complete domain names. Avoid utils, helpers, common, and misc modules and generic Manager, Handler, Data, Info, or Runtime types. | Rust rules checker rejects generic module filenames; review checks type names. |
| R9 | Represent closed sets with enums rather than repeated strings. Across crates, use non-exhaustive enums or newtypes. | Clippy match_wildcard_for_single_variants and review. |
| R10 | Handle every error. Do not discard fallible results with let _ or .ok(); explain intentional ignores. | rustc unused_must_use and Clippy let_underscore_must_use, let_underscore_future, and unused_result_ok; review. |
| R11 | Libraries use typed errors, such as thiserror. Generic errors such as anyhow belong only at binary boundaries. Include the operation and object in errors. | Rust rules checker rejects non-development anyhow dependencies in library crates; review checks error context. |
| R12 | Panic on programming errors with expect explaining why failure is impossible. Production code must not use bare unwrap. Never use todo!, unimplemented!, or dbg!. | Clippy unwrap_used, todo, unimplemented, and dbg_macro; Rust rules checker rejects empty expect explanations. Tests may unwrap. |
| R13 | Libraries log with tracing. Keep messages constant and values in fields. Examples, CLI crates, and build scripts may print through registered crate lint overrides. | Clippy print_stdout and print_stderr; review checks structured messages. |
| R14 | Every background task, connection, and child process has an owner and cancellation path. Bind tokio::spawn JoinHandles; never use let _ to discard them. Explain deliberate detachment. | Rust rules checker verifies binding; review checks lifecycle and cancellation. |
| R15 | Forbid unsafe outside registered FFI crates. FFI crates use unsafe_code = "deny" and an expect with a safety reason at each unsafe operation. | rustc unsafe_code; Rust rules checker verifies registered FFI lint tables. |
| R16 | Suppress lints with #[expect(lint, reason = "...")], never #[allow]. Remove expectations when the lint no longer applies. | Clippy allow_attributes and allow_attributes_without_reason; rustc unfulfilled_lint_expectations. |
| R17 | Never hand-edit generated code. | make generated-code-check. |
| R18 | Preserve existing comments. New comments explain non-obvious reasons and contain fewer than 20 words per block. Public SDK API documentation follows the detailed SDK documentation rules. | SDK missing_docs and review. |
| R19 | Format Rust with rustfmt. | cargo fmt --all --check. |

Use the frozen workspace Rust and Clippy lint tables in every workspace. Members
inherit with `[lints] workspace = true`. Registered FFI and printing crates copy
the entire table, changing only unsafe_code to deny for FFI and print_stdout /
print_stderr to allow for printing crates. The checker rejects other differences.
Keep clippy.toml test settings identical across workspaces. CI runs Clippy with
`--all-targets -- -D warnings`.

The checker uses one generated-code predicate: opening Code generated or
@generated markers, or a gen/ or OUT_DIR path. Generated and test source files
are excluded from structural counts. Inline test modules still count toward a
production file's size. The baseline may only shrink against origin/main:
never add an entry or increase its ceiling. Remove an entry as soon as the
file or directory meets its limit; stale entries fail the check.

## server-zd Engine Rules (`server-zd/**`)

- `dexzd-apply` is a deterministic state machine. It never reads clocks,
  random numbers, configuration, the environment, files, or the network, and
  it never iterates a hash map. Values that change an apply result come from
  the command or from persisted state. Its `clippy.toml` bans the common
  offenders; keep that file's frozen test settings identical to the workspace.
- A change to the result of applying an already committed command needs a new
  behavior version with a named predicate. Never change the behavior of an
  existing version. This rule starts with server-zd 1.0.0, the first
  `server-zd/v1.*` release tag. Until then, server-zd stays at behavior
  version 1, an apply-result change needs no new version, and no work goes
  into version barriers or raising them.
- Persistent formats (`server-zd/proto/dexzd/`, the RocksDB key layout, the
  object storage layout, and snapshots) follow the protobuf compatibility
  rules: additive only; never renumber or reuse field numbers or names.
- Generated protobuf maps use `BTreeMap`.
- Local tests use the in-process s3mock or the in-memory object store, never
  MinIO.

### server-zd Rust conventions ported from the Server Go rules

These rules restate the applicable Server Go rules for `server-zd/**`. The
Rust table (R1–R19) still applies.

- File ordering: a callee appears below its caller. Order a file as type,
  constructor, entry method, handlers in dispatch order, sub-handlers,
  state-changing helpers, converters, then tiny accessors.
- One primary file per type: keep a type's inherent methods in one file. Do
  not split them across feature-specific files.
- Required dependencies: take each one as a non-`Option` constructor
  parameter. Never wire a dependency after construction with a setter.
- Bootstrap order: fix construction order instead of post-construction wiring.
- Config injection: a component that needs tunables receives its config
  section struct, not individual tunables or the whole server configuration.
  See R7. `dexzd-apply` stays configuration-free.
- Optional values: use `Option` only when `None` is a valid state. Never
  silently return on an impossible `None`.
- Config field docs: every config field has a doc comment stating its default
  and meaning. Address fields state the protocol, the connecting party, and
  bind versus advertise. These docs and return-value docs are exempt from R18's
  word limit and its non-obvious-reasons rule.
- API errors: map every request failure to a gRPC status plus a Dex
  `ErrorSubStatus`. Server startup or shutdown may return a bare `UNAVAILABLE`. Bad client or Worker input is a client error, such as
  `INVALID_ARGUMENT`, `NOT_FOUND`, or `FAILED_PRECONDITION`. Infrastructure
  failures are `INTERNAL` or `UNAVAILABLE`.
- Never ignore errors: see R10.
- Trusted versus untrusted values: store rows, server-minted IDs, and
  controlled invariants are trusted and may use `expect` with a reason. Request
  fields and Worker payloads are untrusted. Validate them with an
  error-returning helper; they never reach `expect`, `unwrap`, or `panic!`.
- No defensive clones: never `clone()` "just in case". Move ownership or
  borrow. Clone only when an algorithm needs a distinct value or a task needs
  shared ownership, such as an `Arc` handle.
- Crate names: use each crate's declared name. Alias with `as` only for a
  collision or a misleading name.
- Descriptive names: use descriptive variable names. Only `i j k n err ctx ok
  t id r w tx rx` and closure parameters in short iterator chains may be one or
  two letters.
- Boolean names: boolean variables, fields, constants, and functions returning
  `bool` use predicates such as `is_`, `has_`, `can_`, `should_`, or
  `supports_`.
- Existing terms: reuse the repository or public API term for a concept. Do
  not invent a synonym.
- Stateful closures: lift a closure into a method when it captures three or
  more values, mutates outer state, has several call sites, or outlives one
  statement.
- No casual test skips: never `#[ignore]`, gate, or early-return around a
  failing test without the user's agreement. Find the root cause and fix the
  code or the expectation.
- Test isolation: give each test unique Flow IDs, namespaces, and temporary
  directories so tests run in parallel.
- Async tests: wait with polling or paused tokio time. Use a real sleep only
  inside the behavior under test.
