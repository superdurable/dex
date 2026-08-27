# DrainInternalChannelFlow

**Init** starts **MainStep** and **SideStep** in parallel. **MainStep** publishes
documents to **SideStepData** and then moves to **Finalize**. **SideStep** drains
one value at a time.

**Finalize** publishes a sentinel with `finalCommand: true`. **SideStep**
completes only after it receives that sentinel. **SideStepData** is FIFO, so all
earlier document commands are processed first.

## Endpoint

```text
GET /patterns/drain-channels/internal/start?workflowId={workflowId}
```
