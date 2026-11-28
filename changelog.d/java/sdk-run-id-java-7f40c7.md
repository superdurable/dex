---
section: Added
---
- **Client.describeFlowRun** and **Client.getFlowRunAttributes** read one exact run of a Flow ID, even when a newer run exists. A closed run returns its final status and Attribute values.
- **Client.waitForFlowRun** waits for one exact run. A run that has already closed returns its result at once.
- **StopFlowOptions.withRunId** stops one exact run. A stop with a run ID never affects a newer run and throws **FlowNotActiveOrNotFoundException** when that run has already closed.
