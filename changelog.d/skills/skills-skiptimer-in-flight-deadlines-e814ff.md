---
bump: patch
---
- State that a deploy never changes the conditions of a Step that already waits, and show how to shorten such deadlines with the new duration plus `SkipTimer`, or change the wait by publishing to its Channel, before restarting the Step.
