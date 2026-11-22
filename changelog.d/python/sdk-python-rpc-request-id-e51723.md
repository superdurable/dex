---
section: Added
---
- **RPCInvokeOptions.request_id** sets the request ID of one **invoke_rpc** call. Pass the same ID on every attempt, such as the ID of an event delivered at least once, and Dex Server applies the RPC's Flow changes at most once. [Reference](/reference/server/runtime-data#rpc-request-ids).
- A repeat of a transactional or locking RPC returns the recorded reply without running its handler again. **None**, the default, keeps the generated request ID. Dex Server 0.1.36 and earlier reject a different request under one ID with **DexServiceError** INVALID_ARGUMENT.
