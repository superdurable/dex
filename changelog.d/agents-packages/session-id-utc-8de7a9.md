---
section: Fixed
---
- The Python CLI core (`dex-agents-cli-core`) builds a new session ID from the
  UTC start time, as the TypeScript CLI core does, instead of the local time.
  Both now give the same shape, such as `builtin-20261111-203123-3efeb4`.
