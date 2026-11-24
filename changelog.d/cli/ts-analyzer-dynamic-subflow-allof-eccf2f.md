---
section: Fixed
---
- `dexcli visualize` accepts a TypeScript `Wait.allOf` or `Wait.anyOf` argument that spreads `items.map((item) => SubFlow.run(childFlow, item))` and draws one SubFlow condition for it, as for Python, instead of reporting `dynamic_wait_condition`.
