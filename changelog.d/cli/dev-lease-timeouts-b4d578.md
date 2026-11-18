---
section: Fixed
---
- **dexcli dev** no longer exits with "Dex Server stopped unexpectedly: signal: abort trap" when a busy machine slows the dev node's lease renewals. With a bundled **dex-server-zd** 0.1.35 or later, the dev node uses a 90-second lease with 20-second renewal attempts and rides out renewal stalls of about a minute. A **--zd-config** file that sets **lease.duration** keeps its value. [Reference](/reference/server/high-availability#lease)
