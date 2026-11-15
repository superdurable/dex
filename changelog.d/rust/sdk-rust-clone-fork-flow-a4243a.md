---
section: Added
---
- **Client::clone_fork_flow** creates a new Flow from a copy of another Flow run's state at one point, while the source run keeps running. **CloneForkFlowOptions** selects the current state, the beginning, a history event time, a Step type, a Step execution, or a history event ID, and sets the source run, request ID, ID reuse policy, Flow configuration, task queue, initial Attributes, Flow timeout, reason, and SubFlow reset policy. **CloneForkFlowResult** returns the new run ID and the copied source run, run attempt, and event ID. The call requires server-zd 0.1.33 or later; with an older Server it fails before sending the request.
- **TimeTravelOptions::from_event_id** time-travels to the point right after one history event, and **TimeTravelOptions::sub_flow_reset_policy** chooses a **SubFlowResetPolicy** for the SubFlow conditions that restored Steps wait on.
- The Client negotiates Server protocol 6.
