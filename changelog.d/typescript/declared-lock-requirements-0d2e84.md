---
section: Changed
---
- In a Flow that declares a lock requirement, Registry reads each Step's options once and copies its default lock fields and each RPC's **lockAttributes**. Default Step options and RPC calls send those copies, which the Worker checks. Explicit movement and failure options replace them as before.
