# Transient step movement

A WaitFor method may return one `transient_step_movement` with its waiting
condition. This supports a durable setup action that must finish before the
source step begins waiting.

## Execution

The interpreter applies the WaitFor attribute and channel writes, executes the
transient movement, and then initializes the waiting condition. Timer durations
therefore start after the transient Execute succeeds. Channel messages received
during the Execute remain available when condition matching begins.

The transient movement is a standard step execution with its own execution ID,
lineage, active/completion state, and continue-as-new operation count. Its
lineage source is the WaitFor step execution.

## Contract

The movement must:

- set `skip_wait_for`;
- target a non-empty step type;
- omit WaitFor and Execute failure-proceed behavior; and
- return a DeadEnd close decision from Execute without next steps.

An Execute failure fails the flow. No fallback step runs, and the source
condition is not initialized.

## Continue-as-new

The transient execution is never stored as a resumable execution. A
continue-as-new request received between WaitFor and transient completion waits
for the source coroutine to finish the transient Execute. The interpreter then
normalizes and snapshots the source waiting condition. The next run resumes
that condition without rerunning WaitFor or the transient step.
