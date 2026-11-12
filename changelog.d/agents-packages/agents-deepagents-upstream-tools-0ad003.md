---
section: Changed
---
- `dex-agents-deepagents` (Python) keeps Deep Agents' own tools: `ls`,
  `read_file`, `write_file`, `edit_file`, `delete`, `glob`, `grep`, and
  `execute` on the real workspace files through a `LocalShellBackend` rooted at
  the workspace, LangChain's `write_todos`, and the `task` subagent tool. They
  replace the CLI core's file and shell tools; the connector, web fetch, and
  browser tools stay. Connector writes always wait for approval. File tools
  refuse a path whose real path is outside the workspace, and `ls`, `glob`,
  and `grep` leave out such entries, such as links to files elsewhere. `task`
  runs its subagent inside one tool Step, with the configuration file's model
  and no Steps or approvals of its own, so it waits for approval whenever
  `approval.always` names any Deep Agents tool. A connector tool whose name a
  Deep Agents tool has gets the prefix `connector_`, repeated until the name
  is free. Large tool results and long user messages go to a 0700 directory
  per session under `~/.dex/agents/deepagents-artifacts`. `write_todos` returns a graph `Command` that updates the todo
  list for the turn. Deep Agents' summarization stays off.
