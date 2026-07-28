# iWF Project Rules

iWF is a durable workflow framework with a Go server (`server/`), OpenAPI IDL
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

- Lift stateful closures into struct methods when they capture 3+ values, mutate
  outer state, have multiple call sites, or outlive one statement.
- One-shot callbacks, tiny pure transforms, and IIFEs are acceptable.
- New comments explain only non-obvious reasons, trade-offs, invariants, or
  external constraints. Prefer clearer names over obvious new comments.
- Keep each new contiguous comment block under 20 words. Ask before exceeding
  this for a new comment.
- These comment rules do not apply to existing comments. Never simplify,
  shorten, rephrase, grammar-fix, or delete existing comments for style.
- Preserve existing comments verbatim during refactors and move them with their
  code. If one becomes stale, update only outdated references while retaining
  every original detail and meaning.
- The new-comment rules do not apply to `server/config/`; configuration comments
  should favor complete operational semantics.
- Before producing a binary, add its exact path to both `.gitignore` and
  `.dockerignore`, then remove any stray uncommitted binaries.

## License Headers

- Every new or edited `.go` / `.java` / `.py` file (and hand-written OpenAPI YAML
  under `protos/`) must start with the license header for its directory.
- Templates and mapping: `script/licenseheaders/` (`mit`, `apache-2.0`,
  `dual-mit-apache`). The tool picks the template by longest path prefix.
- Skip generated trees: `**/gen/**`, `*.pb.go`, `*_pb.go`, `*.gen.*`.
- When creating or modifying such a file, check the top; if the header is
  missing, add it. Or run `make copyright` from the repo root.
- Use `make copyright-check` to verify; `make copyright-replace` to rewrite to
  the Super Durable per-directory template (destructive).

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

## Pointers and Naming

- Use `ptr.Any(value)` for pointer literals. Import
  `github.com/superdurable/iwf/service/common/ptr`.
- Give numeric literals explicit types, such as `ptr.Any(int64(0))`.
- Do not use `ptr.Any` when the pointer must alias a named variable used elsewhere.
- Do not call `proto.Clone` (or other defensive deep copies) "just in case".
  Passing a message transfers ownership unless the API says otherwise. Workflow
  inputs, signals, and activity payloads are freshly deserialized and
  single-threaded. Prefer capturing a small id/string or building a new tiny
  value over cloning. Copy only when an algorithm must mutate a distinct shared
  value in place.
- Use each package's declared name. Alias only for collisions, misleading names,
  or established conventions such as `iwfidl`. Do not invent aliases such as
  `servermetrics` or `mongostore`.
- Use descriptive variable names. Receivers and `i j k n err ctx ok t mu wg id r
  w ch` are the only accepted one- or two-letter names.

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
  `github.com/superdurable/iwf/service/common/errors` with an
  `iwfidl.ErrorSubStatus` and HTTP status code.
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
