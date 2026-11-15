---
section: Added
---
- **Client.clone_fork_flow** and **AsyncClient.clone_fork_flow** create a new Flow whose first run starts from a copy of another Flow run's state. **CloneForkFlowOptions** selects the copied point, by default the current state, and sets the new Flow's request ID, ID reuse policy, Flow configuration, task queue, Attribute writes, Flow timeout, reason, and SubFlow reset policy. **CloneForkFlowResult** returns the new run ID and the copied source run, run attempt, and history event ID. The call requires Dex server-zd 0.1.33 or later; an older Server raises **CloneForkFlowUnsupportedError** before the request is sent.
- **TimeTravelType.EVENT_ID** with **TimeTravelOptions.event_id** time travels to the point right after one history event, and **TimeTravelOptions.sub_flow_reset_policy** takes a **SubFlowResetPolicy** that decides what happens to the SubFlow conditions of restored Steps.
- The SDK supports Dex Server protocol version 6.
