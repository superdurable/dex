---
section: Added
---
- Workers warn when **WaitFor**, **Execute**, or an RPC reads and writes an Attribute without a registered lock. [Unlocked read-modify-write check](/reference/server/runtime-data#unlocked-read-modify-write-check).
- **WorkerOptions.FailOnUnlockedAttributeReadModifyWrite** fails such an invocation with **UnlockedAttributeReadModifyWriteError**. Turn it on in tests and local development.
- **Worker.UnlockedAttributeReadModifyWriteCount** counts detections for metrics. **Attribute.DeclareSingleWriter** and **AttributeMap.DeclareSingleWriter** accept an invocation when no other invocation can write the key at the same time.
