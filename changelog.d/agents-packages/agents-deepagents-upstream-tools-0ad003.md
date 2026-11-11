---
section: Changed
---
- `dex-agents-deepagents` (Python) keeps Deep Agents' own tools: `ls`,
  `read_file`, `write_file`, `edit_file`, `delete`, `glob`, `grep`, and
  `execute` on the real workspace files through a `LocalShellBackend` rooted at
  the workspace, LangChain's `write_todos`, and the `task` subagent tool. They
  replace the CLI core's file and shell tools; the connector, web fetch, and
  browser tools stay. Connector writes always wait for approval. File tools
  refuse a path outside the workspace. `task` runs its subagent inside one tool
  Step, with the configuration file's model and no Steps or approvals of its
  own. A connector tool whose name a Deep Agents tool has gets the prefix
  `connector_`. `write_todos` returns a graph `Command` that updates the todo
  list for the turn. Deep Agents' summarization stays off.
