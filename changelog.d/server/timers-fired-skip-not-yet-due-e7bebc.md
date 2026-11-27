---
section: Fixed
---
- A closed Flow run is no longer purged before its retention ends. When retention was postponed or the run closed again after a Time Travel, a timer firing that raced the new deadline could purge the run early. A timer now fires only once its current fire time has passed.
