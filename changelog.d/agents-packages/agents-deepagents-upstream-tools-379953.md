---
section: Breaking
---
- `dex-agents-deepagents` (Python): file writes and shell commands no longer
  wait for approval by default, as in Deep Agents without `interrupt_on`.
  Name `write_file`, `edit_file`, `delete`, `execute`, or `task` in
  `approval.always` to make them wait. `local_tools.shell.allow` has no effect
  on this command. The tool names are Deep Agents' own, so `read_file`,
  `write_file`, and `edit_file` take `file_path` with an absolute path, and the
  CLI core's `list_directory`, `search_files`, and `run_shell_command` are gone
  from this command; a resumed session replays its earlier calls of them
  unchanged.
- `dex_agents_deepagents_cli`: `build_deep_agent` takes the workspace as a
  second argument, and `write_todos_tool` is removed; `write_todos` comes from
  LangChain's `TodoListMiddleware`.
