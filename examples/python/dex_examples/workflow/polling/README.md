# Polling and channel coordination

The Flow waits for task A and task B through external typed Channels while
another Step polls task C on a one-second timer. The polling Step publishes task
C's completion through the same unified Channel API, so `WaitForTasks` cannot
tell the difference between an external and an internal completion.

The start input is the maximum number of polls, and it is carried along as the
`Poll` Step's typed input on every iteration.

With the sample server running:

```text
http://localhost:8080/polling/start?workflowId=polling-1&pollingCompletionThreshold=2
http://localhost:8080/polling/complete?workflowId=polling-1&channel=task-a-completed
http://localhost:8080/polling/complete?workflowId=polling-1&channel=task-b-completed
```
