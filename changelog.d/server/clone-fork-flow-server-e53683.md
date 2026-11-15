---
section: Added
---
- **CloneForkFlow** creates a new Flow whose first run starts from a copy of another Flow run's state, at its current state or at a point in its history. The call only reads the source run, and a running source keeps running. The new run copies Attributes, Channel messages, Steps, waits, and every value they hold, and leaves out history. Hard-delete Attributes take the source run's current values, as a time travel keeps them. **id_reuse_policy** decides on the new Flow ID as for StartFlow, **sub_flow_reset_policy** decides the copied SubFlow waits, and the Flow timeout restarts at the clone fork. See [Clone fork](/reference/server/runtime-client#clone-fork).
- The server reports protocol version 6, which adds **CloneForkFlow**.
