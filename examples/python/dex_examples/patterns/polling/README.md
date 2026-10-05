# Polling

**PollingFlow** waits for an external job. **StartJob** starts the job with the
Flow ID as its idempotent job ID. **AwaitJob** is one long-running Step that
polls the job status with `await asyncio.sleep(...)` between calls. It writes a
**JobProgress** Stream message when the status changes, records a heartbeat
otherwise, and completes with the job result.

**FakeJobService** stands in for the external job API. A job reports `QUEUED`,
then `RUNNING`, and succeeds six seconds after it starts or as soon as
`complete-job` is called.

**AwaitJob** keeps `POLL_INTERVAL + JOB_CALL_TIMEOUT <= heartbeat_timeout - 10s`.
`MAX_JOB_WAIT` sets both `execute_method_timeout` and the retry policy's
`total_duration`. Dex enforces the maximum wait, so the Step code has no
deadline. The heartbeat checkpoint is the last reported status, which a retry
reads back with `get_last_heartbeat_value`.

When the wait expires or the retries are exhausted,
`on_execute_failure_proceed_to` moves to **RecordJobWaitFailure**. That Step
reads `context.recovery_error` and fails the Flow with the error type and
detail.

## Endpoints

- `GET /patterns/polling/start?workflowId={workflowId}`
- `GET /patterns/polling/complete-job?workflowId={workflowId}`

See [Polling](https://docs.superdurable.io/design-patterns/polling) for the
pattern rules.
