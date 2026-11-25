---
section: Fixed
---
- An RPC whose result publishes to a Channel while a Flow close is pending now fails with **FAILED_PRECONDITION** and **FLOW_CLOSING** and commits nothing. Before, the server stored the message, but no Step could consume it. [Reference](/reference/server/runtime-client#pending-close)
