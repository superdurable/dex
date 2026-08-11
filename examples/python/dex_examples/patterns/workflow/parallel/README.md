# Parallel Running Steps

Two variants of running steps in parallel inside a single flow:

1. **`SimpleParallelStatesFlow`** — fan out into independent steps and let each
   one complete on its own.
2. **`ParallelStatesWithAwaitFlow`** — fan out into N copies of the same step and
   wait for all of them before proceeding.

For higher scalability, see [scalableparallel](../scalableparallel) or
[parentchild](../parentchild).

## Use cases

- **Independent work** — a job seeker has a phone number and an email address,
  and the SMS and email notifications have no dependency on each other.
- **Same step, many inputs** — validate every address in a list by running one
  validation step concurrently per address.
- **Fan out then join** — validate every phone number and only continue once all
  of them are done.

## Choosing with or without await

Skip the await when you do not need the results (logging, fire-and-forget
notifications). Use the await when the flow cannot proceed until every parallel
branch has finished, or when you need their results.

## Known limitations

Temporal recommends fewer than 500 in-flight activities. For this pattern, stay
between 10 and 90 parallel steps: they all start immediately after the fan-out,
and large numbers are hard to inspect in Dex Web.

## Cost considerations

With local-activity optimization enabled, activities scheduled together can be
counted as a single Temporal action when there are fewer than 100 parallel
steps. Start around 20 parallel steps and tune from there, staying under 90.

## Steps

`SimpleParallelStatesFlow`

- `Init` — takes the `JobSeeker` input and starts `SendTextMessage` and
  `SendEmail`.
- `SendTextMessage` / `SendEmail` — simulate the notification, record an event,
  and complete the flow.

`ParallelStatesWithAwaitFlow`

- `Starting` — takes a count N, starts N `NotifyUser` steps plus one
  `AwaitAllUsersNotified`.
- `NotifyUser` — simulates a notification, publishes to the internal channel,
  and dead-ends.
- `AwaitAllUsersNotified` — waits for N messages on the channel, then completes.
