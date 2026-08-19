# Interruptible Execution Flow

Shows how long-running work can be interrupted and terminated gracefully from
outside the flow. Useful when requirements change or external conditions make
in-flight work irrelevant.

## Key components

- `InterruptibleExecutionFlow` — owns the `interruptSignal` attribute and the
  `interrupt` RPC.
- `Init` — starts `WorkAExecution` and `WorkNExecution` in parallel with the
  same `WorkJobParametersInput` (15 jobs, starting at 1).
- `WorkAExecution` / `WorkNExecution` — tick on a 1.5-second and 3-second timer
  respectively, do one unit of work, and loop back with the progress
  incremented until it passes the upper bound.

## Interruption logic

The `interrupt` RPC sets the `interruptSignal` attribute to `"cancel"`. Each
work step reads that attribute at the start of `execute` and completes the flow
instead of scheduling the next iteration.

## Use cases and considerations

- **Dynamic task management** — adapt to real-time changes without terminating
  the flow abruptly.
- **Resource optimization** — stop work that is no longer relevant.
- **Error handling** — interrupt flows that hit unexpected conditions.

Trade-offs: interruption logic adds branching to every step, and each step must
be careful about the state it leaves behind when it stops early.
