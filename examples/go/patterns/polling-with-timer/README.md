# Polling with WaitFor Timer

**PollingWithTimerFlow** waits for the same simulated job as
[PollingFlow](../polling). **StartJob** starts the job with the Flow ID as its
idempotent job ID. **CheckJobStatus** waits for a 10-second Timer in `WaitFor`,
then checks the job status once in `Execute`. While the job is not finished, it
moves to itself with the next round number. Each round is a new Step execution
in the Flow's history.

`maxPollRounds` bounds the loop. The round number is the only state the Step
input carries. A transient error in the status call fails the attempt, and the
retry policy runs it again. When the retries are exhausted, `ExecuteFailure`
moves to **RecordJobWaitFailure**, which fails the Flow with the error type and
detail.

The Timer has the condition ID `poll-interval`, so a test or an operator can
skip it with `SkipTimer` instead of shortening the interval.

## Endpoints

- `GET /patterns/polling-with-timer/start?workflowId={workflowId}`
- `GET /patterns/polling-with-timer/complete-job?workflowId={workflowId}`

See [Polling with WaitFor Timer](https://docs.superdurable.io/design-patterns/durable-timer/polling-with-wait-for-timer)
for the pattern rules.
