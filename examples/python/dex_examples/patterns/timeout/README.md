# Flow Graceful Timeout

This Flow uses a one-minute soft timeout. Its handler has a 30-second attempt
limit and retries invocation failures for up to three attempts.

With a true input, the start Step completes immediately. With a false input, it
waits 65 seconds, so the Flow timeout handler runs first and deliberately fails
the Flow with an application message.
