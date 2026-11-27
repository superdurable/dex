---
section: Fixed
---
- **dexcli visualize** reports fewer false **locked_method_external_call** warnings. It follows imports from other project files in Python and TypeScript, and resolves Rust Dex SDK names next to other glob imports. [Reference](/reference/web/flow-visualization#locks-shared-with-rpcs)
- **dexcli visualize** checks Python Step methods whose locks come from factory-built Attributes or lock constants. A Python lock registration it cannot resolve now gets the warning **registration_lock_unresolved** instead of being skipped silently.
