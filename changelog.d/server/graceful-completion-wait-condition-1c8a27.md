---
section: Added
---
- **WaitingCondition.graceful_completion_conditions** adds a wait condition that completes once the Flow has a graceful completion request, from a **GracefulComplete** Step decision or **StopFlow** with **STOP_TYPE_GRACEFUL_COMPLETE**. A request made before the wait starts completes it at once, and a new request wakes only the Steps whose wait includes it. It works with every wait type; in an any-of wait it comes after Timer, Channel, and SubFlow conditions. **ConditionResults.graceful_completion_results** reports its status to **Execute**, and **WaitingConditionState.graceful_completion_conditions** shows it in the Flow state. A wait holds at most one. [Reference](/reference/server/step-and-flow-close#graceful-completion-condition).
- The server reports protocol version 7, which adds the graceful completion wait condition. Before you roll back to an older server, stop the Flows whose Steps wait on it. [Reference](/reference/server/upgrades).
