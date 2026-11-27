---
section: Fixed
---
- **dexcli visualize** reports fewer false **locked_method_external_call** warnings. Python and TypeScript sources follow helpers, classes, and types imported from other files of the project, parsing them without running them. TypeScript treats Attribute values combined with `??`, `||`, `&&`, or a conditional, and the Step input, as data. Python keeps the methods of Channels held in module tuples or returned by helpers. Rust resolves Dex SDK names through a `dex_sdk` glob import beside other globs, and through a dependency renamed in `Cargo.toml`.
- **dexcli visualize** checks Python Step methods whose locks use an Attribute built by a same-module factory function, or a lock or lock list held in a module constant or a local variable. A Python lock it cannot resolve now gets the warning **registration_lock_unresolved** instead of being skipped silently.
