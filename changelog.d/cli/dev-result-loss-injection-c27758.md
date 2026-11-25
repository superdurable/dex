---
section: Added
---
- **dexcli dev** takes **--inject-step-result-loss**, **--inject-rpc-result-loss**, and **--inject-result-loss-seed** and passes them to server-zd, which drops a seeded share of Worker results for tests. Tests use them to find external calls that are not idempotent. They need a server-zd release that supports them. [Reference](/reference/cli/#start-locally)
