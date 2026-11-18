---
section: Fixed
---
- **dex-server-zd dev** keeps its node lease through slower renewal writes on a busy machine. Its **lease.duration** now defaults to 90 seconds instead of 30, and **lease.renewalAttemptTimeout** to 20 seconds. A renewal write may take up to 20 seconds, and a cut-off write is retried once within the about 60 seconds before the lease ends. A log write still stops the node when object storage does not accept it within 10 seconds. An override file that sets **lease.duration** keeps that value and the derived renewal attempt timeout. **dex-server-zd local** and **serve** keep their lease defaults. [Reference](/reference/server/high-availability#lease)
