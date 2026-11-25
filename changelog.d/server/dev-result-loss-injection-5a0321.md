---
section: Added
---
- **dex-server-zd dev** takes **--inject-step-result-loss**, **--inject-rpc-result-loss**, and **--inject-result-loss-seed** for tests. They drop a seeded share of Worker Step method and RPC results, as if the Worker crashed after its handler ran. A dropped Step result fails its attempt like a Worker disconnect and retries under the Step's retry policy; a dropped RPC result answers the caller with UNAVAILABLE. The same seed drops the same results. **local** and **serve** reject the flags, and the configuration file has no keys for them. [Reference](/reference/server/configuration#dev-worker-result-loss)
