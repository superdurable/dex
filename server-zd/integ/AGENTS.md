# server-zd Integration Test Rules

The repository root `AGENTS.md` and `server-zd/AGENTS.md` rules also apply.

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
