# Control Long-Running Work

- `GET /patterns/control-long-running-work/start?workflowId={workflowId}`
- `GET /patterns/control-long-running-work/set-batch-size?workflowId={workflowId}&batchSize={batchSize}`
- `GET /patterns/control-long-running-work/stop?workflowId={workflowId}`

`RecordBackfillFlow` processes 1000 records in batches, one batch per second. `ProcessRecordBatch` reads the `batchSize` and `stopRequested` Attributes at the start of each `Execute`. `SetBatchSize` changes the size of every later batch; the default is 10. `RequestStop` makes the loop complete gracefully before its next batch.

See the [Control Long-Running Work design pattern](https://docs.superdurable.io/design-patterns/external-interaction/control-long-running-work).
