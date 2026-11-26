---
section: Changed
---
- The timer service skips timers it already fired when it reads the timer index, and reads only one future timer when nothing is due. This lowers CPU and storage reads on partitions with many timers.
