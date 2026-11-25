---
bump: patch
---
- **dex-app-builder** drops "the Timer wins when ready at once" as a rule: a queued message wins at once, otherwise the first event to commit.
- **dex-app-builder** adds a pattern that closes a reply window with an Attribute lock. The first reply closes it, every later reply is told the window closed, and the locked Execute gets a short retry budget.
