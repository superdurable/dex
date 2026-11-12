---
section: Changed
---
- Every agents TypeScript package (`@superdurable/dex-agents`, the adapters,
  `@superdurable/dex-agents-pi`, and the agent CLI packages) adds Node 26
  (`^26.0.0`) to its **engines.node** range, so installing on Node 26 no longer
  prints `EBADENGINE` warnings for them. The two Pi packages keep their
  `^22.19.0` lower bound. Agents CI runs the TypeScript packages on Node 22
  and Node 26.
