# Cron Schedule Workflow

Demonstrates starting a Dex flow with a cron schedule so each tick runs the flow
like a recurring job.

## How it starts

The sample process starts flow ID `cron-schedule-sample` with cron `*/1 * * * *`
at boot. There is no HTTP endpoint.
