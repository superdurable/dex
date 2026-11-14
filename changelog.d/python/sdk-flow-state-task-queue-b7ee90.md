---
section: Added
---
- **FlowInfo.task_queue** names the task queue of the Flow that **Client.describe_flow** and **AsyncClient.describe_flow** describe. An empty string means the default task queue. A Client without a Worker can read it to see which Workers must serve the Flow. **describe_flow** now sends a second request that reads the run's committed state.
