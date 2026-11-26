---
section: Added
---
- **dexcli visualize** reports the experimental warning **locked_method_external_call** for Go, TypeScript, Python, Java, and Rust sources. It flags a **WaitFor** or **Execute** that registers a lock an RPC of the same Flow also registers, and may call an external system. An allowlist decides what counts as an external call. See [Locks shared with RPCs](/reference/web/flow-visualization#locks-shared-with-rpcs).
