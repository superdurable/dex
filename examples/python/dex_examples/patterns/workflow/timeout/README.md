# Flow Graceful Timeout

A flow-level timeout that fails the flow deliberately instead of letting the
platform kill it. The flow's configured timeout terminates it abruptly, with no
chance to record why it stopped; a parallel timeout branch gives you that
chance, and it is where any cleanup would go.

## Steps

1. `Init` — starts `Task` and `Timeout` in parallel, passing its own boolean
   input through to `Task`.
2. `Task` — skips the wait entirely when the input is `True` (the fast path);
   otherwise it waits 65 seconds to simulate slow work, then force-completes the
   flow.
3. `Timeout` — waits 1 minute on a timer, then force-fails the flow with an
   explanatory message.

Whichever branch finishes first decides the outcome. With input `True` the task
wins and the flow completes; with `False` the 65-second task loses to the
1-minute timer and the flow fails on purpose.

## Guidance

Set the timer shorter than the flow's own timeout so the graceful path always
wins the race. Keep any work in `Timeout` cheap and idempotent — it may run
concurrently with the tail of the task branch.
