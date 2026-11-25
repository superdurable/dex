---
section: Changed
---
- **lock_requirement_not_declared** no longer warns when every writer holds one common lock of any Attribute, when the writes are blind, or, in Go, when every **InvokeRPCWithOptions** call in the package locks what the RPC writes.
