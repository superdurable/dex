# Timer

A Timer is a durable Dex Server timer used as a WaitFor Condition. It survives Worker and Server restarts and uses no Worker resource while it waits.

## When to use

| Need | Use |
| --- | --- |
| A durable delay, reminder, or business deadline | Timer Condition |
| A deadline that races a user action | AnyOf(Timer, Channel) |
| A recurring schedule | The cron pattern: AnyOf(Timer, trigger Channel, skip Channel) in a loop |
| A deadline for the whole Flow | The Flow timeout and timeout handler, not a competing Step. See [flow.md](flow.md#flow-timeout). |
| Waiting for an external system to become ready | Never a Timer loop. Use the [Polling pattern](patterns.md#polling). |
| Delay before the first work | A start Step that waits on a Timer. Dex has no start delay. |

## Rules

- A Timer is a Condition, not an in-process timer. Never sleep in a handler to model a business delay.
- In Execute, check whether the Timer fired, through the Context, before you assume which Condition won.
- Leave Timers unnamed in Until, AnyOf, and AllOf. Give a Timer an ID when an operation selects it by ID, and always inside AnyCombinationOf.
- Decide what happens when the Timer is skipped: whether the business deadline completes, cancels, fails, or routes to a handler.
- After each accepted activity in an inactivity window, return to a Step that creates a new Timer.

## Skip a Timer

SkipTimer makes a pending Timer behave as fired. It selects the Step execution and either the Timer's Condition ID or its zero-based Condition index. Use it in tests and for authorized operations.

- Acceptance is not proof of execution. The Timer may fire on its own before the skip applies.
- The Execute that follows runs asynchronously. Confirm it with a Step-completion wait or a business read.
- The Flow timeout is also a Timer. SkipTimer can fire it early by index.

## Self-check

- Does every Timer wait have a defined skip and late-delivery behavior?
- Is no Timer loop used to poll an external system?
- Does Execute check Timer outcomes through the Context?
