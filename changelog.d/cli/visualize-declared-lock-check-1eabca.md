---
section: Added
---
- For Go and Python, `dexcli visualize` reports **declared_lock_missing** when a Step method or RPC writes a declared Attribute or publishes to a declared Channel without the required registration lock, and **declared_lock_unresolved** when it cannot prove the lock.
- `dexcli visualize` warns with **lock_requirement_not_declared** when an Attribute without a declared lock requirement may lose an update: an RPC and another handler write it and one writer holds no lock of it, or an RPC reads and writes it without its lock.
