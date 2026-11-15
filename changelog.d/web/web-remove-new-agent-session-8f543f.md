---
section: Breaking
---
- Dex Web no longer starts Dex Agents sessions. The **New session** button and dialog are gone from the **Conversation** tab and from **Work Queue**, and **POST /api/v2/agent-sessions/start** is removed. A session started there ran on the default task queue, so no Agents CLI command ran it. Start a session with an Agents CLI command, or with the application's Client, such as **AgentSessionClient.start_session** or **startAgentSession**. Dex Web still shows the session, sends it messages, and edits its settings. [Reference](/reference/web/dex-web#agent-new-session).
