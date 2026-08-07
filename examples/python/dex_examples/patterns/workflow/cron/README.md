# Cron Schedule Flow

Starts a Dex flow on a cron schedule so each tick runs the flow like a recurring
job.

## Key components

- `CronScheduleFlow` — the scheduled flow. Each trigger runs its start step and
  completes.
- `CronScheduleStep` — the single start step.

## How it starts

Pass a cron expression in `StartFlowOptions` when starting the flow once at
process boot:

```python
client.start_flow(
    cron_schedule_flow,
    CRON_SCHEDULE_FLOW_ID,
    None,
    StartFlowOptions(
        timeout=timedelta(hours=1),
        cron_schedule=CRON_SCHEDULE_EXPRESSION,
        ignore_already_started=True,
    ),
)
```

`ignore_already_started=True` keeps restarts safe: if the schedule already
exists, the start call is a no-op.

## Useful links

- [CRON expression format](https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format)
- [CRON expression tester](https://crontab.guru/)

## Schedule management

View and manage the underlying Temporal schedule in the Temporal UI: filter
workflows by the flow ID, or use the Schedules tab to pause, trigger, or delete.
To change the schedule, update the expression, delete the old schedule in the
UI, then restart the sample process so it recreates the schedule.

## Current limitations

Schedule overlap policy is not exposed and defaults to skipping overlapping
runs.
