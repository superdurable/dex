# Rust primitives

How each Dex Primitive looks in Rust. Read the matching core file for what the
Primitive guarantees. The skeletons below compile against the current Rust SDK;
`use` lines are omitted. Option lists and defaults are in the
[Rust Developer reference](https://docs.superdurable.io/reference/rust/).

| Primitive | Rust shape | Core |
| --- | --- | --- |
| Flow | `impl Flow for OrderFlow { type StartInput = ...; }` | [flow.md](../core/flow.md) |
| Step | `impl Step for X { type Input = ...; fn wait_for; fn execute }` | [step.md](../core/step.md) |
| Attribute, AttributeMap | `static LazyLock<Attribute<T>>`, `LazyLock<AttributeMap<T>>` | [attribute.md](../core/attribute.md) |
| Channel, ChannelMap | `static LazyLock<Channel<T>>`, `LazyLock<ChannelMap<T>>` | [channel.md](../core/channel.md) |
| RPC | `const X: Rpc<In, Out>` bound in `rpcs()` | [rpc.md](../core/rpc.md) |
| Stream | `static LazyLock<Stream<T>>`, `buffered_text` | [stream.md](../core/stream.md) |
| Timer | `Timer::by_duration` Condition | [timer.md](../core/timer.md) |
| SubFlow | `SubFlow::run` Condition | [subflow.md](../core/subflow.md) |
| Client | `Client` methods taking definitions and RPC consts | [client.md](../core/client.md) |

## Flow and Steps

```rust
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct OrderRequest {
    pub order_id: String,
    pub amount_cents: i64,
}

#[derive(Default)]
pub struct OrderFlow {
    await_approval: AwaitApproval,
    charge_payment: ChargePayment,
    record_charge_failure: RecordChargeFailure,
}

impl Flow for OrderFlow {
    type StartInput = OrderRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_approval)
            .and(&self.charge_payment)
            .and(&self.record_charge_failure)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&ORDER_STATUS)
            .attribute(&ORDER_REVISION)
            .attribute_map(&ORDER_NOTES)
            .channel(&ORDER_APPROVALS)
            .stream(&ORDER_PROGRESS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(APPROVE_ORDER, Self::approve_order)
            .function_without_input(GET_ORDER_STATUS, Self::get_order_status)
            .function(ADD_ORDER_NOTE.lock(ORDER_REVISION.lock()), Self::add_order_note)
            .procedure_without_input(CANCEL_ORDER, Self::cancel_order)
    }

    fn timeout_handler(&self) -> Option<FlowTimeoutHandler<Self>> {
        Some(Self::handle_timeout)
    }
}

#[derive(Default)]
struct AwaitApproval;

impl Step for AwaitApproval {
    type Input = OrderRequest;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            ORDER_APPROVALS.for_one().with_id("approval"),
            Timer::by_duration(Duration::from_secs(24 * 3600)).with_id("approval-deadline"),
        ]))
    }

    fn execute(&self, context: &mut Context, order: Self::Input) -> HandlerResult<StepDecision> {
        if context.has_any_timer_fired() {
            return Ok(StepDecision::force_fail("approval deadline expired"));
        }
        let approvers = ORDER_APPROVALS.condition_results(context)?;
        ORDER_STATUS.set(context, format!("approved by {}", approvers[0]))?;
        Ok(StepDecision::go_to(&ChargePayment, order))
    }
}
```

- The start Step's `Input` equals the Flow's `StartInput`.
- `wait_for` defaults to `Wait::skip_immediately()`; omit it when the Step
  needs no wait.
- `wait_for` declares durable readiness. It may write Attributes, publish, and
  set Step execution local values, but it never calls an external system.
  External effects belong in `execute`. See the
  [WaitFor rule](../core/step.md#waitfor-versus-execute).
- `execute` returns one `StepDecision`. Returning `Err(HandlerError)` fails the
  attempt and discards everything the attempt staged.
- Every `Context` read or write returns `HandlerResult`; propagate it with `?`.

## Waits and Conditions

| Need | Rust |
| --- | --- |
| One Condition | `Wait::until(condition)` |
| First ready of several | `Wait::any_of([c1, c2])` |
| All of several | `Wait::all_of([c1, c2])` |
| First complete group | `Wait::any_combination_of([ConditionCombination::all_of([c1, c2]), ConditionCombination::all_of([c3])])` |
| No wait | `Wait::skip_immediately()` or the default `wait_for` |

- Condition factories: `CHANNEL.for_one()`, `for_n(n)`, `at_least(n)`,
  `at_most(n)`, `range(Some(min), Some(max))` (each bound an `Option<usize>`);
  `CHANNEL_MAP.for_one(instance)` and friends; `Timer::by_duration(duration)`;
  `SubFlow::run(&child, input).map_err(HandlerError::from_error)?`, because
  `SubFlow::run` returns an `SdkResult`.
- `.with_id("...")` names a Condition. Leave Conditions unnamed in `until`,
  `any_of`, and `all_of`; `execute` reads results from the Channel and the
  Timer accessors, never by ID. Name every Condition inside
  `any_combination_of`, and a Timer that `skip_timer` targets with
  `TimerId::by_condition_id`.
- Which ready Condition wins an `any_of`, and which messages a Channel
  Condition consumes, follow [../core/step.md](../core/step.md). For strict
  priority, wait on the higher-priority Condition alone.
- Read results in `execute` from the same invocation's `Context`:
  `CHANNEL.condition_results(context)?`, `context.has_any_timer_fired()`,
  `context.has_timer_fired(index)`, `SubFlow::condition_result(context)?`.
  Never infer a winner from elapsed time.

## Step decisions

| Decision | Rust | Effect |
| --- | --- | --- |
| Next Step | `StepDecision::go_to(&Step, input)` | Schedules one Step. |
| Fan out | `StepDecision::go_to_many(items.into_iter().map(\|item\| StepMovement::to(&Work, item)))` | Schedules several Steps at once. |
| End branch | `StepDecision::dead_end()` | This branch stops; the Flow stays open. |
| Complete when siblings finish | `StepDecision::graceful_complete(output)` | Flow completes after other active Steps settle. |
| Complete now | `StepDecision::force_complete(output)` | Flow completes immediately; other Steps stop. |
| Complete if queues empty | `StepDecision::force_complete_if_channels_empty(output, fallback_movement, [QUEUE.when_empty()])` | Atomically completes, or runs the fallback when a Channel has messages. |
| Fail | `StepDecision::force_fail(reason)` | Flow fails with a business reason. |

- `StepMovement::to_with_options(&Step, input, options)` overrides options for
  one execution. If every transition needs it, put it in `Step::options`.
- `.cancel_sibling_step(&Step)` cancels executions of that Step type scheduled
  by the same predecessor. `.cancel_step(&Step)` cancels every execution of
  it. Steps created by the same decision are not canceled.
- Clone owned input only where two movements need it.

## StepOptions

```rust
fn options(&self) -> StepOptions<Self::Input> {
    StepOptions::new()
        .execute_method_timeout(Duration::from_secs(30))
        .execute_retry(RetryPolicy::new().maximum_attempts(5))
        .execute_durability(StepDurability::Sync)
        .execute_lock(ORDER_STATUS.lock())
        .on_execute_failure_proceed_to(&RecordChargeFailure)
}
```

- Timeouts: `wait_for_method_timeout`, `execute_method_timeout`,
  `heartbeat_timeout`. The Server enforces a minimum heartbeat timeout; check
  the Server reference before choosing a small value.
- Retries: `wait_for_retry`, `execute_retry` with `RetryPolicy::new()`
  (`initial_interval`, `backoff_coefficient`, `maximum_interval`,
  `maximum_attempts`, `total_duration`). Bound retries whenever a recovery
  route must eventually run.
- Failure routes: `on_execute_failure_proceed_to(&Recovery)` and
  `wait_for_failure(WaitForFailurePolicy::Proceed)`. See
  [error-handling.md](error-handling.md).
- Durability: `wait_for_durability` and `execute_durability` take
  `StepDurability::Default`, `Sync`, or `Async`. Choose from
  [../core/step.md](../core/step.md); do not infer it from the timeout. A
  Flow-wide default goes in
  `StartFlowOptions::new().config_override(FlowConfig::new().step_durability(...))`.
- Locks: `wait_for_lock` / `execute_lock` with `ATTRIBUTE.lock()` or
  `ATTRIBUTE_MAP.lock(instance)`.
- Loads: `wait_for_load_*` / `execute_load_*` for `attribute_map`,
  `attribute_map_instance(MAP.load(instance))`, `channel`, `channel_map`, and
  `channel_map_instance(MAP.load_messages(instance))`. Reading a map or pending
  messages that were not loaded fails.

## Attributes

```rust
pub static ORDER_STATUS: LazyLock<Attribute<String>> = LazyLock::new(|| {
    Attribute::new("order-status").indexed(AttributeIndex::keyword().with_key("keyword1"))
});
pub static EMAIL: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("email").sync_to_attribute_store());
static API_KEY: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("api-key").hard_delete_on_null());
pub static ORDER_NOTES: LazyLock<AttributeMap<String>> =
    LazyLock::new(|| AttributeMap::new("order-notes"));
```

- Handler access: `ATTR.get(context)?` returns `Option<T>`;
  `get_required(context)?` fails the handler when absent;
  `set(context, value)?`; `delete(context)?`. Maps add the instance:
  `ORDER_NOTES.get(context, "n1")?`, `set(context, "n1", value)?`,
  `map_size(context)?`, `all_instance_keys(context)?`. Size and keys need a
  whole-map load; to test one known key, load that instance and read it.
- Singleton Attributes are always readable. Map instances are readable only
  when loaded.
- Index: `AttributeIndex::keyword()`, `full_text()`, `keyword_array()`,
  `int()`, `double()`, `bool()`, `date_time()`, each with an explicit generic
  key such as `.with_key("keyword1")`. Keep business names on the Attribute and
  reuse typed keys across Flow types. Search limits are in the
  [Server search reference](https://docs.superdurable.io/reference/server/search).
- `sync_to_attribute_store()` projects latest values to the Flow's configured
  Attribute Store (`FlowConfig::new().attribute_store_names(...)`). The
  projection is asynchronous; never base a Flow decision on it. Serde field
  names of an AttributeMap struct become the store's columns. See the
  [Server Attribute Store reference](https://docs.superdurable.io/reference/server/attribute-store-sync).
- `hard_delete_on_null()` is for a short-lived secret such as an API key.
  Registration rejects numeric and `bool` value types, indexes, and Store
  sync; wrap a numeric secret in a struct. `delete` removes it permanently.
  Pass it at start with `StartFlowOptions::new().initial_attribute(&API_KEY, value)`.
  `wait_for_attribute_match` rejects it. Do not read-modify-write it. See
  [../core/attribute.md](../core/attribute.md).
- Map instance keys are stable business keys: non-empty and without `/`.

## Channels

- Publish from a handler: `ORDER_APPROVALS.publish(context, value)?`,
  `CHANNEL_MAP.publish(context, instance, value)?`. Outside the Flow, publish
  through an RPC; the Client has no direct publish.
- Consume by waiting: a satisfied Channel Condition removes the selected
  messages. Read them with `condition_results(context)?`.
- Manage a queue explicitly only with a load (`execute_load_channel(&QUEUE)` or
  `RPC.load_channel(&QUEUE)`): `pending_messages(context)?`,
  `find_pending_message(context, id)?`, `delete(context, message_id)?`,
  `size(context)?`.
- Pending-message reads are a snapshot, not a lock. See
  [../core/channel.md](../core/channel.md).
- An RPC that deletes a message and must abort when it is missing uses
  `RPC.is_transactional()`.

## RPCs

```rust
pub const ADD_ORDER_NOTE: Rpc<OrderNote, i64> = Rpc::new("AddOrderNote");

impl OrderFlow {
    fn approve_order(&self, context: &mut Context, approver: String) -> HandlerResult<()> {
        ORDER_APPROVALS.publish(context, approver)
    }

    fn get_order_status(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        let status = ORDER_STATUS.get(context)?.unwrap_or_else(|| "new".to_string());
        Ok(RpcResult::new(status))
    }

    fn add_order_note(
        &self,
        context: &mut Context,
        note: OrderNote,
    ) -> HandlerResult<RpcResult<i64>> {
        ORDER_NOTES.set(context, &note.note_id, note.text)?;
        let revision = ORDER_REVISION.get(context)?.unwrap_or(0) + 1;
        ORDER_REVISION.set(context, revision)?;
        Ok(RpcResult::new(revision))
    }
}
```

Calling an RPC:

```rust
client.invoke_rpc(&flow_id, APPROVE_ORDER, "alice".to_string())?;
let status = client.invoke_rpc_without_input(&flow_id, GET_ORDER_STATUS)?;
```

| Registration | Handler signature |
| --- | --- |
| `function(RPC, Self::m)` | `fn(&self, &mut Context, In) -> HandlerResult<RpcResult<Out>>` |
| `function_without_input(RPC, Self::m)` | `fn(&self, &mut Context) -> HandlerResult<RpcResult<Out>>` |
| `procedure(RPC, Self::m)` | `fn(&self, &mut Context, In) -> HandlerResult<()>` |
| `procedure_without_input(RPC, Self::m)` | `fn(&self, &mut Context) -> HandlerResult<()>` |

- Registration options chain on the const: `.timeout(d)`, `.lock(ATTR.lock())`,
  `.is_transactional()`, `.load_attribute_map(&M)`,
  `.load_attribute_map_instance(M.load(i))`, `.load_channel(&C)`,
  `.load_channel_map(&CM)`, `.load_channel_map_instance(CM.load_messages(i))`.
- Locks and loads chosen at call time go in `RpcInvokeOptions` with
  `invoke_rpc_with_options`. For read-modify-write of one map instance, add
  the same instance as both lock and load:

```rust
client.invoke_rpc_with_options(
    flow_id,
    ADD_ORDER_NOTE,
    note,
    RpcInvokeOptions::new()
        .lock_attribute_map_instance(ORDER_NOTES.lock(bucket.clone()))
        .load_attribute_map_instance(ORDER_NOTES.load(bucket)),
)
```

- Which RPCs can read a closed Flow is in [../core/rpc.md](../core/rpc.md).
- `RpcResult::new(output).then(StepMovement::to(&Step, input))` starts a Step.
  `.cancel_step(&Step)` cancels Steps Flow-wide; an RPC has no siblings.
- A lock conflict fails at once with `SdkError::RpcLockConflict`. Retry it
  with backoff only if the RPC is idempotent.
- Keep RPC handlers short and free of external calls. Start a Step for
  external work.

## Streams

```rust
pub static ORDER_PROGRESS: LazyLock<Stream<String>> =
    LazyLock::new(|| Stream::new("order-progress", 1 << 20));

// inside execute
let progress = ORDER_PROGRESS.buffered_text(context)?;
progress.write(format!("page {page} rendered"))?;
```

- `STREAM.write(context, value)?` sends one message. `buffered_text` and
  `buffered_text_with_options` batch text deltas such as model tokens. Create
  one writer per invocation and propagate its errors.
- Buffered writes are asynchronous, and Streams are best effort. Keep
  authoritative state in Attributes.
- Readers use `client.read_stream_with_timeout(flow_id, &STREAM, resume_token, timeout)`
  for forward long-polling reads and `client.list_stream_messages(flow_id,
  &STREAM, page_size, before_page_token)` for newest-first pages. Pass tokens
  back unchanged; start with an empty token.
- The Server rejects a page size outside its allowed range with
  INVALID_ARGUMENT; it does not clamp it. The range is in the
  [Server reference](https://docs.superdurable.io/reference/server/configuration).
- External writers use `client.write_stream(flow_id, &STREAM, source, value)`.

## Timers

`Timer::by_duration(duration)` returns a durable Condition; add `.with_id(...)`
to name it. It survives Worker restarts. Use it for deadlines, reminders, and
inactivity windows; never `thread::sleep` for business time. A Timer fires no
earlier than its duration; do not assert exact precision.
`client.skip_timer(flow_id, StepExecutionId::of(&Step), TimerId::by_condition_id("..."))`
fires a pending Timer early.

## SubFlows

```rust
#[derive(Default)]
struct StartShipment;

impl Step for StartShipment {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, order_id: Self::Input) -> HandlerResult<Wait> {
        let condition = SubFlow::run_with_options(
            &ShipmentFlow::default(),
            order_id,
            SubFlowOptions::new().timeout(Duration::from_secs(3600)),
        )
        .map_err(HandlerError::from_error)?;
        Ok(Wait::until(condition))
    }

    fn execute(&self, context: &mut Context, _order_id: Self::Input) -> HandlerResult<StepDecision> {
        let result = SubFlow::condition_result(context)?;
        let output: String = result.single_output().map_err(HandlerError::from_error)?;
        Ok(StepDecision::graceful_complete(output))
    }
}
```

- Register the child Flow in the same Registry as the parent.
- `SubFlow::condition_result_at(context, index)?` and
  `SubFlow::flow_id_at(context, index)?` read one of several children. Check
  `result.status()` before decoding output.
- Decide the child timeout, timeout policy, and reuse policy explicitly with
  `SubFlowOptions`.

## Flow timeout handler

Return `Some(Self::handle_timeout)` from `timeout_handler()` to make the
Flow's timeout run a business decision instead of failing. Set the timeout in
`StartFlowOptions::timeout`. Configure the handler's retries, locks, loads,
and failure route with `FlowTimeoutHandlerOptions`, passed to
`StartFlowOptions::timeout_handler_options` (or the `SubFlowOptions` method of
the same name) together with `timeout_policy`. Keep it bounded and
idempotent.

```rust
fn handle_timeout(&self, context: &mut Context) -> HandlerResult<StepDecision> {
    ORDER_STATUS.set(context, "timed-out".to_string())?;
    Ok(StepDecision::force_fail("order deadline expired"))
}
```

## Heartbeats, cancellation, and invocation data

```rust
fn options(&self) -> StepOptions<Self::Input> {
    // The summary instance is read below, so Execute must load it.
    StepOptions::new().execute_load_attribute_map_instance(ORDER_NOTES.load("summary"))
}

fn execute(&self, context: &mut Context, pages: Self::Input) -> HandlerResult<StepDecision> {
    let progress = ORDER_PROGRESS.buffered_text(context)?;
    let first_page = context.last_heartbeat_value::<u32>()?.unwrap_or(0);
    for page in first_page..pages {
        if context.is_cancelled() {
            return Ok(StepDecision::dead_end());
        }
        thread::sleep(Duration::from_millis(100));
        progress.write(format!("page {page} rendered"))?;
        context.record_heartbeat_value(page + 1)?;
    }
    let summary = ORDER_NOTES.get(context, "summary")?;
    Ok(StepDecision::graceful_complete(summary.unwrap_or_default()))
}
```

- A long handler calls `record_heartbeat_value(checkpoint)?` (or
  `record_heartbeat()?`) within its heartbeat timeout. The next attempt reads
  it with `last_heartbeat_value::<T>()?`.
- A checkpoint is resume state, not a commit. Record it only after the work it
  describes is complete or safe to repeat.
- Cancellation is cooperative: check `context.is_cancelled()` between bounded
  units of work.
- `Context` also exposes `flow_id()`, `run_id()`, `step_execution_id()`,
  `attempt()`, and `first_attempt_at()`. Use them for logs and idempotency
  keys.
- `context.record_event(name, value)?` adds a business milestone to Flow
  history.
- `set_step_execution_local` passes a value from `wait_for` to `execute` of
  the same Step execution. Dex commits it with the method's result and stores
  it in the Step record, so `execute` reads it even on another Worker.
  `step_execution_local` returns an error when the key is absent. Only that
  Step execution can read it; use an Attribute for state another Step or a
  Client needs. See [Step execution local values](../core/step.md#step-execution-local-values).

## Client

Use the Client at the service boundary. Every call blocks; keep it off async
executor threads.

| Need | Rust |
| --- | --- |
| Start | `client.start_flow(&OrderFlow::default(), flow_id, input)?` returns the run ID |
| Start with options | `client.start_flow_with_options(&flow, flow_id, input, StartFlowOptions::new()...)` |
| Idempotent start | `.request_id(stable_id).ignore_already_started(true)` |
| Call an RPC | `invoke_rpc`, `invoke_rpc_without_input`, and `*_with_options` |
| Read committed Attributes | `client.get_attributes(flow_id, (&ATTR, &MAP.instance(key)))?` |
| Wait for a value | `wait_for_attribute_match` / `wait_for_attribute_map_instance_match` with `AttributeMatch` |
| Wait for a Step | `client.wait_for_step_completion(flow_id, StepExecutionId::of(&Step), options)` |
| Wait for the end | `client.wait_for_flow_with_timeout(flow_id, timeout)` |
| Current status | `client.describe_flow(flow_id)` |
| Search | `client.search_flows(query, page_size)` |
| Stop | `client.stop_flow(flow_id, StopFlowOptions::cancel())` |
| Connectivity | `client.health_check()` |

- `start_flow` checks that the Flow value's Rust type is the registered one;
  pass any instance of it, such as `&OrderFlow::default()`.
- Start options also set the timeout, ID reuse policy, initial Attributes,
  and config override.
- Never set a positive start delay (`start_delay`). The Server rejects it with
  INVALID_ARGUMENT. To delay the first work, start the Flow at once and have
  the start Step wait on a Timer.
- The Client has no direct write. Write Flow state through an RPC.
- Wait options take `request_timeout`. `SdkError::RequestTimeout` means only
  the caller's budget ran out.
- `wait_for_flow*` returns a `FlowResult`; check `status()` before
  `single_output()`. See [../core/client.md](../core/client.md) for revision
  watermarks, `describe_flow`, and search rules.

## Client Attribute reads

`client.get_attributes(flow_id, reads)` reads committed values of registered
Attributes and AttributeMap instances in one request. It does not run Flow
code or need a Worker.

```rust
let (status, note) =
    client.get_attributes(flow_id, (&ORDER_STATUS, &ORDER_NOTES.instance("n1")))?;
let status = status.unwrap_or_else(|| "new".to_string());
```

- `reads` is a tuple of one or more reads; the result is a tuple of
  `Option<T>` in the same order. Use `(&ORDER_STATUS,)` for one value. The
  tuple size is bounded; for more values from one commit, use a read-only RPC.
- `MAP.instance(key)` selects a map instance for this call. It is a
  different type from `MAP.load(key)`, which is for handler loads.
- A read must match a registered definition: name, kind, Rust value type,
  index, Store sync, and hard-delete settings. Reuse the Flow's statics.
- A missing Flow returns `SdkError::FlowNotFound`; a closed Flow stays
  readable until purge. Retry a large-value `SdkError::ValueMapping` once.
- When to prefer a read-only RPC is in [../core/client.md](../core/client.md).

## Patterns in Rust

Choose the pattern in [../core/patterns.md](../core/patterns.md). The Rust
building blocks:

| Pattern | Rust shape | Example module |
| --- | --- | --- |
| Static parallel Steps | `go_to_many([StepMovement::to(&A, x.clone()), StepMovement::to(&B, x)])` | `patterns/parallel` |
| Dynamic parallel Steps | `go_to_many((0..n).map(\|i\| StepMovement::to(&Work, i)))` | `patterns/parallel` |
| Await all Steps | branches publish to a Channel and `dead_end()`; joiner waits `for_n(count)` | `patterns/parallel` |
| First win | winner returns `.cancel_sibling_step(&Same)` | `patterns/parallel` |
| Parallel SubFlows | `Wait::all_of(conditions)` built from `SubFlow::run` | `patterns/parallel_subflows` |
| Polling | one long `execute` loop with heartbeat and bounded retry | `patterns/polling` |
| Cron, reminder, inactivity | Timer raced with Channels in `Wait::any_of` | `patterns/cron`, `patterns/reminders`, `patterns/inactiveness_tracker` |
| Inbound event loop | RPCs publish; one Step waits on `Wait::any_of` over `replies.at_least(1)`, `resolutions.for_one()`, and a Timer, appends to the transcript, and loops back ([rules](../core/patterns.md#inbound-event-loop)) | closest: `patterns/inactiveness_tracker` |
| Execute recovery | `on_execute_failure_proceed_to(&Recovery)` | `patterns/recovery` |
| WaitFor recovery | `WaitForFailurePolicy::Proceed` plus `context.wait_for_method_failed()` | `primitives/proceed_on_wait_failure` |
| Manual recovery | recovery Step waits on retry and skip Channels | `patterns/intervention` |
| Graceful timeout | `timeout_handler` plus `StartFlowOptions::timeout` | `patterns/timeout` |
| Drain internal Channel | side Step loops until a final marker | `patterns/drain_channels` |
| Drain external publishing | `force_complete_if_channels_empty(output, fallback, [Q.when_empty()])` | `patterns/drain_channels` |
| Interruptible | RPC writes an interrupt Attribute or cancels Steps; handlers check it | `patterns/interruptible` |
| Wait for Step completion | `wait_for_step_completion` after start | `patterns/wait_for_step_completion` |
| Wait for Attribute match | locked revision Attribute plus `wait_for_attribute_match` | `products/job_post` |
| Stream progress | `buffered_text` plus `read_stream_with_timeout` | `primitives/stream` |
| Entity store | long-lived RPC-driven Flow per entity | `patterns/entity_store` |
| Chunked AttributeMap | bounded `current` instance plus immutable archives | `patterns/sequentially_chunked_attribute_map` |
| Hash-partitioned AttributeMap | stable hash picks a bucket; invocation lock and load | `patterns/hash_partitioned_attribute_map` |

Modules are under [examples/rust/src](https://github.com/superdurable/dex/tree/main/examples/rust/src).
Copy the shape, not the business types.

An entity Flow that runs fallible background work registers the work and
recovery Steps with `StepList::empty().and(...)`, starts them from an RPC with
`RpcResult::new(out).then(StepMovement::to(...))`, and ends each with
`StepDecision::dead_end()` so the entity stays open.

### Drain an externally published queue

```rust
fn options(&self) -> StepOptions<Self::Input> {
    StepOptions::new().execute_load_channel(&EXTERNAL_QUEUE)
}

fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
    Ok(Wait::until(EXTERNAL_QUEUE.for_one()))
}

fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
    for item in EXTERNAL_QUEUE.condition_results(context)? {
        context.record_event("drained", item)?;
    }
    Ok(StepDecision::force_complete_if_channels_empty(
        "drained".to_string(),
        StepMovement::to(&DrainQueue, ()),
        [EXTERNAL_QUEUE.when_empty()],
    ))
}
```

### Polling an external job

Follow the polling rules in [../core/patterns.md](../core/patterns.md).

```rust
fn options(&self) -> StepOptions<Self::Input> {
    StepOptions::new()
        .execute_method_timeout(MAX_JOB_WAIT)
        .heartbeat_timeout(Duration::from_secs(60))
        .execute_retry(RetryPolicy::new().maximum_attempts(3).total_duration(MAX_JOB_WAIT))
        .on_execute_failure_proceed_to(&RecordJobFailure)
}

fn execute(&self, context: &mut Context, job_id: Self::Input) -> HandlerResult<StepDecision> {
    let mut reported = context.last_heartbeat_value::<JobState>()?;
    loop {
        let state = fetch_job_state(&job_id)?;
        match state {
            JobState::Succeeded => return Ok(StepDecision::graceful_complete(job_id)),
            JobState::Failed => return Ok(StepDecision::force_fail(format!("job {job_id} failed"))),
            JobState::Running => {}
        }
        if reported != Some(state) {
            reported = Some(state);
            context.record_event("job-state", state)?;
        }
        context.record_heartbeat_value(state)?;
        thread::sleep(POLL_INTERVAL);
    }
}
```

- "Not ready" stays inside the loop. Only transient provider failures return
  `Err`, so Step retry runs again.
- Give each provider call its own timeout, and keep
  `POLL_INTERVAL + call timeout <= heartbeat timeout - 10 s`. With the
  one-minute default and a 10-second call timeout, `POLL_INTERVAL` is at most
  40 seconds. Keep the one-minute default unless a healthy call is silent
  longer.
- Keep `maximum_attempts` small, such as 3 to 5. `total_duration` bounds the
  time; never use a large attempt count as a time bound.
- `execute_method_timeout`, `total_duration`, and the failure route bound the
  whole wait. Never loop through a WaitFor Timer, `HandlerError::retry_after`,
  or a hand-written deadline.
- The full example, with a Stream for status changes, is
  [polling/flow.rs](https://github.com/superdurable/dex/blob/main/examples/rust/src/patterns/polling/flow.rs).
