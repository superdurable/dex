---
section: Added
---
- Python and TypeScript CLI commands: **--resume** continues a session that
  another host, user, configuration file, or workspace started, as long as the
  same harness started it. The session keeps its Flow type, and the command's
  Worker polls that Flow type instead of its own. The command waits for the
  session's terminal lease as before, prints one line that names both
  execution environments and its workspace, and cancels a turn in progress.
  From then on the session's model and tool calls run in this environment,
  with its configuration file, credentials, and workspace. Every model and
  tool call first checks the session's lease: a Worker outside the
  environment that holds the session refuses the call, which records
  **failed** and does not run. So nothing approved in one environment runs in
  another. Close the commands of the session's own environment while you work
  on it elsewhere: Dex may hand them its calls, and they refuse them. In the
  terminal UI, such a session offers no **/new**. **--resume** of another
  harness's session still exits with code 2.
  [Reference](/reference/agents/cli#other-environments).
