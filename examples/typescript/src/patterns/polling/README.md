# Polling

**PollingFlow** waits for an external job. **StartJob** starts the job with the
Flow ID as its idempotent job ID. **AwaitJob** is one long-running Step that
polls the job status with `setTimeout` from `node:timers/promises` between
calls. It writes a **JobProgress** Stream frame when the status changes,
records a heartbeat otherwise, and completes with the job result.

**FakeJobService** stands in for the external job API. A job reports `QUEUED`,
then `RUNNING`, and succeeds six seconds after it starts or as soon as
`complete-job` is called.

**AwaitJob** keeps `POLL_INTERVAL_MS + JOB_CALL_TIMEOUT_MS <= heartbeatTimeoutMs - 10s`.
Each status call has its own `AbortSignal.timeout`. Its business deadline is
`context.firstAttemptAt + JOB_WAIT_BUDGET_MS`, so it stays stable across
retries. A retry resumes from the last reported status through
`getLastHeartbeatValue`.

## Endpoints

- `GET /patterns/polling/start?workflowId={workflowId}`
- `GET /patterns/polling/complete-job?workflowId={workflowId}`

See [Polling](https://docs.superdurable.io/design-patterns/polling) for the
pattern rules.
