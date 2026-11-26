---
section: Changed
---
- Behavior change: **limits.maxConcurrentStepsPerFlow** (default 1000) is enforced. A Flow start or RPC that would start more active Steps fails with RESOURCE_EXHAUSTED; such a Step completion fails its attempt without retry. [Reference](/reference/server/configuration#step-and-channel-limits)
- Behavior change: **limits.maxChannelMessages** is enforced, and its default drops from 10000 to 1000. A publish that would leave a Channel with more pending messages fails the same way. [Reference](/reference/server/configuration#step-and-channel-limits)
- Both limits apply only to Flow runs that start on this release or later, including child Flows and clones. Earlier Flow runs stay unlimited, and recovery applies their commands as before. [Upgrades](/reference/server/upgrades)
