# Dex — Codex Instructions

Durable workflow framework: Go server (`server/`), OpenAPI IDL (`protos/`), and
SDKs/samples for Go, Java, and Python. See [README.md](README.md) for the module
map.

## Plan Mode

Every plan must include all three sections below. Use `N/A: <one-line reason>`
only when a section genuinely doesn't apply.

- `## Tests` — list specific scenarios (integration vs E2E, why each)
- `## Documentation` — which module READMEs / `CONTRIBUTING.md` to create or update
- `## UI/UX` — usually `N/A: no in-repo web UI`

### Tests

- Default to **integration tests** (`server/integ/`) and SDK integ suites.
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
- In product-doc prose (`docs/content/` and the matching `zh-Hans` pages), do
  not use inline backticks for API names, methods, types, or identifiers. Use
  **bold** instead (for example **WaitFor**, **StepDecision**). Fenced code
  blocks and `bash` / `text` fences are exempt.
- Product docs ship in English and Simplified Chinese (`zh-Hans`); keep both
  locales in sync. See `.cursor/rules/docs-i18n.mdc`.
- Application snippets in product docs must be copied from runnable files under
  `examples/`. Link the example (and the examples playground when the sample has
  HTTP and is catalogued). Do not invent APIs; add the example first.
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

### UI/UX

- No in-repo web UI. Do not invent Temporal Web work unless the change adds a UI.

## Code Quality Rules

### Commit Every Changing Turn

End every agent turn that changes repository files with one commit on the
current branch. Leave a clean working tree. Do not create empty commits for
discussion-only turns.

Never add Cursor as Author, Committer, or `Co-authored-by`. Cursor's commit
wrapper often injects `cursoragent@cursor.com`; strip it before pushing. After
every commit run `git log -1 --format=%B`. Do not use `--no-verify`. See
`.cursor/rules/git-commit.mdc`.

### Branch From Latest Main

Before creating a feature branch, fetch and branch from `origin/main`. See
`.cursor/rules/git-branch.mdc`. Cursor and Codex hooks enforce this workflow
(`script/branch_off_main_policy.py`; Codex requires `features.codex_hooks =
true` in `~/.codex/config.toml`).

### Agent Rule Synchronization

Keep the Cursor (`.cursor/rules/`), Codex (`AGENTS.md`), and Claude
(`CLAUDE.md`) coding-agent rules synchronized. Any addition or modification to
one requires equivalent updates to all three in the same commit.

### Temporal Skill Routing

- In this repository, use the `temporal-developer` skill only when changing or
  diagnosing `server/service/interpreter/**` requires Temporal runtime or SDK
  semantics.
- Do not use it for other modules, SDKs, examples, docs, routine integration
  tests, or merely because Temporal, workflow, signal, query, worker,
  heartbeat, or `temporalIntegTests` is mentioned.
- An explicit user request to use `temporal-developer` overrides this
  restriction.

### Regenerate the Entire Repository After Proto Changes

Whenever any `.proto` file changes, run `make generated-code` from the repository
root and commit every resulting change. Proto-changing PRs must refresh all
checked-in generated code across the server and SDKs. Do not use component-only
codegen targets for these PRs; they leave stale outputs for the next PR.

### Do Not Reserve Proto Fields Before Launch

Do not add `reserved` field numbers or names to `.proto` files. The project has
not launched, so removed fields do not need compatibility protection. Delete the
field and renumber the remaining fields in that message into contiguous order,
then regenerate the entire repository.

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

### No Backward Compatibility

The project has **not launched**. Remove dead config fields immediately. Break
APIs freely. Ask before adding any compat shim. Do not leave docs/comments that
explain former behavior.

### Rust Workflow Schema Definitions

In Rust examples and SDK tests under `sdk-rust/crates/dex-sdk/tests/integ/`,
define every Attribute, Channel, and Stream as a module-level `static
LazyLock<T>`. Do not use a function to construct or return one of these
definitions. Reuse the static directly, or clone its initialized value only
when an owned field is required.

### Python Examples

Do not use `del` in Python examples merely to mark parameters or local values
as unused. Keep required callback parameters unused instead.

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

### Inject Config Sections by Pointer, Not Individual Fields

When a component needs tunables from a config section, pass a pointer to that
whole section into its constructor and read fields off it — do NOT thread
individual fields as separate constructor params.

- Store an unexported `cfg *config.XyzConfig`; read `h.cfg.SomeKnob` where used.
  Panic in the constructor if nil.
- Pass the **section**, not the whole `config.Config`, and by **pointer**, not
  value.

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

The new-comment simplification and 20-word rules also do not apply to
`server/config/`. Configuration comments should favor complete operational
semantics.

Comments documenting function or method return values are also exempt from the
20-word limit and should describe complete return semantics.

### Detailed Public SDK API Documentation

Public SDK API documentation is exempt from the short-comment and
no-obvious-comment rules. Document every public or exported SDK type,
interface, class, method, function, constructor, field, constant, enum value,
struct member, annotation element, and equivalent language construct. Do not
add API documentation to non-public or generated declarations.

