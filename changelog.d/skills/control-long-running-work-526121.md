---
bump: patch
---
- Rename the **Stop Long-Running Work** pattern to **Control Long-Running Work**. It now covers changing a running loop, such as its batch size, through control Attributes read at the start of each iteration, and when to use a Channel instead for per-item commands. The three built-in ways to stop work are unchanged.
