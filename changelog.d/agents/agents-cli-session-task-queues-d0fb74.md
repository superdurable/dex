---
section: Changed
---
- Python and TypeScript CLI commands: a session runs only while a command has
  it open. A message that Dex Web sends to a session that no command has open
  waits for a command to open it, within the server's RPC dispatch wait, and
  then fails. Before, any command of the same environment ran it.
  [Reference](/reference/agents/cli#other-environments).
- Python and TypeScript CLI commands: after **/sessions** or **/new** in the
  terminal UI, the reopened terminal UI starts with the new session's history.
  With a **dexcli** earlier than 1.47.2, which switches sessions in place, the
  commands use the plain chat.
  [Reference](/reference/agents/cli#one-terminal-per-session).
