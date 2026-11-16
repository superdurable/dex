---
section: Added
---
- **StopFlow** accepts **STOP_TYPE_GRACEFUL_COMPLETE** and **STOP_TYPE_FORCE_COMPLETE**, which complete a Flow from the client API with the same semantics as the **GracefulComplete** and **ForceComplete** Step decisions. The new optional **StopFlowRequest.output** becomes a Flow result whose Step type and Step execution ID are empty. A complete stop during a pending close returns **FAILED_PRECONDITION** with **FLOW_CLOSING**, and an output with any other stop type returns **INVALID_ARGUMENT**. An output that grows the active run state past its maximum returns **RESOURCE_EXHAUSTED**.
