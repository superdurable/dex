# Reminder Flow

Sends a reminder on a fixed cadence until the user accepts, opts out, or the
overall deadline passes.

## Steps

1. `Init` — sets the status to `INITIATED` and starts `ProcessTimeout` and
   `Reminder` in parallel.
2. `ProcessTimeout` — waits for either the 60-day timeout timer or the internal
   completion channel, then notifies the external system with `ACCEPTED` or
   `TIMEOUT` and force-completes the flow.
3. `Reminder` — waits for either the 5-second reminder interval or the opt-out
   signal. It completes the flow if the status is already `ACCEPTED` or the user
   opted out; otherwise it sends the reminder email and loops back to itself.

Because the two branches run in parallel, whichever completes first ends the
flow: acceptance or opt-out stops the reminders immediately, and the deadline
stops them even if the user never responds.

## RPCs

- `accept` — rejects the call unless the status is `INITIATED`, then sets the
  status to `ACCEPTED` and publishes to the internal completion channel so
  `ProcessTimeout` wakes up and completes the flow.

## Channels and persistence

- `OptOutReminder` — signal channel the user publishes to in order to stop
  receiving reminders.
- `CompleteProcess` — internal channel `accept` uses to wake `ProcessTimeout`.
- `Status` — a `str` attribute holding `INITIATED` or `ACCEPTED`.
