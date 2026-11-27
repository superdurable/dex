---
bump: patch
---
- Split the child-job rule in App Builder into identity and start, the already-started error, and ending and reconcile. A rejected admission is its own state, never ended or failed, and every report lists it as rejected.
- Tying a child's reads, reports, and stops to the claimed run ID is now a business decision: it is needed when a child number can be reopened while the parent waits; with one run per number, the Flow ID is enough.
