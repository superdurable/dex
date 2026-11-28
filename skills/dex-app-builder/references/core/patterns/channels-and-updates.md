# Channel and update patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Drain Channels

Choose by who publishes. Keep accepted messages durable and define when publication is rejected or redirected.

### Drain internal Channels

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Flow completion](../../../SKILL.md#flow-timeout-completion-and-cancellation).

Producers and consumers are branches of one Flow; finish only after every published message is handled. Go folder: `drain-channels/internal-drain`.

```text
StartImport.execute -> goToMany(WriteRecords, ReadPages)
ReadPages.execute -> Commands.publish(page); goTo(ReadPages) ... then goTo(PublishEndMarker)
PublishEndMarker.execute -> Commands.publish(end marker); gracefulComplete()
WriteRecords.waitFor -> Until(Commands.ForOne())
WriteRecords.execute -> if end marker: gracefulComplete(report) else: write; goTo(WriteRecords)
```

- The Channel is FIFO, so the consumer sees the end marker only after every earlier message.
- With several producers, the end-marker Step waits for all of them before publishing it.

### Drain an external Channel

Derived from: [Flow completion](../../../SKILL.md#flow-timeout-completion-and-cancellation), [ID reuse](../../../SKILL.md#starting-a-flow-and-id-reuse), [RPCs](../../../SKILL.md#rpcs).

Bursty external messages; the Flow stays short and restarts on the next message. Go folder: `drain-channels/external-publishing`.

```text
HandleMessage.waitFor(message) -> if message is empty: Until(Queue.ForOne())
                                  else: SkipWaitImmediately
HandleMessage.execute -> handle the message
                         forceCompleteIfChannelsEmpty([Queue], else goTo(HandleMessage, empty))
rpc publishExternalMessage -> Queue.publish(message)

Controller(entityId, message):                   // flowId = entityId; message has a stable ID
    repeat a few times:
        try startFlow(flowId, input = message, reuse = allow if not running,
                      requestId = "start:" + message.id, ignore already started)
            return accepted
        on already started:                     // a different start request owns the active run
            try invokeRPC(flowId, publishExternalMessage, message,
                          requestId = "publish:" + message.id)   // where the SDK supports it
                return accepted
            on missing-or-inactive: continue    // that run closed; start again
        other errors: return them
    return unconfirmed with message.id          // outcome unknown; the caller sends it again
```

- How a retried or a new start resolves follows the [Request ID and reuse tables](../../../SKILL.md#starting-a-flow-and-id-reuse).
- "Accepted" means the run accepted the message, not that a Step handled it.
- One stable Flow ID per entity, one active run at a time, messages handled in the order their publications commit. An external business order needs its own ordering mechanism.
- The RPC only validates the message and publishes it to the Channel. It writes no Attribute that a Step reads or locks, such as a counter or a per-entity status. The consuming Step owns all state, so concurrent RPCs never hit a lock conflict.
- Close only with the atomic decision; never check the Channel size first and then complete. See [ForceCompleteIfChannelsEmpty](https://docs.superdurable.io/reference/go/step-decisions#forcecompleteifchannelsempty).
- Only one Step may consume a Channel that the decision checks.
- When the caller must confirm each message was handled, the consuming Step records the message's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).

## Control Long-Running Work

Derived from: [Starting a Flow](../../../SKILL.md#starting-a-flow-and-id-reuse), [Snapshots](../../../SKILL.md#repeated-execution-snapshots-and-locks).

Long repeating work that callers change or stop while it runs. Formerly Stop Long-Running Work and Interruptible execution. Go folder: `control-long-running-work`.

```text
Loop.waitFor(progress) -> Until(Timer(gap))
Loop.execute(progress) -> if stopRequested: gracefulComplete()
                          size = batchSize ?? default
                          process one batch of size; if done: gracefulComplete()
                          goTo(Loop, progress + size)
rpc setBatchSize(size) -> validate; batchSize = size
rpc requestStop        -> stopRequested = true
```

- Keep the loop position in the Step input and the controls in Attributes. An RPC validates and writes each control.
- Read every control at the start of each `Execute`. A new value applies from the next iteration.
- Never let a Step write a control's default: a Step's write could overwrite an RPC that arrived first. Apply the default at read time, or pass it as an initial Attribute at start, which commits with the Flow's creation.
- Use an Attribute for a parameter, such as a batch size, a rate, or a pause flag: the latest value wins. Use a Channel for per-item commands, such as skip or retry: accepted commands queue in FIFO order and one consumer takes them in that order.
- The loop then waits on the Timer and the Channel with `AnyOf`, and `Execute` checks which branch won: it handles the consumed commands when the Channel won and processes the next batch when the Timer won. Later commands stay queued.
- When every accepted command must be handled before the loop stops, drain the Channel before the Flow closes. When outside callers can publish concurrently, close with an atomic complete-if-empty decision; see [Drain an external Channel](#drain-an-external-channel).
- A stop request in a control Attribute lets each unit finish before the loop stops; StopFlow and Step cancellation stop at once, as the [cancellation facts](../../../SKILL.md#flow-timeout-completion-and-cancellation) describe.
- Each committed `GoTo` moves the position past the processed range, so a new batch size never skips or repeats a range. A retried `Execute` can process the same range again, so make real processing idempotent by each record's business ID.
- To stop now and cancel running Steps, use RPC cancellation. See [rpc.md](../rpc.md#cancel-running-steps-from-an-rpc).

## Responsive update

Derived from: [Runs, reads, and outputs](../../../SKILL.md#runs-reads-and-outputs), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks), [Streams](../../../SKILL.md#streams).

Return at the boundary the caller needs while the Flow keeps owning durable work. Set the request timeout at the HTTP or admission boundary.

### Step completion

The caller needs a durable milestone before the background work ends. Go folder: `wait-for-step-completion`.

```text
PersistRecord.execute -> commit the record; set Attribute; goTo(UpdateExternalSystem)
Controller: startFlow -> waitForStepCompletion(PersistRecord) -> read through an RPC or Client Attribute read
```

Choose a Step only after it committed the state that makes the response valid.

### Attribute match

The caller refreshes after durable business state changes. Example: `products/job-post`.

```text
revision: integer Attribute, initialized to 0
rpc updatePosting(change)        // locks revisionLock
    apply change; revision = revision + 1
Caller:
    seen = waitForAttributeMatch(revision > lastSeen)
    read canonical state with a Client Attribute read or a read RPC
    lastSeen = seen
```

- Every writer that advances the state increments the revision in the same invocation. When more than one can write concurrently, they share one lock.
- The revision is a watermark, not an event stream. A wait may skip intermediate values.
- The wait sees only that Attribute, not run IDs or history.

### Stream

The caller shows incremental progress. Example: `primitives/stream`.

The Flow writes to a Stream. The caller reads forward with a resume token and passes the new token to the next read. Messages are best effort, not a final result. Use a Step-completion or Attribute-match wait for a durable condition.
