# DEX — Claude Instructions

Durable Workflow Framework: gRPC server (`server/`), Go SDK (`sdk-go/`), WebUI (`web/`), benchmarks (`benchmark/`), and deployment tooling (`deploy/`). See [README.md](README.md) for the full module map.

## Plan Mode

Every plan must include all five sections below. Use `N/A: <one-line reason>` only when a section genuinely doesn't apply.

- `## Tests` — list specific scenarios (integration vs E2E, why each)
- `## Metrics` — table of new metrics with tier, type, and name
- `## Logging` — table mapping operations to log levels and example messages
- `## Documentation` — which docs in `docs/` to create or update
- `## UI/UX` — affected `web/app/...` routes/components, or `N/A: <reason>`

### Metrics tiers

- `MetricTierCritical` (1): business-critical invariants (e.g. run success rate)
- `MetricTierInfo` (2): required for production ops (e.g. shard-level batch results)
- `MetricTierDebug` (3): troubleshooting (e.g. per-task counters)
- `MetricTierDeepDebug` (4): deep investigation only

**By default, only add Critical/Info metrics.** Do NOT add `MetricTierDebug` or `MetricTierDeepDebug` metrics unless I explicitly ask. If nothing Critical/Info applies, write `N/A: <reason>` instead of padding with Debug counters; mention any Debug/DeepDebug idea as an optional opt-in, never add it preemptively.

Define new metrics in `server/internal/metrics/metrics_defs.go`; tags in `server/internal/metrics/tags_defs.go`.

### Logging levels

**Server-side (`server/`):**
- INFO: shard-level or low-frequency ops operators need in production
- ERROR: infrastructure failures needing operator attention
- WARN: degraded but recoverable infrastructure situations
- DEBUG: per-task / high-frequency, or **any condition caused by user/tenant error** (never inflate WARN/ERROR for tenant errors — a misbehaving tenant can flood logs)

**SDK/Worker (`sdk-go/`):**
- ERROR: unexpected failures the developer should investigate
- WARN: degraded but recoverable situations
- INFO: normal lifecycle milestones
- DEBUG: per-step or high-frequency tracing

### Tests

- Default to **integration tests** (`server/internal/integration/runengine/`) and **E2E tests** (`server/internal/integration/sdke2e/`).
- Do NOT add unit tests unless explicitly asked or the edge case is unreachable through integration/E2E paths.
- For cluster features, include a multi-instance E2E scenario.
- List specific test scenarios — not just "add tests".

### UI/UX

When changes have user-visible effects:
- Name affected `web/app/...` routes, components, and mappers.
- Specify how new state/events render — component names, where they slot in, what badges/colors/icons mean.
- Call out new BFF mapper types in `web/app/api/_grpc/mappers.ts`.
- Do not split "backend ships now, UI later" without a concrete UI plan on the backend PR.

## Code Quality Rules

### No Backward Compatibility

The project has **not launched**. Remove dead config fields immediately. Break APIs freely. Change store schemas without migrations. Ask before adding any compat shim.

### No Setter Injection

Constructor injection only. Never add `SetXyz`, `Inject*`, `Wire*`, or exported mutable fields for wiring components after `New*()` returns. Fix bootstrap ordering instead:

- gRPC `NewClient` is non-blocking — dial before the server starts if the address is known.
- Cyclic A↔B dependency → extract shared thing into C, inject C into both.
- Need listener address early → bind the listener earlier (cheap operation).

### Inject Config Sections by Pointer, Not Individual Fields

When a component needs tunables from a config section, pass a pointer to that whole section (`*config.RunServiceConfig`, `*config.TaskProcessorConfig`, `*config.MatchingEngineConfig`, …) into its constructor and read fields off it — do NOT thread individual fields as separate constructor params. Adding a new knob then touches only the use site, not every signature and call site.

- Store an unexported `cfg *config.XyzConfig`; read `h.cfg.SomeKnob` where used. Panic in the constructor if nil.
- Pass the **section**, not the whole `config.Config` (a component depends only on its own section, e.g. `&cfg.RunEngine`), and by **pointer**, not value.
- Standard for `RunsService` + `RunEngine` (`*config.RunServiceConfig`); apply to the other components (task processor, matching, …) as they're touched.

### No Stateful Closures — Use Methods on Structs

A closure that captures 3+ outer variables, mutates outer state, is called from more than one site, or outlives a single statement → lift it into a method on a struct with explicit fields.

Fine: one-shot callbacks (`sort.Slice`, `errgroup.Go`, `defer`), tiny pure transforms, IIFEs for scoping.

