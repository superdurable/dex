---
section: Changed
---
- Dex Agents CLI commands: when a shell command ends, what it left running in its process group, such as **sleep 30 &**, gets SIGTERM and SIGKILL 0.5 seconds later. A process that left the group, for example with **setsid**, is not stopped; once the group is cleaned, the result waits at most 1 more second for its output. The result starts with **exit code N** (128 plus the signal's number for a signal) or **killed: timed out after N seconds**, then has a **--- stdout ---** and a **--- stderr ---** section, each cut at 64 KiB. [Workspace and local tools](/reference/agents/cli#local-tools).
