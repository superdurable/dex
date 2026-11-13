---
section: Added
---
- Python and TypeScript CLI commands: a session is open in at most one command
  at a time. A command that opens a session another command has open, with
  **--resume**, **/sessions**, or **/new**, waits, and takes the session over
  as soon as the other command exits. While it waits it shows one status line
  that updates in place, with who has the session open, how long it has
  waited, and Ctrl-C to quit. A killed or paused command frees the session
  after 15 seconds; a paused command that continues after another took its
  session stops its chat with one line. In the terminal UI, **/sessions** and
  **/new** wait the same way with dexcli 1.47.2. Commands of earlier releases
  take no lease. [Reference](/reference/agents/cli#one-terminal-per-session).
  Since 0.6.3.
- Python and TypeScript cores: the session Flow has the
  **acquire_session_terminal_lease** and **release_session_terminal_lease**
  RPCs and the **SessionTerminalLease** Attribute, which mark the terminal that
  has the session open. Since 0.6.3.
