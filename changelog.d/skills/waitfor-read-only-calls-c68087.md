---
bump: patch
---
- State one WaitFor rule everywhere: WaitFor never mutates an external system but may make read-only, idempotent, time-bounded calls, such as reading a due date for a Timer. Provider mutations and Connector writes run in Execute. Add when to read a provider deadline in the previous Step instead.
