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
- The Python shell tool's result now has the TypeScript CLI's form: a timeout
  is a result, `killed: timed out after N seconds`, instead of a tool error; a
  shell that a signal ended reports `exit code 128+N`; both `--- stdout ---`
  and `--- stderr ---` always appear, each cut at 64 KiB with a
  `[cut at 65536 bytes]` line. The output is cut while it is read, so a
  command that prints a lot no longer fills the memory.
- A cancelled shell tool call now finishes stopping the command's group,
  reaping the shell, and closing its pipes before the cancellation goes on.
