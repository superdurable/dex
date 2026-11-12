---
section: Changed
---
- Dex Agents CLI commands: when a shell command ends, what it left running in its process group, such as **sleep 30 &**, gets SIGTERM and SIGKILL 0.5 seconds later. A process that left the group, for example with **setsid**, is not stopped and delays the result by at most 1 second. The exit code is the shell's own number. [Workspace and local tools](/reference/agents/cli#local-tools).
