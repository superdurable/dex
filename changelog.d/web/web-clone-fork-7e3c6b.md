---
section: Added
---
- The Run page offers **Clone fork…** in the action menu and **Clone fork from here…** in the selected event's panel. The dialog starts a new Flow from a copy of the run's state at a point, with the new Flow ID, task queue, ID reuse policy, SubFlow reset policy, and reason. A run that a clone fork created links to its source run and event. **POST /api/flows/clone-fork** calls **CloneForkFlow**.
- The time travel dialog adds the **Event ID** point and a **SubFlow reset policy** choice. **POST /api/flows/time-travel** accepts **eventId** and **subFlowResetPolicy**.
