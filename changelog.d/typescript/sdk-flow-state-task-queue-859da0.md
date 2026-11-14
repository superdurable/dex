---
section: Added
---
- **FlowState.taskQueue** names the task queue of the Flow that **Client.getFlowState** reads. An empty string means the default task queue. A Client without a Worker can read it to see which Workers must serve the Flow.
