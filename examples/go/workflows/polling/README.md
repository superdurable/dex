# Polling and channel coordination

The Flow waits for task A and task B through external typed Channels while another Step polls task C on a timer. The polling Step publishes task C's completion through the same unified Channel API.

With the sample server running:

```text
http://localhost:8080/polling/start?workflowId=polling-1&pollingCompletionThreshold=2
http://localhost:8080/polling/complete?workflowId=polling-1&channel=task-a-completed
http://localhost:8080/polling/complete?workflowId=polling-1&channel=task-b-completed
```
