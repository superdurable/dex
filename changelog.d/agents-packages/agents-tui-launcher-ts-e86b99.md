---
section: Fixed
---
- The shell tool of the TypeScript CLI commands stops what a command left in
  its process group when the command ends, such as a process started with
  `&`, and answers at once. Before, such a call waited for its timeout and
  then killed the group.
