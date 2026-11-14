---
section: Added
---
- **RPCInvokeOptions.RequestID** sets the request ID of **Client.InvokeRPCWithOptions**. A retry with the same request ID on the same Flow run returns the recorded reply, so Dex applies the RPC's Flow state changes at most once, for example when an event that is delivered at least once invokes a transactional RPC. A different request under a recorded request ID fails with **ServiceError** and INVALID_ARGUMENT. Nil keeps the SDK-generated request ID. [API reference](pathname:///api/go/latest/dex/#type-rpcinvokeoptions).
