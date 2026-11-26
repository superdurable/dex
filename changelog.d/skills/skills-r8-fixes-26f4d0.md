---
bump: patch
---
- A deadline measured from an event is a Timer started when the event is handled: a fresh Timer for each idle window, or a watchdog Step started with the first polling round. The remaining time is never re-derived from a stored timestamp and the clock.
- A decision that was consumed is final: no clock or Timer grace window after it catches a later event. Under a reply window, the reply RPC publishes the reply and closes only the window; the waiting Step's Execute records the outcome.
- A client timeout on a blocking call that cannot observe cancellation is now a recommendation. The method or heartbeat timeout still ends the attempt, and retries continue.
- A child Flow retried by its own business number keeps that number as its Flow ID and starts a new run under a reuse policy that allows a closed run. The parent accepts reports and reads only from the run ID it claimed. A rejected admission is its own state, never ended or failed.
- The lock rule states the stop-must-block fix first: the stop RPC and the deciding Step share the lock, and the next, unlocked Step calls the external system. The no-lock grace variant is a separate, shorter rule.
