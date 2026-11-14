---
section: Added
---
- **RPCInvokeOptions.requestId** sets the request ID of one **Client.invokeRPCWithOptions** call. Pass the same ID on every attempt, such as the ID of an event that is delivered at least once, and Dex Server applies the RPC's Flow changes at most once and returns the recorded output to a repeat. A transactional or locking RPC answers a repeat without running its handler again. An RPC that is neither transactional nor locking and changes no Flow state keeps no request ID and runs again. Reusing an ID for a different request fails with **DexServiceError** INVALID_ARGUMENT. Without the option, each call still sends a new random request ID. [Reference](/reference/server/runtime-data#rpc-request-ids).
