---
section: Fixed
---
- A Worker serves an RPC declared without input when the request leaves the input unset, as TypeScript Clients before 1.45.8 send it. Before, the Worker failed the call with **Value has no concrete kind**. An RPC declared with an input still needs one; an unset input fails the call with **ValueMappingException**. (#2034)
