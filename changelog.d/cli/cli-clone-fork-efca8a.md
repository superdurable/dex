---
section: Added
---
- **dexcli flow clone-fork SOURCE_FLOW_ID --flow-id NEW_FLOW_ID** creates a new Flow from a point of another Flow run through **CloneForkFlow**. **--type** selects the point: **current** (the default), **beginning**, **history-event-time**, **step-type**, **step-execution-id**, or **event-id**. Other flags set the source run, run attempt, task queue, Flow configuration override, initial Attributes, Flow timeout, ID reuse policy, SubFlow reset policy, request ID, and reason.
- **dexcli flow time-travel** accepts **--type event-id** with the history event ID as **--target**, **--run-attempt** to search an earlier run attempt, and **--sub-flow-reset-policy** to decide what happens to restored SubFlow waits. Its output adds **runAttempt**.
