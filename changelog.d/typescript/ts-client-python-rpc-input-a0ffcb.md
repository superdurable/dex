---
section: Fixed
---
- **Client.invokeRPC** and **Client.invokeRPCWithOptions** send a null input for an RPC declared without input, as the Go, Java, Python, and Rust Clients do, instead of leaving it unset. Python and Java Workers before 1.45.8, and Rust Workers, fail such a call with **Value has no concrete kind**. (#2034)
