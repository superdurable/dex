# Parallel SubFlows

The runnable examples cover basic fan-out, long-lived and short-lived parents,
partitioning across parents, and durable back pressure.

- `GET /patterns/parallel-subflows/start/basic?workflowId={workflowId}`
- `GET /patterns/parallel-subflows/start/long-lived-parent?workflowId={workflowId}`
- `GET /patterns/parallel-subflows/start/short-lived-parent?workflowId={workflowId}`
- `GET /patterns/parallel-subflows/start/submit?workflowId={workflowId}`

The Flow and Step definitions are in
[parallel-subflows.ts](./parallel-subflows.ts).
