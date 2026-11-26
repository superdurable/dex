---
bump: patch
---
- Split Polling into two shapes. Polling with WaitFor Timer waits for a Timer in WaitFor, calls the provider once in Execute, and returns GoTo to the same Step while the work is not ready. Polling with long-running Step heartbeat keeps the existing single-Execute loop. A WaitFor Timer loop that polls an external system is allowed again.
- State the trade-off: the Timer shape records every round in the Flow's history, so each round is visible and Time Travel can move the run back to a round; it is somewhat heavier. The heartbeat shape is lighter. Add a choice table: between rounds the Timer shape holds no Worker task slot, a Dex Server failover fails only a check in progress instead of the running attempt, and Timers count whole seconds.
- Add the Timer-shape rules: "not ready" is a GoTo, never an error; bound the loop with a round number or an Attribute deadline; keep the Step input small and accumulated state in Attributes; call the provider only in Execute; tests skip the Timer with SkipTimer. Retry policy, backoff, or RetryAfter as the polling loop and a new Flow per page stay forbidden.
- Link the new WaitFor Timer polling examples in Go, Python, TypeScript, Java, and Rust.