### No Obvious Comments

Write the fewest comments needed. Never restate what the code or a well-named identifier already says. Write a comment only for a non-obvious *why* (trade-off, workaround, ordering constraint, subtle invariant, hidden external dependency). When in doubt, improve the name instead.

### Short Comments — Under 20 Words

Every comment block (a contiguous group of `//` lines) must be fewer than 20 words. If you believe a longer one is necessary, ask the user first.

### Preserve Comments During Refactoring

Never delete existing comments during a refactor. Move them with the code they describe. Rewrite stale comments to reflect the new reality — do not drop them.

### Top-Down File Ordering (Go files)

In the same file, a callee always appears **below** its caller. High-level orchestration at the top, leaf helpers at the bottom. Preferred order for a struct-based file:

1. Type declaration
2. Constructor (`new<Type>`)
3. Constructor's own helpers
4. Main entry method (`Run`, `Serve`, `Handle`, `Process`)
5. Per-event/step handlers (in dispatch order)
6. Sub-handlers
7. Mutators / state-changing helpers
8. Encoders / converters / pure transforms
9. Tiny accessors at the very bottom

Exceptions: generated code (leave as-is); tightly grouped methods on different subsystems in one file (keep the cluster intact).

### `ptr.Any(...)` for Pointer Literals (Go)

Use `ptr.Any(value)` instead of a throwaway local variable taken by address. Import: `github.com/superdurable/dex/server/common/utils/ptr`. Use explicit types for numerics: `ptr.Any(int64(0))`, `ptr.Any(int32(1))`.

Do not use `ptr.Any` when the pointer must alias an existing named variable that is also read or mutated elsewhere.

### Update Ignore Files When Producing Binaries

Before running `go build -o <path>` or adding a new `main` package, add the output path to both `.gitignore` **and** `.dockerignore`. Use exact paths, not overly broad globs. Delete stray uncommitted binaries.

### Run Tests After Every Change

After code changes, run tests via the Makefile — not bare `go test`:

- `make -C server test` — full suite (~30s, requires Mongo/Postgres)
- `make -C server test-unit` — units only, no DB, <10s
- `make -C server test-integration` — integration sub-packages only

Always tee output: `make -C server test 2>&1 | tee /tmp/test-<scope>.log`

Fix all failures before moving on. If stuck after multiple attempts, pause and ask the user with: (1) the failure, (2) what you tried, (3) where you're blocked.

## Go-Specific Rules

### Config Field Comments

Every config struct field must have a Go doc comment:
1. Always state the default value.
2. Explain what it means/controls if non-obvious.
3. State immutability, relationships, valid ranges if constrained.
4. Add a concrete example if tricky.

For address fields, explain protocol served, who connects, and bind-vs-advertise relationship.

### Go Package Aliases

Use the package's declared name. Only alias when:
- Two packages share the same name in one file.
- The default name is misleading or ambiguous.
- An established repo convention applies (e.g. `pb` for generated protobuf packages).

Do not invent aliases like `servermetrics` or `mongostore`.

### No Unnecessary Nil Checks

Required dependencies must panic or `log.Fatal` if nil — fail fast at startup. Do not add `if x == nil { return nil }` guards that silently swallow bugs. Only add nil checks when nil is a valid, expected value (optional fields, cache misses, user-supplied callbacks).

### Server Error Handling (`server/`)

All server-side code that can fail must return `errors.CategorizedError` from `github.com/superdurable/dex/server/common/errors`, not plain `error`.

- Propagate `CategorizedError` directly — never re-wrap inside another `CategorizedError`.
- At gRPC handler boundaries, use `errors.ToProtoError(err)` — never hand-pick `status.Errorf(codes.X, ...)`.
- Category guide: CAS/optimistic lock → `ConflictError`; bad SDK/client input → `InvalidInputError`; infrastructure failure → `InternalError`; transient → `UnavailableError`; deadline → `TimeoutError`.
- Manual `status.Errorf` is correct only when the source error is not a `CategorizedError` (request validation, wire-level stream errors, direct dial failures, stream protocol violations).

### Never Silently Ignore Errors

Every returned error must be handled — returned, logged, or explicitly acted on. Never `_ = f()`, `x, _ := f()`, or `if err == nil { use(x) }` with no `else` that lets the failure path vanish. If you genuinely must ignore one, leave a code comment explaining why **and** call it out in review. Full rule: `.cursor/rules/no-ignored-errors.mdc`.

Trusted vs untrusted decides fail-fast vs graceful — ask where the value came from, not its type (the same field can be untrusted inbound but trusted on store read):

