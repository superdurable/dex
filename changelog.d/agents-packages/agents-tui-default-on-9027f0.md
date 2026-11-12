---
section: Changed
---
- Python and TypeScript CLI cores: the commands download `dexcli` 1.46.0,
  which has the terminal UI, instead of 1.45.0 when they find no `dexcli` of
  at least 1.46.0. A `dexcli` on `PATH` older than 1.46.0 is no longer used.
