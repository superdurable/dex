---
section: Added
---
- **dex-agents** and **@superdurable/dex-agents**: **fork_agent_session**
  (**forkAgentSession**) and **AgentSessionClient.fork_session** create a new
  session from another session's conversation between two turns, with Dex
  clone fork. Every fork copies the state at the start of one
  **AwaitUserMessage** WaitFor: the one waiting now, or, for
  **after_message** (**afterMessage**), the one that started the turn after
  that message, found from its **turn_started** event. The fork deletes
  **SessionTerminalLease** and **SessionLastTerminalEnvironment** in the same
  change, never reuses a Flow ID, and takes an optional task queue. A point
  inside a turn, during a running turn, before a steered turn, or beyond the
  retained events raises **AgentSessionForkError** with a code and one
  sentence. After the copy the fork must show no running turn and exactly the
  kept messages; otherwise it is stopped. **AgentSessionForkingFlow**
  (**createAgentSessionForkingFlow**) lets a Client fork sessions without
  building the agent. The rules are in **agents/conformance/session_fork.json**.
- Python and TypeScript CLI commands: **fork SESSION_ID [--at N]** and the
  plain chat's **/fork [N]** fork a session into a new session ID with its own
  task queue, then offer to open it. A session from before 0.6.8 keeps its
  task queue in the fork. A server without clone fork stops the fork with one
  line that names **dexcli** 1.47.8.
