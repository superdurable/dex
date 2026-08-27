# Interruptible execution

**InterruptibleFlow** runs **WorkAStep** and **WorkBStep** in parallel. The
interrupt RPC writes a durable signal; each Step sees it before scheduling more
work and completes gracefully.

- `GET /patterns/interruptible/start?workflowId={workflowId}`
- `GET /patterns/interruptible/cancel?workflowId={workflowId}`

Diagram: [Lucid](https://lucid.app/lucidchart/b2866468-d530-4f76-9cc7-4441c5742460/edit?page=3-Wo.4lcXZvd)
