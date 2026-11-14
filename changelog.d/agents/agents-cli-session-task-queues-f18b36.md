---
section: Added
---
- Python and TypeScript CLI commands: a session continues in any environment
  that runs the same command. Each new session has the harness's Flow type and
  its own task queue, which only the command that has the session open pulls.
  **--resume** from another host, user, configuration file, or workspace takes
  the session over: its next model and tool calls run there, and the command
  prints one line that names both places. A turn still running in the other
  place is cancelled first, so none of its calls run in the new workspace.
  **sessions** lists the sessions of every environment, with where each was
  last open, stored in the session's **SessionLastTerminalEnvironment**
  Attribute. Sessions that earlier releases started keep resuming only in their
  own environment. The commands need server-zd 0.1.30, which dexcli 1.47.5
  runs. [Reference](/reference/agents/cli#other-environments).
