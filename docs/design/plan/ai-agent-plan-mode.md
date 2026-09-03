# AI Agent Plan Mode

The AI Agent example supports a planning-only interaction mode selected for one
user message. Planning produces durable state and never executes MCP tools,
approval-gated operations, or timers.

## Durable state

`AgentPlan` is a regular Attribute containing an ordered list of tasks. Each task
is pending, in progress, or completed. The built-in `write_todos` tool replaces
the complete list atomically and advances a monotonic revision. An empty list
clears the plan.

Plan lifecycle state is separate from task state:

- A draft is ready for user review.
- An active plan has been approved for execution.
- A completed plan has no unfinished tasks.

Returning to the user does not imply plan completion. An active plan may remain
incomplete when the model stops, and the user can explicitly continue it.

## Planning and execution

A Plan mode message forces one `write_todos` call. The model then explains the
draft without receiving any executable tools. Draft discussions also keep
business tools unavailable.

The Execute plan RPC publishes a revision-scoped Channel message. The RPC rejects
stale, duplicate, completed, or non-waiting requests. Execution exposes the
configured tools plus `write_todos`, so the model can keep progress current.
Steered messages take priority at safe boundaries. At the user-input wait,
queued messages take priority over plan execution requests.

The current plan is injected independently of conversation history. Context
compaction may summarize old messages, but it never summarizes or deletes the
current plan. Plan update events are best effort; the Attribute remains the UI's
source of truth.
