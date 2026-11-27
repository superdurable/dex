---
section: Changed
---
- A partition rebuild at node start or failover looks for Flow runs whose purge had started only among runs whose deletion time is at or before the partition's last command time. It no longer reads every closed Flow run that retention keeps, so partitions with many closed runs rebuild faster.
