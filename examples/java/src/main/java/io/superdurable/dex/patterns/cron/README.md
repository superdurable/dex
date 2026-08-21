# Cron Schedule Flow

**CronScheduleStarter** starts **CronScheduleFlow** with a one-hour interval and
ten occurrences. The next durable timer is scheduled when the current work
starts. Publish to **trigger** to run the pending occurrence now or **skip** to
consume it. Restarting the application does not duplicate an active Flow.
