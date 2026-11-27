---
section: Added
---
- **Client::describe_flow_run** and **Client::get_flow_run_attributes** read one exact run of a Flow ID, even when a newer run exists. A closed run returns its final status and Attribute values.
- **Client::wait_for_flow_with_options** with **WaitForFlowOptions::run_id** waits for one exact run; a closed run returns its result at once. **WaitForFlowOptions::timeout** bounds the wait.
- **StopFlowOptions::run_id** and **RpcInvokeOptions::run_id** target one exact run. A stop with a run ID never affects a newer run and returns **SdkError::FlowNotActiveOrNotFound** when that run has already closed.
