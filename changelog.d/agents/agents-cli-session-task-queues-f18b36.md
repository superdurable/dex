---
section: Added
---
- Python and TypeScript CLI commands: a session continues in any environment
  that runs the same command. Each new session has the harness's Flow type and
  its own task queue, which only the command that has the session open pulls.
  **--resume** from another host, user, configuration file, or workspace takes
  the session over: its next model and tool calls run there, and the command
  prints one line that names both environments. The calls of a turn still
  running in the other environment are refused there, and the turn is
  cancelled; the command says whether it could confirm that. **sessions** lists
  the sessions of every environment, with where each was last open, stored in
  the session's **SessionLastTerminalEnvironment** Attribute. Sessions that
  earlier releases started keep resuming only in their own environment. The
  commands need server-zd 0.1.30, which dexcli 1.47.5 runs.
  [Reference](/reference/agents/cli#other-environments).
- Python and TypeScript CLI commands: a command that cannot renew its session
  lease stops its chat with one line and exit code 1 before the lease expires,
  so another terminal that then takes the session never runs it at the same
  time. [Reference](/reference/agents/cli#one-terminal-per-session).
