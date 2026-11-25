---
section: Added
---
- **writesRequireLock**, **writesRequireAttributeLock**, and **publishesRequireAttributeLock** declare the lock every write or publish needs. A Step method or RPC that writes or publishes without registering it fails with **MissingAttributeLockException**.
