# Parent Child Design Pattern (Option 2)

## Endpoints

- `GET /design-pattern/parentchild/start?workflowId={workflowId}&numRequests={n}`

Concurrency per parent: 3

The parent ignores `FlowAlreadyStartedError` when a child already exists and
handles `LongPollTimeoutError` when a child remains active beyond the selected
wait duration.
