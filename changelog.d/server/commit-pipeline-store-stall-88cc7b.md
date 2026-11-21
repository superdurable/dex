---
section: Changed
---
- **commit.batchWriteDeadline** now defaults to the larger of 10 seconds and **lease.duration**, at most 5 minutes, instead of 10 seconds. A short object storage stall no longer stops a node whose lease survives it: commits wait for the store and are acknowledged once their log batch is stored, in order. With **dex-server-zd dev** the deadline is 90 seconds and with **dex-server-zd local** 30 seconds. An explicit value still wins. [Reference](/reference/server/high-availability#lease)
- After the log write deadline passes, the node makes one more attempt of at most 5 seconds before it stops. A process pause, such as SIGSTOP or a frozen VM, that passed the deadline while a write was in flight no longer stops the node when object storage answers after the resume. [Reference](/reference/server/high-availability#lease)
