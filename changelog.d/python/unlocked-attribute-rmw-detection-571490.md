---
section: Added
---
- Workers warn when **wait_for**, **execute**, or an RPC reads and writes an Attribute without a registered lock. [Unlocked read-modify-write check](/reference/server/runtime-data#unlocked-read-modify-write-check).
- **WorkerOptions.fail_on_unlocked_attribute_read_modify_write** fails such an invocation with **UnlockedAttributeReadModifyWriteError**. Turn it on in tests and local development.
- **Worker.unlocked_attribute_read_modify_write_count** and the same **AsyncWorker** property count detections for metrics.
- **Attribute.declare_single_writer** and **AttributeMap.declare_single_writer** accept an invocation when no other invocation can write the key at the same time.
