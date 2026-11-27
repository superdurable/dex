---
section: Changed
---
- When a partition has a positive last command timestamp, rebuilding it at node start or failover looks for interrupted purges only among Flow runs whose deletion time is at or before that timestamp. This speeds up rebuilds for partitions with many closed runs still in retention.
