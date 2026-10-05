# Polling

**PollingFlow** waits for an external job. **StartJob** starts the job with the
Flow ID as its idempotent job ID. **AwaitJob** is one long-running Step that
polls the job status with `Thread.sleep` between calls. It writes a
**JobProgress** Stream frame when the status changes, records a heartbeat
otherwise, and completes with the job result.

**FakeJobService** stands in for the external job API. A job reports `QUEUED`,
then `RUNNING`, and succeeds six seconds after it starts or as soon as
`complete-job` is called.

**AwaitJob** keeps `POLL_INTERVAL + JOB_CALL_TIMEOUT <= HeartbeatTimeout - 10s`.
`MAX_JOB_WAIT` sets both `executeMethodTimeout` and the retry policy's
`totalDuration`. Dex enforces the maximum wait, so the Step code has no
deadline. A retry resumes from the last reported status through
`getLastHeartbeatValue`.

When the wait expires or the retries are exhausted, `onExecuteFailureProceedTo`
moves to **RecordJobWaitFailure**. That Step reads `getRecoveryError()` and
fails the Flow with the error type and detail.

## Endpoints

- `GET /patterns/polling/start?workflowId={workflowId}`
- `GET /patterns/polling/complete-job?workflowId={workflowId}`

See [Polling](https://docs.superdurable.io/design-patterns/polling) for the
pattern rules.
