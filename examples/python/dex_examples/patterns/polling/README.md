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
The Python SDK has no first-attempt time, so the first attempt computes the
business deadline from `JOB_WAIT_BUDGET`. The heartbeat checkpoint carries that
deadline and the last reported status, so a retry keeps the deadline and resumes
through `get_last_heartbeat_value`.

## Endpoints

- `GET /patterns/polling/start?workflowId={workflowId}`
- `GET /patterns/polling/complete-job?workflowId={workflowId}`

See [Polling](https://docs.superdurable.io/design-patterns/polling) for the
pattern rules.
