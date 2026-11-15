---
section: Added
---
- **ResetFlow** takes a **sub_flow_reset_policy**. It decides what a Time Travel does with each SubFlow condition that a restored Step waits on: attach it to its child, restart the child, fail the condition with the new **FLOW_ERROR_TYPE_SUB_FLOW_ABANDONED** error type, or reject the Time Travel. The default is **SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART**. An attached condition whose child ended completes with that child's result, even when the parent had not received it yet. See [SubFlows across a time travel](/reference/server/runtime-client#sub-flows-across-a-time-travel).
