---
section: Fixed
---
- The shell tool of the Python CLI commands (`dex-agents-cli-core`) now stops
  what a command leaves running in its process group, such as `sleep 30 &`,
  when the command ends: SIGTERM, then SIGKILL 0.5 s later. Before, those
  processes ran on after the plain chat's tool call returned, and one holding
  the output kept the call waiting until it exited.
- A process that left the command's group, for example with `setsid`, and
  still holds its output no longer delays the result by more than 1 s after
  the command ends. It is not stopped.
