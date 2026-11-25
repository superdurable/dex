---
section: Fixed
---
- An RPC whose result publishes to a Channel while a Flow closes now fails with **FAILED_PRECONDITION** and **FLOW_CLOSING** and commits nothing. Before, it succeeded and the closed Flow dropped the message. [Reference](/reference/server/runtime-client#requests-during-flow-close)
