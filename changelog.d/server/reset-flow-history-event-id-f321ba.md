---
section: Added
---
- **ResetFlow** (Time Travel) can select its target by event ID: set **reset_type** to **FLOW_RESET_TYPE_EVENT_ID** and **event_id** to the **event_id** of an event that **GetHistoryEvents** returns. The run travels to the first point at or after that event where Time Travel can stop, as with a history event time. An event outside the selected **run_attempt**'s history or outside the window fails with **FAILED_PRECONDITION**. [Reference](/reference/server/runtime-client#time-travel).
