---
bump: patch
---
- Correct the Worker dispatch rules in `dex-sdk`: each Worker serves one task queue, a Flow type name must be unique within one task queue rather than on a whole Server, and sticky routing never crosses task queues.
