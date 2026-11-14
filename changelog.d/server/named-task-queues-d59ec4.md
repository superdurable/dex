---
section: Added
---
- Named task queues. A Flow names its task queue in **WorkerTarget.task_queue** when it starts,
  and only pull Workers registered on that task queue receive its Steps and RPCs, through its
  sticky Worker or its Flow type queue inside the task queue. A Worker registers on one task
  queue with **PollWorkerTaskRegister.task_queue**, and the server echoes it in
  **PollWorkerTaskRegistered.task_queue**. Each app Worker serves one task queue. An empty
  name keeps the default task queue, so existing Flows and Workers work as before.
- A SubFlow inherits its parent's task queue. Its override may name another task queue, or
  select the default task queue with **dex.default**.
- The task queue is fixed when a Flow starts. **UpdateFlowConfig** rejects a Worker target that
  names another task queue with INVALID_ARGUMENT.
- **PullRouting.task_queue** records the task queue of each dispatch.
- The server reports protocol version 5.
