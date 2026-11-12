---
section: Changed
---
- **dex-agents-builtin** (Python and TypeScript): plan mode is on, and the
  agent has the `write_todos` built-in tool next to `ask_user`. In the
  terminal UI, `/plan` drafts a plan with `write_todos` only, and `/run`
  approves it and runs it with every tool; the session API works too. The
  plain chat has no plan commands.
- **dex-agents-builtin**: `write_todos` is now a reserved tool name, so a
  connector or local tool with that name fails the turn. Rename the
  conflicting tool, or use another harness.
- Python CLI core, terminal UI: the startup notices of the Worker host, such
  as connector tools that are unavailable, and the lines about a `dexcli dev`
  that the command started, such as its log file, reach the terminal UI as
  launch notices. Before, they were printed only before the terminal UI
  covered the screen, so the user saw them after exiting.
