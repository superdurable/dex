---
section: Fixed
---
- **dex-server-zd dev** no longer stops when a busy machine slows its node lease renewals for about 15 seconds. Its **lease.duration** now defaults to 90 seconds instead of 30, and **lease.renewalAttemptTimeout** to 20 seconds, so the dev node rides out renewal stalls of about a minute. An override file that sets **lease.duration** keeps that value and the derived renewal attempt timeout. **dex-server-zd local** and **serve** keep their lease defaults. [Reference](/reference/server/high-availability#lease)
