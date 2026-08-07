# Cron Schedule Workflow

Demonstrates starting a Dex flow with a cron schedule so each tick runs the flow
like a recurring job.

## Endpoints

- `GET /design-pattern/cron/start` — start flow ID `cron-schedule-sample` with
  cron `*/1 * * * *`
