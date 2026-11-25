---
bump: patch
---
- **dex-app-builder** drops "the Timer wins when ready at once" as a rule: a queued message wins at once, otherwise the first event to commit. A new pattern closes a reply window with an Attribute lock. The first reply closes the window, so it is taken even after the Timer won, and every later reply is told the window closed. The locked Execute gets a short retry budget.
