---
section: Added
---
- **Client.cloneForkFlow** creates a new Flow from a copy of another Flow run's state at one point, while the source run keeps running. **CloneForkFlowOptions** selects the point with **TimeTravelType**, including the new **CURRENT** default, and sets the request ID, ID reuse policy, already-started handling, configuration override, task queue, initial Attributes, Flow timeout, reason, and SubFlow reset policy. **CloneForkFlowResult** returns the new run ID and the source run ID, run attempt, and event ID of the copied point. It requires Server protocol version 6, which server-zd 0.1.33 provides.
- **Client.timeTravel** accepts **TimeTravelType.EVENT_ID** with **TimeTravelOptions.eventId**, and **TimeTravelOptions.subFlowResetPolicy** with the new **SubFlowResetPolicy** values decides what happens to SubFlow conditions that restored Steps wait on.
- The SDK now negotiates Server protocol version 6.
