---
section: Added
---
- **Client.cloneForkFlow** creates a new Flow whose first run starts from a copy of another Flow run's state, at the current point or at a time travel point, while the source run keeps running. **CloneForkFlowOptions** selects the point, the source run, the new Flow's ID reuse policy, request ID, configuration, task queue, Attributes, Flow timeout, reason, and SubFlow reset policy; **CloneForkFlowResult** returns the new run ID and the copied source point. The call requires Dex server-zd 0.1.33 or later; against an older Server it throws **IllegalStateException** before sending the request.
- **TimeTravelType.EVENT_ID** with **TimeTravelOptions.Builder.eventId** returns a Flow to the point right after a history event, and **TimeTravelOptions.Builder.subFlowResetPolicy** takes a **SubFlowResetPolicy** that decides what happens to SubFlow conditions that restored Steps wait on. **TimeTravelType.CURRENT** selects the current point for **cloneForkFlow** only.
