---
section: Added
---
- Workers report a WaitFor, Execute, or RPC invocation that reads an Attribute and then writes it without a registered lock. By default the Worker logs one warning per Flow type, handler, and Attribute, and the invocation succeeds unchanged. [Unlocked read-modify-write check](/reference/server/runtime-data#unlocked-read-modify-write-check).
- **WorkerOptions.FailOnUnlockedAttributeReadModifyWrite** fails such an invocation with **UnlockedAttributeReadModifyWriteError**. Turn it on in tests and local development.
- **Worker.UnlockedAttributeReadModifyWriteCount** counts detections for metrics. **Attribute.DeclareSingleWriter** and **AttributeMap.DeclareSingleWriter** accept a handler that is the only writer.
