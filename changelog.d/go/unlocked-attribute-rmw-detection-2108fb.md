---
section: Added
---
- **WritesRequireLock** and **WritesRequireAttributeLock** declare the lock every write of an Attribute or AttributeMap must hold. **PublishesRequireAttributeLock**, a new **ChannelOption** of **DefineChannel** and **DefineChannelMap**, does the same for publishes.
- A WaitFor, Execute, or RPC handler that writes or publishes without registering the required lock fails with **MissingAttributeLockError**. Only **StepOptions** and **RPCOptions** locks count. [Declared lock requirements](/reference/server/runtime-data#declared-lock-requirements).
