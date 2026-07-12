# SDK persistence design

Workflow step state and channels share a schema-first, typed accessor model. Step methods receive a single `dex.Context` that carries run metadata, state access, consumed channel messages, and deferred publishes.

## Step signatures

```go
func (s *MyStep) WaitFor(ctx dex.Context, input MyInput) (dex.WaitForCondition, error)
func (s *MyStep) Execute(ctx dex.Context, input MyInput) (dex.StepDecision, error)
```

`ctx.TimerFired()` is meaningful in Execute after a wait resumed on a durable timer.

## State keys

Declare keys at package scope (like channels):

```go
var keyCount = dex.NewStateKey[int]("Count")
var keyOrders = dex.NewDynamicStateKey[OrderDetail]("orders/")
```

Register them in `GetPersistenceSchema`:

```go
func (f *MyFlow) GetPersistenceSchema() dex.PersistenceSchema {
    return dex.PersistenceSchema{
        StateKeys: []dex.StateKeyDef{
            dex.DefineStateKey(keyCount),
        },
        DynamicStateKeys: []dex.StateKeyDef{
            dex.DefineDynamicStateKey(keyOrders),
        },
        Channels: []dex.ChannelDef{dex.DefineChannel(myChannel)},
    }
}
```

Read and write inside steps via the key and context:

```go
count, err := keyCount.GetValue(ctx)
if err := keyCount.SetValue(ctx, newCount); err != nil { ... }

detail, err := keyOrders.GetValue(ctx, orderID)
if err := keyOrders.SetValue(ctx, orderID, updated); err != nil { ... }
```

After a run completes, read final state from the client:

```go
status, err := client.WaitForRunCompletion(ctx, runID, pollInterval)
count, err := keyCount.GetRunValue(client, ctx, runID)
```

### Semantics

| Case | Behavior |
|------|----------|
| Missing key on get | Zero value, `nil` error |
| Decode failure on get | Zero value, wrapped error |
| Undeclared state key | `errors.Is(err, ErrUndeclaredStateKey)` |
| Undeclared channel | `errors.Is(err, ErrUndeclaredChannel)` |
| Encode failure on set | Error |

State writes and channel publishes are buffered on the context and flushed when the step method returns.

## Channels

Static and dynamic channels mirror state keys:

| | Static | Dynamic |
|---|--------|---------|
| Type | `Channel[T]` | `DynamicChannel[T]` |
| Constructor | `NewChannel(name)` | `NewDynamicChannel(prefix)` |
| Read in step | `ch.GetConsumedMessages(ctx)` | `dc.GetConsumedMessages(ctx, key)` |
| Write in step | `ch.Publish(ctx, values...)` | `dc.Publish(ctx, key, values...)` |
| Schema | `DefineChannel(ch)` | `DefineDynamicChannel(dc)` |

External publishes to a running workflow use client methods:

```go
client.PublishToChannel(ctx, runID, ch.Name, values...)
client.PublishToDynamicChannel(ctx, runID, dc.Prefix, key, values...)
```

## Field locking

Use typed helpers so wire names stay aligned with schema:

```go
dex.LockStateKey(keyCount, keyThreshold)
dex.LockDynamicStateKey(keyOrders)
```

## Symmetry

State keys and channels follow the same pattern: declare typed handles, register in schema, access through the handle with `ctx` as the first argument.

## ObjectCodec

Serialization is pluggable via `ObjectCodec`. The default is `DefaultObjectCodec()`.

Pass a custom codec when creating the registry:

```go
registry := dex.NewRegistryWithOptions(dex.RegistryOptions{
    ObjectCodec: myCodec, // nil uses DefaultObjectCodec()
})
client := dex.NewClient(registry, conn, namespace)
worker := dex.NewWorker(registry, matchConn, runsConn, namespace, opts)
```

Client, worker, state keys, channel values, and step inputs all use the registry's codec.

## Testing

Unit tests outside the worker use `dex.NewTestContext(parent, schema, stateMap, timerFired, channelMessages)`.
