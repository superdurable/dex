# DEX Project Rules

DEX is a durable workflow framework with a gRPC server (`server/`), Go SDK
(`sdk-go/`), WebUI (`web/`), benchmarks (`benchmark/`), and deployment tooling
(`deploy/`). See `README.md` for the module map.

## Compatibility

- The project has not launched. Remove dead config fields immediately.
- Break APIs and change store schemas without migrations when appropriate.
- Ask before adding any backward-compatibility shim.

## Dependency Injection

- Use constructor injection. Never add `SetXyz`, `Inject*`, `Wire*`, or exported
  mutable fields to wire dependencies after construction.
- Fix bootstrap ordering instead: dial non-blocking gRPC clients before startup,
  bind listeners earlier when their address is needed, or extract cyclic shared
  dependencies into a third component.
- Inject a pointer to the component's config section, not individual tunables or
  the whole `config.Config`.
- Store it as `cfg *config.XyzConfig`, panic on nil in the constructor, and read
  fields where used.

## Maintainability

- Lift stateful closures into struct methods when they capture 3+ values, mutate
  outer state, have multiple call sites, or outlive one statement.
- One-shot callbacks, tiny pure transforms, and IIFEs are acceptable.
- Comments explain only non-obvious reasons, trade-offs, invariants, or external
  constraints. Prefer clearer names over obvious comments.
- Keep every contiguous comment block under 20 words. Ask before exceeding this.
- Preserve comments during refactors; move them or update stale wording.
- Before producing a binary, add its exact path to both `.gitignore` and
  `.dockerignore`, then remove any stray uncommitted binaries.

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
  `github.com/superdurable/dex/server/common/utils/ptr`.
- Give numeric literals explicit types, such as `ptr.Any(int64(0))`.
- Do not use `ptr.Any` when the pointer must alias a named variable used elsewhere.
- Use each package's declared name. Alias only for collisions, misleading names,
  or established conventions such as `pb`. Do not invent aliases such as
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

- Every fallible server operation returns `errors.CategorizedError` from
  `github.com/superdurable/dex/server/common/errors`, not plain `error`.
- Propagate categorized errors directly; never wrap one inside another.
- At gRPC handler boundaries, use `errors.ToProtoError(err)`.
- Categories: CAS/optimistic lock → `ConflictError`; invalid SDK/client input →
  `InvalidInputError`; infrastructure failure → `InternalError`; transient
  failure → `UnavailableError`; deadline → `TimeoutError`.
- Manual `status.Errorf` is only for non-categorized sources: request validation,
  wire-level stream errors, direct dial failures, and protocol violations.

## Never Ignore Errors

- Every returned error must be returned, logged, or explicitly acted on.
- Never use `_ = f()`, `value, _ := f()`, or an `err == nil` branch without an
  error path.
- If an error genuinely must be ignored, explain why in a short comment and call
  it out in review.

## Trusted and Untrusted Values

- Values from store rows, server-minted IDs, and controlled invariants are
  trusted. Violations are bugs: fail fast with a `Must*` helper such as
  `ids.MustParseTaskID`, or preserve the typed value end-to-end.
- Values from requests, SDK/worker payloads, and any client-settable proto field
  are untrusted, even if marked internal.
- Validate untrusted values with an error-returning helper such as
  `ids.ValidateBlobID`, then return `InvalidInputError`.
- Never allow untrusted input to reach a `Must*` helper or panic path.

# Server Logging (`server/**/*.go`)

## Levels

- INFO: shard-level or low-frequency events operators need in production.
- ERROR: infrastructure failures requiring operator attention.
- WARN: degraded but recoverable infrastructure conditions.
- DEBUG: per-task or high-frequency events and every user/tenant-caused
  condition. Tenant behavior must not flood WARN or ERROR logs.

## Messages and Tags

- The first argument to `logger.Info`, `logger.Warn`, and `logger.Error` must be
  a static string.
- Put every dynamic value in structured tags such as `tag.RunID(id)`,
  `tag.Namespace(namespace)`, and `tag.Address(address)`.
- Never concatenate strings or use `fmt.Sprintf` in those messages.
- `logger.Debug` and `logger.DebugF` are exempt from the static-message rule.
- In `server/common/log/tag/`, provide only explicit named tag functions with a
  hardcoded slog key.
