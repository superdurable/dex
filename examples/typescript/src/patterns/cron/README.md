# Cron Schedule Flow

**startCronSchedule** starts **CronScheduleFlow** with a one-hour interval and
ten occurrences. The next durable timer is scheduled when work starts. Publish
to **trigger** to run now or **skip** to consume the pending occurrence.