- **Trusted** (our own store rows, server-minted ids, invariants we control): a violated invariant is a bug — fail fast. Use a `Must*` helper that panics (e.g. `ids.MustParseTaskID`, `ids.MustParseBlobID`) rather than silently ignoring. Better still, thread the typed value end-to-end so there is no parse/error branch.
- **Untrusted** (gRPC request fields, SDK/worker payloads, anything a client can set — including "internal only" proto fields): must handle gracefully — validate with an error-returning helper (e.g. `ids.ValidateBlobID`) and return `InvalidInputError`; **never** `Must*`/panic. A malicious client must not be able to crash the server.

### Log Messages Must Be Static (`server/`)

Log messages (first argument to `logger.Info/Warn/Error`) must be static strings. All dynamic values go in tags: `tag.RunID(id)`, `tag.Namespace(ns)`, `tag.Address(addr)`, etc. No string concatenation or `fmt.Sprintf` in messages. `logger.Debug`/`logger.DebugF` are exempt.

### No Generic Log Tag Constructors (`server/common/log/tag/`)

Only explicit, named tag functions with a hardcoded slog key inside. Never add `NewIntTag(key, value)` or any helper that accepts an arbitrary key string. Add a new named function to `tags.go` for each new key.

### Naming — No 1-2 Letter Variable Names

Variables (struct fields, parameters, locals) must use descriptive names. Method receivers are exempt (Go convention: `func (w *Worker) ...`).

Allowed short non-receiver names: `i j k n err ctx ok t mu wg id r w ch`

## Kubernetes / Deployment Rules

### Always Use Explicit Kube Context

All `kubectl`, `helm`, or k8s commands targeting the local Kind cluster must use `--context kind-dex-e2e` (or `KUBE_CONTEXT="${KUBE_CONTEXT:-kind-dex-e2e}"`). Never rely on the default kube context — the user may have it pointed at a GKE or other production cluster.

### Benchmarks Require Multi-Replica

Always benchmark with ≥3 replicas (default in `deploy/kind/dex-values-kind.yaml`). Never reduce to 1 to "simplify" debugging — single-replica hides cluster routing bugs (tasklist partition forwarding, shard cross-node dispatch, heartbeat forwarding). Check logs on **all** nodes. Use `deploy/scripts/benchmark-escalation.sh` with `wait=false`; poll the DB directly for completion.

## Test Isolation Rules

Tests run in parallel across packages (no `-p 1`). Follow these rules for any code under `server/`.

### Per-Package Database Prefix

Each test package that touches the DB must use its own `dex_test_<pkg>_*` prefix. Use `testhelpers.ApplyPersistence(t, &cfg, uri, dbPrefix)` or `testhelpers.NewStoreSetForTest(t, dbPrefix)`. Never share a database across packages.

```go
// server/internal/integration/<pkg>/main_test.go
const dbPrefix = "dex_test_integration_mypkg"
func TestMain(m *testing.M) { testhelpers.RunMain(m, dbPrefix) }
```

Backend selection: `DEX_TEST_PERSISTENCE_BACKEND` (default `postgres`). Use backend-agnostic helpers — do not hardcode `mongo.New*` in integration tests.

### Per-Test Isolation by Namespace

Generate a unique namespace per test: `ns := "mytest-" + uuid.NewString()`. Scope every write/read to that namespace. Never call `DeleteAll`-style full-collection wipes per test.

### No `time.Sleep` for Async Convergence

Use `require.Eventually` or a polling loop. `time.Sleep` is only acceptable inside the system under test itself (e.g. a step that must take 100ms).

### Port Ranges

Memberlist ports are reserved in `server/internal/integration/testhelpers/testports/testports.go`. Never hardcode a port literal in a test file. When adding a new package with memberlist ports, add a disjoint constant range to `testports.go`.

| Constant | Range | Used by |
|---|---|---|
| `testports.InternalCluster` | `37946–37999` | `server/internal/cluster` |
| `testports.IntegrationCluster` | `17946–17999` | `server/internal/integration/cluster` |

### Expensive Setup in `TestMain`

Multi-node clusters, memberlist, and Docker-managed processes go in `TestMain` and are shared across all tests in the package. Surface setup errors via a package-level `var <foo>StartErr error`.

### Split Large Test Packages

If a package's wall time exceeds ~30s, split into sub-packages (each with its own `dbPrefix`, `TestMain`, port range). Move shared helpers to `internal/integration/testhelpers/` only if used by ≥2 sub-packages.
