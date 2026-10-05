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
Its business deadline is `getFirstAttemptAt() + JOB_WAIT_BUDGET`, so it stays
stable across retries. A retry resumes from the last reported status through
`getLastHeartbeatValue`.

## Endpoints

- `GET /patterns/polling/start?workflowId={workflowId}`
- `GET /patterns/polling/complete-job?workflowId={workflowId}`

See [Polling](https://docs.superdurable.io/design-patterns/polling) for the
pattern rules.
