---
bump: patch
---
- **dex-app-builder**: the product workflow and product constraints move from **SKILL.md** to **references/product-route.md**, which the product route reads before Stage 0. SDK-only work no longer loads them.
- **core/patterns.md** becomes an index; each pattern family lives in its own file under **core/patterns/**, so an agent opens only the family it needs. Rules are unchanged.
