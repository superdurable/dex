---
section: Added
---
- **Client.DescribeFlowRun** and **Client.GetFlowRunAttributes** read one exact run of a Flow ID, even when a newer run exists. A closed run returns its final status and Attribute values.
- **WaitForFlowOptions.RunID**, **StopOptions.RunID**, and **RPCInvokeOptions.RunID** target one exact run. A wait on a closed run returns its result at once. A stop with a run ID never affects a newer run and returns **FlowNotActiveOrNotFoundError** when that run has already closed.
