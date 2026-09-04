# FlowGracefulTimeout

This Flow uses a one-minute soft timeout. Its handler has a 30-second attempt
limit and retries invocation failures for up to three attempts.

## Endpoint

- `GET /patterns/timeout/start?workflowId={workflowId}`
