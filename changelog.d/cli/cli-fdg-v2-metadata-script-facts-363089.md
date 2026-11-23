---
section: Changed
---
- Python and TypeScript indexed Attributes require **dex:indexed-attribute** and report **v2_indexed_attribute** when it is missing.
- Python and TypeScript view RPCs report **rpc_lock_unresolved** for unreadable locks, **v2_view_rpc_output** for unreadable output keys, **v2_rpc_signature** for input parameters, and **v2_view_rpc_side_effect** for writes.
- Python and TypeScript sources report the **start_input_unresolved** warning when the Start input type cannot be resolved in the file.
