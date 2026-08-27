# Interruptible execution

`InterruptibleFlow` starts `WorkAStep` and `WorkBStep` in parallel. The
`interrupt` RPC writes `interruptSignal`; each Step checks it before scheduling
more work and completes gracefully.

Diagram: [Lucid](https://lucid.app/lucidchart/b2866468-d530-4f76-9cc7-4441c5742460/edit?page=3-Wo.4lcXZvd)
