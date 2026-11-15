---
section: Fixed
---
- A Time Travel no longer leaves the parent waiting forever on a child run that no longer exists, for example after its purge. It now rejects the request with **FAILED_PRECONDITION** and names each Step execution and child Flow ID, because such a child can be neither attached nor restarted.
