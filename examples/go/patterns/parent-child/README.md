# Parent Child Design Pattern (Option 2)

## Endpoints

- `GET /patterns/parent-child/start?workflowId={workflowId}&numRequests={n}`

Concurrency per parent: 3

The parent ignores `FlowAlreadyStartedError` when a child already exists and
handles `LongPollTimeoutError` when a child remains active beyond the selected
wait duration.
