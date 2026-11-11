# Parallel Steps

Four runnable Flow examples cover the common parallel Step shapes:

- `StaticParallelStepsFlow` starts `WorkAStep` and `WorkBStep`.
- `DynamicParallelStepsFlow` creates N `DoWorkStep` executions at runtime.
- `AwaitParallelStepsFlow` forks the workers with a Join and waits for every worker's result.
- `FirstWinParallelStepsFlow` keeps the first result and cancels sibling workers.

The dynamic workers use short random delays so completion order, waiting, and
first-win cancellation are visible.

HTTP routes:

- `GET /patterns/parallel/start/static?workflowId=...`
- `GET /patterns/parallel/start/dynamic?workflowId=...`
- `GET /patterns/parallel/start/await?workflowId=...`
- `GET /patterns/parallel/start/first-win?workflowId=...`
