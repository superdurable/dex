---
section: Changed
---
- The timer service starts each read of the timer index at a cursor, so it scans fewer tombstones left by deleted index rows, and it reads only one future timer when nothing is due. This lowers CPU and storage reads on partitions with many timers.
