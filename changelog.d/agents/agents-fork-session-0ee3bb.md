---
section: Added
---
- Python and TypeScript cores and CLI commands fork a session: a new session
  starts from another session's conversation between two turns, and the
  source keeps running. The commands run **fork SESSION_ID**, with **--at N**
  to fork after message **N**, and the plain chat runs **/fork**; both offer
  to open the new session. Forks need server-zd 0.1.33 or later, which
  **dexcli** 1.47.8 runs. [Reference](/reference/agents/cli#fork).
