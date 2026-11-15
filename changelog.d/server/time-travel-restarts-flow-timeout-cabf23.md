---
section: Fixed
---
- Time Travel restarts the Flow timeout at the time travel, as if the run started then. Before, a restored Flow timeout kept the deadline from the run's start, so a run past that deadline timed out right after the time travel. A restored state whose timeout already fired, such as a running timeout handler, keeps that outcome.
