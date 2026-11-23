---
section: Fixed
---
- **dex-server-zd dev** and **local** bind **--listen** before the embedded S3 emulator takes a free port. A start on a port that the caller has just freed no longer fails with "Address already in use" when the emulator gets that port. [Reference](/reference/server/configuration#single-node-local)
