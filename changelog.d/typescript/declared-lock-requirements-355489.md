---
section: Added
---
- **writesRequireLock** and **writesRequireAttributeLock** of **Attribute** and **AttributeMap** declare the lock every write must hold. **publishesRequireAttributeLock** of **Channel** and **ChannelMap** does the same for publishes.
- A waitFor, execute, or RPC handler that writes or publishes without registering the required lock fails with **MissingAttributeLockError**. Only **StepOptions** and **RPCOptions** locks count. [Declared lock requirements](/reference/server/runtime-data#declared-lock-requirements).
