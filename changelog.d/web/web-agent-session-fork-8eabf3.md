---
section: Added
---
- The **Conversation** tab forks an agent session. **Fork session** copies the session right after its latest turn ended. **Fork from this message** on the message that ended a turn copies the session right after that turn. A fork of a session on its own task queue, as an Agents CLI command starts it, gets its own task queue, derived from its session ID as the Agents CLI does; a fork of any other session inherits the source's task queue. The new session starts with no terminal lease, so an Agents CLI command can open it. The source session does not change. [Reference](/reference/web/dex-web#agent-session-fork).
- **POST /api/flows/clone-fork** takes **deleteAttributeKeys**, the Attributes the new run deletes after the copy. [Reference](/reference/web/dex-web#clone-fork).
