---
section: Added
---
- Python and TypeScript cores: each session keeps its title, the time of its latest turn, and its turn count in three indexed Attributes, **AgentSessionTitle**, **AgentSessionLastActivityAt**, and **AgentSessionTurnCount**. **list_agent_sessions** and **listAgentSessions** read a page of sessions with them, and an optional title search, without a snapshot per session and without a Worker. A session that an earlier release started has no title. [Reference](/reference/agents/approvals#list-sessions).
- The **sessions** subcommand of the CLI commands sorts its page of up to 50 sessions by last activity and shows the time of the latest turn, the turn count, and the title. [Reference](/reference/agents/cli#resume).
