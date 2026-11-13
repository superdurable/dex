---
section: Changed
---
- **dexcli visualize** reports a missing TypeScript compiler with a message
  that names both places it looks: the source project and the
  **node_modules** directories listed in **NODE_PATH**. A source project
  without **node_modules** can use a separately installed, pinned
  **typescript** package through **NODE_PATH**; see
  [Flow source visualization](/reference/web/flow-visualization).
