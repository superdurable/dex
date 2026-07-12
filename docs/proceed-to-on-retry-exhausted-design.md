# Proceed To Step On Retry Exhausted

When a step's WaitFor or Execute method exhausts its retry budget, the run can
continue to a user-configured error-handler step instead of failing.

## Configuration

Set on `StepOptions`:

- `WaitForMethodProceedToAfterRetryExhausted`
- `ExecuteMethodProceedToAfterRetryExhausted`

Assign a handler step directly (`&RefundStep{}`). The handler receives the
**same input** as the failing step. Use `ctx.FromStepExecutionID()` to learn
which step failed (works for normal `GoTo` children too).

`Registry.Register` panics if a proceed handler is not registered in the same
flow or if its input type is incompatible with the failing step (`Step[any]`
handlers accept any concrete input type).

## Worker behavior

On retry exhaustion, if a handler is configured and registered:

1. Build `StepMethodReport` with `outcome=FAILED`.
2. Spawn handler via `NextSteps` with copied input and `from_step_exe_id` =
   failing step's exe id.
3. Run continues (`Running`).

If no handler is configured, behavior is unchanged (fail the run).

## Server behavior

- **Execute**: accepts `execute_method FAILED` + `next_steps` +
  `stop_decision=NONE`.
- **WaitFor**: accepts `wait_for_method FAILED` + `next_steps`; does not emit
  `RunStop(Failed)`.

History records the failed method report and spawned `next_steps`.

## Demo

Benchmark worker: `GET /trigger?mode=saga&methodKind=execute|waitFor`

See [benchmark/cmd/benchmarkworker/saga_flow.go](../benchmark/cmd/benchmarkworker/saga_flow.go).
