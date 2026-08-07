# Cron Schedule Workflow

Demonstrates starting a Dex flow with a cron schedule so each tick runs the
flow like a recurring job.

## Key components

1. **`CronScheduleStarter`** — on Spring `ApplicationReadyEvent`, starts flow ID
   `cron-schedule-sample` with cron expression `*/1 * * * *` (idempotent if
   already started).
2. **`CronScheduleFlow`** — the scheduled flow; each trigger runs its start step
   and completes.

## Useful links

- [CRON Expression Format](https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format)
- [CRON Expression Tester](https://crontab.guru/)

## How it starts

`CronScheduleStarter` uses the shared Dex `Client` after the Worker is up:

```java
client.startFlow(
    cronScheduleFlow,
    "cron-schedule-sample",
    null,
    StartFlowOptions.newBuilder()
        .timeout(Duration.ofHours(1))
        .cronSchedule("*/1 * * * *")
        .build());
```

If the flow is already running, `FLOW_ALREADY_STARTED` is ignored so restarts
are safe.

## Schedule management

View and manage the underlying Temporal schedule in the Temporal UI (local
`dexcli` Temporal or Temporal Cloud):

- **Workflows** — filter by workflow ID `cron-schedule-sample`
- **Schedules** — pause, trigger, or delete

To change the schedule: update `CronScheduleStarter`, deploy, delete the old
schedule in the UI if needed, then restart the sample process so it recreates
the schedule.

## Current limitations

Schedule overlap policy is not exposed and defaults to skip overlapping runs.