- Never add a generic tag constructor accepting an arbitrary key. Add a named
  function to `tags.go` for each new key.

# Server Testing (`server/**/*`)

## Execution

- After every code change, run tests through the Makefile, never bare `go test`.
- Always tee output: `make -C server <target> 2>&1 | tee /tmp/test-<scope>.log`.
- Targets: `test` for the full suite, `test-unit` for no-DB unit tests, and
  `test-integration` for integration subpackages.
- Fix all failures. After multiple unsuccessful attempts, report the failure,
  attempted fixes, and exact blocker.

## Isolation

- Tests run in parallel across packages; never force `-p 1`.
- Each DB-using package gets a unique `dex_test_<pkg>_*` prefix through
  `testhelpers.ApplyPersistence` or `testhelpers.NewStoreSetForTest`.
- Select backends with `DEX_TEST_PERSISTENCE_BACKEND` (default `postgres`) and
  use backend-agnostic helpers. Never hardcode `mongo.New*`.
- Generate a unique namespace per test, for example
  `"mytest-" + uuid.NewString()`, and scope every read/write to it.
- Never perform full-collection cleanup per test.

## Async and Expensive Setup

- Use `require.Eventually` or polling for convergence. Do not use `time.Sleep`
  except inside the behavior under test.
- Put multi-node clusters, memberlist, and Docker processes in `TestMain`, shared
  by the package. Expose setup failures as `var <name>StartErr error`.
- Reserve memberlist ports in
  `server/internal/integration/testhelpers/testports/testports.go`; never
  hardcode ports in tests.
- Existing ranges: `InternalCluster` 37946–37999 and `IntegrationCluster`
  17946–17999. New packages need a disjoint range.
- Split test packages exceeding roughly 30 seconds. Each subpackage gets its own
  DB prefix, `TestMain`, and port range.
- Move helpers to `internal/integration/testhelpers/` only when at least two
  subpackages use them.

# Deployment and Benchmarks (`deploy/`, `benchmark/`, `server/`)

## Kubernetes Safety

- Every `kubectl`, `helm`, or Kubernetes command targeting local Kind must pass
  `--context kind-dex-e2e` or set
  `KUBE_CONTEXT="${KUBE_CONTEXT:-kind-dex-e2e}"`.
- Never rely on the default Kubernetes context; it may target production.

## Benchmarks

- Run benchmarks with at least three replicas. Do not reduce to one for
  debugging because that hides cluster-routing defects.
- Use the defaults in `deploy/kind/dex-values-kind.yaml`.
- Inspect logs on every node.
- Run `deploy/scripts/benchmark-escalation.sh` with `wait=false`.
- Poll the database directly for completion.

# Plan Requirements

Every implementation plan must include all five sections below. Use
`N/A: <one-line reason>` only when a section genuinely does not apply.

## Tests

- List specific integration and E2E scenarios and why each is needed.
- Default to integration tests in `server/internal/integration/runengine/` and
  E2E tests in `server/internal/integration/sdke2e/`.
- Do not propose unit tests unless explicitly requested or the edge case cannot
  be reached through integration/E2E paths.
- Cluster features require a multi-instance E2E scenario.

## Metrics

- By default, add no metrics. Only propose them when explicitly requested.
- When requested, provide a table with tier, type, and name.
- Define metrics in `server/internal/metrics/metrics_defs.go` and tags in
  `server/internal/metrics/tags_defs.go`.
- Tiers: `MetricTierCritical` (business invariants), `MetricTierInfo`
  (production operations), `MetricTierDebug` (troubleshooting), and
  `MetricTierDeepDebug` (deep investigation).

## Logging

- Provide a table mapping operations to levels and example messages.
- Server: INFO for low-frequency operational events; ERROR for infrastructure
  failures; WARN for recoverable degradation; DEBUG for high-frequency events
  and every tenant-caused condition.
- SDK/worker: ERROR for unexpected failures; WARN for recoverable degradation;
  INFO for lifecycle milestones; DEBUG for high-frequency tracing.

## Documentation

- Name docs under `docs/` to create or update.

## UI/UX

- Name affected `web/app/...` routes, components, and mappers.
- Specify state/event placement and badge, color, and icon semantics.
- Call out new BFF mapper types in `web/app/api/_grpc/mappers.ts`.
- Do not defer UI for a user-visible backend change without a concrete UI plan.
