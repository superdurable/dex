# FlowGracefulTimeout

This Flow uses a one-minute soft timeout. Its handler has a 30-second attempt
limit and retries invocation failures for up to three attempts.

## Usage

Start the fast path:

```http request
http://localhost:8080/patterns/timeout/start?workflowId=handleTimeoutWorkflow
```

Trigger the timeout handler:

```http request
http://localhost:8080/patterns/timeout/start?workflowId=handleTimeoutWorkflow&successfulWorkflow=false
```
