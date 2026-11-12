---
section: Fixed
---
- The shell tool of the TypeScript CLI commands answers about 1 s after the
  shell exits, even when a background process the command started keeps its
  output open, and leaves that process running. Before, the call waited for
  the timeout and then killed the shell's process group, whose ID the system
  may already have given to another process.
