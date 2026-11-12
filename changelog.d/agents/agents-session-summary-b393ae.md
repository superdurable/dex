---
section: Added
---
- Python and TypeScript cores: with the new **summary_attributes_indexed** (**summaryAttributesIndexed**) option, a session keeps its title, the time of its latest turn, and its turn count in three indexed Attributes. It is off by default, because the title is the user's first message and the index keys share the namespace's index capacity. **list_agent_sessions** and **listAgentSessions** read a page of sessions with them, and an optional title search, without a snapshot per session and without a Worker. [Reference](/reference/agents/approvals#list-sessions).
- The CLI commands turn the session summary on. Their **sessions** subcommand sorts up to five pages of 50 sessions by last activity, says when more exist, and shows the time of the latest turn, the turn count, and the title. [Reference](/reference/agents/cli#resume).
