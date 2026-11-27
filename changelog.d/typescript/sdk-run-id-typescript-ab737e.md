---
section: Added
---
- **Client.describeFlow** takes an optional `runId`, and the new **Client.getFlowRunAttributes** reads Attributes of one exact run of a Flow ID, even when a newer run exists. A closed run returns its final status and Attribute values.
- **Client.waitForFlow** takes an optional `runId`, and **StopFlowOptions.runId** targets one exact run. A wait on a closed run returns its result at once. A stop with a run ID never affects a newer run and rejects with **FlowNotActiveOrNotFoundError** when that run has already closed.
