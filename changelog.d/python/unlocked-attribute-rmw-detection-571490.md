---
section: Added
---
- Workers report a `wait_for`, `execute`, or RPC invocation that reads an Attribute and then writes it without a registered lock. By default the Worker logs one WARNING per Flow type, handler, and Attribute, and the invocation succeeds unchanged. [Unlocked read-modify-write check](/reference/server/runtime-data#unlocked-read-modify-write-check).
- **WorkerOptions.fail_on_unlocked_attribute_read_modify_write** fails such an invocation with **UnlockedAttributeReadModifyWriteError**. Turn it on in tests and local development.
- **Worker.unlocked_attribute_read_modify_write_count** and **AsyncWorker.unlocked_attribute_read_modify_write_count** count detections for metrics. **Attribute.declare_single_writer** and **AttributeMap.declare_single_writer** accept a handler that is the only writer.
