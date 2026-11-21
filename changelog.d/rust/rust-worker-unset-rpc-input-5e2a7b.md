---
section: Fixed
---
- A Worker serves an RPC bound without input, with **RpcList::function_without_input** or **RpcList::procedure_without_input**, when the request leaves the input unset, as TypeScript Clients before 1.45.8 send it. Before, the Worker failed the call with **Value has no concrete kind**. An RPC bound with an input still needs one; an unset input fails the call with a value mapping error that names the RPC. (#2034)
