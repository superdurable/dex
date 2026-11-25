---
bump: patch
---
- **dex-app-builder** drops "the Timer wins when ready at once" as a rule: a queued message wins at once, otherwise the first event to commit. A new pattern closes a reply window with an Attribute lock, so a reply after the Timer won is taken or told the window closed.
