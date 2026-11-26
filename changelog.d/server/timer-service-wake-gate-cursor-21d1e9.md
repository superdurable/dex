---
section: Changed
---
- The timer service wakes for a newly armed timer only when it fires before the service's next planned check. It starts each read of the timer index at a cursor, so it scans fewer tombstones left by deleted index rows, and it reads only one future timer when nothing is due. This lowers CPU and storage reads on partitions with many timers.
