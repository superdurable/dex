---
section: Added
---
- Python and TypeScript cores: a Connector Mutation whose manifest says **agent.repeatOnUnknown: limited** becomes a **bounded_write** tool. It runs in **CallTool**. When a send's outcome is unknown, the Connector reads back first and resends at most **agent.maxSends** times per call. A definite failure is never resent. A tool policy's **max_sends** (**maxSends**) sets the cap per tool, from 1 to 10. Upgrade every Worker first: an older Worker cannot read a session that recorded a **bounded_write** call. [Reference](/reference/agents/connector-tools#bounded-resends).
- Python and TypeScript cores: a fence marker that carries a send count keeps a later Connector branch **outcome_unknown**, as any claim does. [Reference](/reference/agents/connector-tools#outcomes).
