# server-zd Integration Test Rules

The repository root `AGENTS.md` and `server-zd/AGENTS.md` rules also apply.

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
