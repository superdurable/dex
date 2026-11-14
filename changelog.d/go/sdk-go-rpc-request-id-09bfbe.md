---
section: Added
---
- **RPCInvokeOptions.RequestID** sets the request ID of **Client.InvokeRPCWithOptions**. Pass the same ID on every attempt, such as the ID of an event that is delivered at least once, and Dex Server applies the RPC's Flow changes at most once. A transactional or locking RPC answers a repeat with the recorded reply without running its handler again, and a different request under the same ID fails with **ServiceError** and INVALID_ARGUMENT. Any other RPC runs its handler again on a repeat; when that attempt changes no Flow state, it returns the new output and keeps no request ID. Nil keeps the SDK-generated request ID. [API reference](pathname:///api/go/latest/dex/#type-rpcinvokeoptions).
