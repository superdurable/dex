# Polling

**PollingFlow** waits for an external job. **StartJob** starts the job with the
Flow ID as its idempotent job ID. **AwaitJob** is one long-running Step that
polls the job status with `time.Sleep` between calls. It writes a
**JobProgress** Stream frame when the status changes, records a heartbeat
otherwise, and completes with the job result.

**FakeJobService** stands in for the external job API. A job reports `QUEUED`,
then `RUNNING`, and succeeds six seconds after it starts or as soon as
`complete-job` is called.

**AwaitJob** keeps `pollInterval + jobCallTimeout <= HeartbeatTimeout - 10s`.
`maxJobWait` sets both `ExecuteMethodTimeout` and `ExecuteRetry.TotalDuration`.
Dex enforces the maximum wait, so the Step code has no deadline. A retry resumes
from the last reported status through `GetLastHeartbeatValue`.

When the wait expires or the retries are exhausted, `ExecuteFailure` moves to
**RecordJobWaitFailure**. That Step reads `RecoveryError` and fails the Flow
with the error type and detail.

## Endpoints

- `GET /patterns/polling/start?workflowId={workflowId}`
- `GET /patterns/polling/complete-job?workflowId={workflowId}`

See [Polling with Long-Running Step Heartbeat](https://docs.superdurable.io/design-patterns/durable-timer/polling-with-long-running-step-heartbeat)
for the pattern rules. [Polling with WaitFor Timer](../polling-with-timer)
waits for the same job with one Step execution per round.
