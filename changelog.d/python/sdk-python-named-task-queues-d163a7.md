---
section: Added
---
- Named task queues. `PullWorkerOptions.task_queue` makes a Worker poll one named task queue,
  and `StartFlowOptions.task_queue` starts a Flow on one. Only Workers on that task queue
  receive the Flow's Steps and RPCs. `SubFlowOptions.task_queue` moves a SubFlow to another
  task queue, or to the default one with `dex.default`; empty inherits the parent's task queue.
  Empty everywhere keeps the default task queue, so existing code works as before.
- A named task queue needs Dex Server protocol version 5. Against an older Server, the
  Worker's `start` raises `RuntimeError` before it opens task streams, and `start_flow`
  raises it before it sends the request. A
  stream that the Server registers under another task queue is closed and reported as a
  permanent rejection through `PullRegistrationError` and `pull_registration_state`, whose
  entries and errors now carry `task_queue`.
