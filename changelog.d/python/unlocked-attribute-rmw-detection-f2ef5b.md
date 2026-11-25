---
section: Added
---
- **writes_require_lock** of **Attribute** and **AttributeMap** declares the lock every write must hold. **publishes_require_lock** of **Channel** and **ChannelMap** does the same for publishes.
- A wait_for, execute, or RPC handler that writes or publishes without registering the required lock raises **MissingAttributeLockError**. Only **StepOptions** and **@rpc** locks count. [Declared lock requirements](/reference/server/runtime-data#declared-lock-requirements).
