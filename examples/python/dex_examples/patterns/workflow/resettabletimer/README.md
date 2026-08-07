# Resettable Timer Flow

A timer that can be pushed back before it fires. Every reset message restarts the
countdown; if the timer does fire, the flow moves on to the expiry handling.

## Use cases

- Session or inactivity timeouts that extend on every user action.
- Debouncing bursts of events into a single downstream action.
- Deadlines that an operator can postpone.

## Steps

1. `ResettableTimerStep` — waits for either the duration timer or a reset
   message. On a reset message it loops back to itself, restarting the timer; on
   the timer it moves to `TimerExpired`.
2. `TimerExpired` — handles expiry and completes the flow.

## RPCs

- `send_reset_message` — publishes to the reset channel, restarting the timer.

## Channels

- `reset_channel` — carries reset messages.
