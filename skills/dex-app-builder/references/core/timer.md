# Timer

A Timer is a durable Dex Server timer used as a WaitFor Condition. It survives Worker and Server restarts and uses no Worker resource while it waits.

## When to use

| Need | Use |
| --- | --- |
| A durable delay, reminder, or business deadline | Timer Condition |
| A deadline that races a user action | AnyOf(Timer, Channel) |
| A recurring schedule | The cron pattern: AnyOf(Timer, trigger Channel, skip Channel) in a loop |
| A deadline for the whole Flow | The Flow timeout and timeout handler, not a competing Step. See [flow.md](flow.md#flow-timeout). |
| Waiting for an external system to become ready | A [Polling](patterns.md#polling) shape: a bounded Timer loop that calls the provider once per Execute, or one long-running Execute with heartbeats. |
| Delay before the first work | A start Step that waits on a Timer. Dex has no start delay. |

## Rules

- A Timer is a Condition, not an in-process timer. Never sleep in a handler to model a business delay.
- In Execute, check whether the Timer fired, through the Context, before you assume which Condition won.
- Timers in Until, AnyOf, and AllOf need no names. Give a Timer an ID when an operation selects it by ID, and always inside AnyCombinationOf.
- After each accepted activity in an inactivity window, return to a Step that creates a new Timer.

## Skip a Timer

SkipTimer makes a pending Timer behave as fired. It selects the Step execution and either the Timer's Condition ID or its zero-based Condition index. It works only on an active Flow. Use it in tests, and in production to end a waiting Timer early.

- A deploy never changes a Timer that already waits. To shorten a deadline, new Steps use the new duration, and a periodic job or operator action calls SkipTimer on each old Timer once its new deadline passes. See [application.md](application.md#steps-that-already-wait).

- Acceptance is not proof of execution. The Timer may fire on its own before the skip applies.
- The Execute that follows runs asynchronously. Confirm it with a Step-completion wait or a business read.
- The Flow timeout is also a Timer. SkipTimer can fire it early by index.

## Self-check

- Does a Timer loop that polls an external system call it only in Execute, and is it bounded by a round limit, the Flow timeout, or a watchdog Timer?
- Does Execute check Timer outcomes through the Context?
