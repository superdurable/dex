---
section: Changed
---
- When a partition has a positive last command timestamp, rebuilding it at node start or failover scans for interrupted purges through one hour after that timestamp. The bound covers early Retention timer fires and skips runs with later deletion times.
