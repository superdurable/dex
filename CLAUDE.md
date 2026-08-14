# Dex Project Rules

Dex is a durable workflow framework with a Go server (`server/`), OpenAPI IDL
(`protos/`), and SDKs/samples for Go, Java, and Python. See `README.md` for the
module map.

## Compatibility

- The project has not launched. Remove dead config fields immediately.
- Break APIs, interfaces, and data formats freely. Prefer the cleanest design.
- Do not keep shims, dual-path logic, deprecated aliases, or migration adapters.
- Do not add docs or comments that explain former behavior.
- Ask before adding any backward-compatibility shim.

## Dependency Injection

- Use constructor injection. Never add `SetXyz`, `Inject*`, `Wire*`, or exported
  mutable fields to wire dependencies after construction.
- Fix bootstrap ordering instead of post-construction wiring.
- Inject a pointer to the component's config section, not individual tunables or
  the whole `config.Config`.
- Store it as `cfg *config.XyzConfig`, panic on nil in the constructor, and read
  fields where used.

## Maintainability

- Within `server/service/interpreter/`, export every component type. Helper and
  value types that are not components may remain unexported.
- Export constructors and methods called by another interpreter component,
  even when both components share the `interpreter` package.
- Interpreter components may use `UnifiedContext` during construction but must
  not retain it. Pass the operation-scoped context explicitly to methods.
- Handle every interpreter `Await` error immediately. After a successful
  `Await`, write synchronously without a redundant context check; otherwise
  check the context before mutating state.
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
- The new-comment rules do not apply to `server/config/`; configuration comments
  should favor complete operational semantics.
- Comments documenting function or method return values are exempt from the
  20-word limit and should describe complete return semantics.
- Public SDK API documentation is exempt from the short-comment and
  no-obvious-comment rules. Document every public or exported SDK type,
  interface, class, method, function, constructor, field, constant, enum value,
  struct member, annotation element, and equivalent language construct. Do not
  document non-public or generated declarations.
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

# Server Go Conventions (`server/**/*.go`)

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
  `github.com/superdurable/dex/service/common/ptr`.
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

## Nil and Config Fields

- Required dependencies must panic or `log.Fatal` when nil. Do not silently
  return for impossible nil values.
- Check nil only when it is a valid state, such as an optional field, cache miss,
  or user-supplied callback.
- Every config struct field needs a Go doc comment stating its default and
  meaning. Include immutability, relationships, ranges, or an example as needed.
- Address fields must document the protocol, connecting party, and
  bind-versus-advertise relationship.

# Server Error Handling (`server/**/*.go`)

- API failures that reach Gin handlers should use `errors.ErrorAndStatus` from
  `github.com/superdurable/dex/service/common/errors` with an
  `dexpb.ErrorSubStatus` and HTTP status code.
- Prefer `NewErrorAndStatus` / `NewErrorAndStatusWithWorkerError`.
- Bad client/SDK input → 4xx + appropriate `ErrorSubStatus`.
- Infrastructure / unexpected failures → 5xx.

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
  input-style `ErrorAndStatus`. Never allow untrusted input to reach a `Must*`
  helper or panic path.

# Server Testing (`server/**/*`)

## Execution

- After every code change, run tests through the Makefile, never bare `go test`
  for full suites.
- Always tee output:
  `make -C server <target> 2>&1 | tee /tmp/test-<scope>.log`.
- Targets: `unitTests`; `integTests` / `temporalIntegTests` /
  `cadenceIntegTests`; `ci-all-tests` for the CI matrix.
- Fix all failures. After multiple unsuccessful attempts, report the failure,
  attempted fixes, and exact blocker.

## Do Not Casually Skip Failing Tests

- Never skip, gate, or early-return around a failing assertion just to make the
  suite green (e.g. `if backend == Cadence { return }` / `t.Skip` for a known
  flake) unless the user explicitly asks to skip.
- When a test fails: add targeted logs, dump Temporal/Cadence workflow history
  (and relevant query/describe output), identify the root cause, then fix
  product code or the test expectation.
- Backend-specific skips need a proven platform limitation plus user agreement;
  prefer fixing Reset/query/retry behavior over hiding the path.

## Isolation and Async

- Prefer package-level isolation so tests can run in parallel across packages.
- Use unique workflow IDs / namespaces per test when sharing a Temporal/Cadence
  stack.
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
- Default to integration tests in `server/integ/` and the relevant SDK integ
  suites.
- Do not propose unit tests unless explicitly requested or the edge case cannot
  be reached through integration/E2E paths.

## Documentation

- Product docs: [`docs/`](docs/) (entry: [`docs/README.md`](docs/README.md)).
- Contributor / module docs: module READMEs or `CONTRIBUTING.md`.

## UI/UX

- `N/A: no in-repo web UI` unless a change adds one.
