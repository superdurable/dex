---
bump: patch
---
- A deadline measured from an event is a Timer started when the event is handled, never a remaining time derived from a stored timestamp.
- Once the decision's snapshot or a closed window fixes the candidates, no clock or Timer grace window waits for later events. The reply RPC publishes the reply and closes the window in one locked commit. The waiting Step's Execute decides the outcome.
- A client timeout on a blocking call that cannot observe cancellation is now a recommendation. The method or heartbeat timeout still ends the attempt, and Dex schedules later attempts by the retry policy.
- A child Flow reopened by its business number keeps it as the Flow ID. A reopen is a new start request with a new Request ID, under a reuse policy that allows the needed closed state and rejects an active run. The parent trusts only its claimed run ID.
- A rejected child admission is its own state, never ended or failed.
- The lock rule states the decision-commit deadline first: the stop RPC and the deciding Step share the lock, and the next, unlocked Step calls the external system. The no-lock grace variant is a separate, shorter rule.
