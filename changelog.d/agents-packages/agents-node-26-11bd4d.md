---
section: Changed
---
- Every agents TypeScript package (`@superdurable/dex-agents`, the adapters,
  `@superdurable/dex-agents-pi`, and the agent CLI packages) accepts Node 26 in
  its **engines.node** range (`^22.0.0 || ^24.0.0 || ^26.0.0`), so installing
  on Node 26 no longer prints `EBADENGINE` warnings for them. Agents CI runs
  the TypeScript packages on Node 22 and Node 26.