Write each API document from the application developer's perspective. Start
with a one-sentence summary, then explain when and how the API is used. Give a
complete language-native example for each related API family; overloads and
simple accessors may refer to their enclosing type's example instead of
repeating it. Explain defaults, units, lifecycle, blocking or concurrency
behavior, nullability, ownership, side effects, errors, and other tricky
semantics whenever users must understand them. Finish with language-native
documentation for every type parameter, input, output, and user-visible error.
Do not impose a word limit; prefer a complete paragraph over a terse fragment.
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
Import: `github.com/superdurable/dex/service/common/ptr` (server) or
`github.com/superdurable/dex/sdk-go/dex/ptr` (SDK). Use explicit types for
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

- `make -C server unitTests`
- `make -C server integTests` / `temporalIntegTests` / `cadenceIntegTests`

Always tee output: `make -C server unitTests 2>&1 | tee /tmp/test-<scope>.log`

Fix all failures before moving on. If stuck after multiple attempts, pause and
ask the user with: (1) the failure, (2) what you tried, (3) where you're blocked.

### Do Not Casually Skip Failing Tests

Never skip/gate/early-return around a failing assertion just to green the suite
(e.g. `if backend == Cadence { return }` or `t.Skip` for a known flake) unless
the user explicitly asks. On failure: add targeted logs, dump Temporal/Cadence
workflow history (plus describe/query output), find the root cause, then fix
product code or the expectation. Backend-specific skips need a proven platform
limit and user agreement.

## Go-Specific Rules

### Strongly Typed Log Tags

Never use or reintroduce `tag.Value`. Add a semantic, strongly typed constructor in `server/service/common/log/tag` for each structured field.

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

### Server Error Handling (`server/`)

API failures that reach Gin handlers should use `errors.ErrorAndStatus` from
`github.com/superdurable/dex/service/common/errors` with an
`dexpb.ErrorSubStatus` and HTTP status code.

- Prefer `NewErrorAndStatus` / `NewErrorAndStatusWithWorkerError`.
- Bad client/SDK input → 4xx + appropriate `ErrorSubStatus`.
- Infrastructure / unexpected failures → 5xx.

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
  set): must handle gracefully — validate and return an input-style
  `ErrorAndStatus`; **never** `Must*`/panic.

### Naming — No 1-2 Letter Variable Names

Variables (struct fields, parameters, locals) must use descriptive names. Method
receivers are exempt (Go convention: `func (w *Worker) ...`).

Allowed short non-receiver names: `i j k n err ctx ok t mu wg id r w ch`

### Boolean Names Are Predicates

Boolean variables and constants, and methods returning booleans, must use names
that clearly signal boolean semantics, such as `isXxx`, `hasXxx`, `canXxx`,
`shouldXxx`, or `supportsXxx`. Follow each language's capitalization conventions.

### Interpreter Components

Within `server/service/interpreter/`, every component type must be exported.
Helper and value types that are not components may remain unexported.
Constructors and methods called by another component must also be exported,
even when both components share the `interpreter` package. Component-internal
methods remain unexported.

Components may use `UnifiedContext` during construction but must not retain it.
Pass the operation-scoped context explicitly to methods. Handle every `Await`
error immediately. After a successful `Await`, write synchronously without a
redundant context check; otherwise check the context before mutating state.

## Go SDK Conventions (`sdk-go/`)

Prefer explicit domain naming, thin public APIs, apply-style options, and
Def/Impl layering. Keep design docs and examples in sync with API changes.

### Naming

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

### API shape

- Omit parameters the SDK can default (e.g. client methods target the current
  run; do not take `runID` unless an operation truly needs a specific run).
- Do not wrap a few flat inputs in an Options struct. Prefer direct parameters
  (`SearchFlows(ctx, query, pageSize, nextPageToken)`).
- Prefer batch APIs alongside or instead of thin single-item variants when that
  matches product shape; drop unused singles (e.g. public Delete helpers) rather
  than keeping dead surface area.
- Optional overrides are pointer fields on options (`RequestID *string`), with
  SDK-generated defaults when nil.

### Functional options

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

### Sealed Def interfaces

Def interfaces exist for SDK-internal schema erasure. Seal them with
unexported methods only. Application-facing names stay on concrete typed values
(e.g. `Attribute[T].AttributeName()`), not on the Def interface.

### Documentation

When changing public SDK types or Client signatures, update in the same change:
[`docs/design/plan/go-sdk-rewrite.md`](docs/design/plan/go-sdk-rewrite.md),
[`sdk-go/README.md`](sdk-go/README.md), examples, and compile-contract tests.

## Test Isolation Rules

### No `time.Sleep` for Async Convergence

Use `require.Eventually` or a polling loop. `time.Sleep` is only acceptable
inside the system under test itself.

### Unique IDs Per Test

Generate unique workflow IDs (and namespaces when applicable) per test when
sharing a Temporal/Cadence stack. Never rely on leftover state from another test.
