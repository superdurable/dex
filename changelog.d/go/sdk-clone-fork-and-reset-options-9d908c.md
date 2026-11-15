---
section: Added
---
- **Client.CloneForkFlow** creates a new Flow whose first run starts from a copy of another Flow run's state at one point, while the source run keeps running. **CloneForkFlowOptions** selects the point as **TimeTravelOptions** does, plus the new **TimeTravelToCurrent**, and sets the request ID, ID reuse policy, already-started options, Flow configuration and task queue, initial Attributes, Flow timeout, reason, and SubFlow reset policy. **CloneForkFlowResult** returns the new run ID and the copied source run, run attempt, and event ID. It needs Server protocol 6, server-zd 0.1.33 or later; against an older Server it returns **CloneForkFlowUnsupportedError**.
- **Client.TimeTravel** returns to the point after one history event with **TimeTravelByEventID** and **TimeTravelOptions.EventID**, and takes a **SubFlowResetPolicy** that decides what happens to the SubFlow conditions of restored Steps.
