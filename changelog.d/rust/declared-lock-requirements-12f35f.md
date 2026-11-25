---
section: Added
---
- Attributes and Channels can declare the lock every write or publish requires. A Step or RPC that writes or publishes without registering it fails with **MissingAttributeLockError**. [Declared lock requirements](https://docs.superdurable.io/reference/server/runtime-data#declared-lock-requirements)
