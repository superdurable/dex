# Cron Schedule Flow

The sample starts one Flow with a one-hour interval and ten occurrences. Its
next durable timer is scheduled when the current work starts, so the interval
is measured from start to start. Publish to **Trigger** to run the pending
occurrence now or **Skip** to consume it without running work. Canceling the
Flow stops the pending timer and prevents later runs.
