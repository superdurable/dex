---
section: Breaking
---
- **dex-agents-pi**: file writes, edits, and **bash** no longer wait for approval by default, as in Pi. Name the tool in **approval.always** to make it wait. **local_tools.shell.allow** has no effect on this command. The file tools and **bash** have no workspace boundary. Without **workspace** in the file, the workspace is the directory the command starts in. A **bash** command has no default timeout unless **pi.bash_timeout_seconds** sets one. [Reference](/reference/agents/cli#harness-tools).
