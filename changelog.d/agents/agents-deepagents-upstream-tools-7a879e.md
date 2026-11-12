---
section: Breaking
---
- **dex-agents-deepagents**: file writes and shell commands no longer wait for approval by default, as in Deep Agents without **interrupt_on**. Name the tool in **approval.always** to make it wait. **local_tools.shell.allow** has no effect on this command, and its file tools are Deep Agents' own, which take absolute paths. [Reference](/reference/agents/cli#harness-tools).
