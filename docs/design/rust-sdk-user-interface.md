# Rust SDK User Interface

Status: proposed through compile contracts.

## Principles

- User handlers are synchronous.
- Flow start input, Step input, RPC input/output, Attributes, and Channels retain
  their Rust types.
- User code derives `serde::Serialize` and `serde::Deserialize`; it does not
  provide codecs at each SDK call site.
- Durable Flow and Step names default to Rust type names and may be overridden.
- Runtime internals may erase types only after the public API checks them.
- Calls should read naturally in Rust rather than reproduce Java syntax.

## Flow and Step

`Flow::StartInput` binds a Flow to its start Step at compile time:

```rust
struct OrderFlow {
    validate: ValidateOrder,
    charge: ChargeOrder,
}

impl Flow for OrderFlow {
    type StartInput = Order;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.validate).and(&self.charge)
    }
}
```

`StepDecision::go_to` accepts only the target Step's input type:

```rust
impl Step for ValidateOrder {
    type Input = Order;

    fn execute(
        &self,
        context: &mut Context,
        order: Order,
    ) -> HandlerResult<StepDecision> {
        self.status.set(context, "validated".to_string())?;
        Ok(StepDecision::go_to(&self.charge, ChargeInput::from(order)))
    }
}
```

Execute-only Steps omit `wait_for`. Wait expressions use domain nouns:

```rust
fn wait_for(&self, _context: &mut Context, _input: OrderId) -> HandlerResult<Wait> {
    Ok(Wait::any_of([
        self.approved.for_one(),
        Timer::by_duration(Duration::from_secs(30)).with_id("approval-timeout"),
    ]))
}
```

## Persistence

Definitions carry their value types. Persistence operations therefore do not
take runtime type tokens or codecs:

```rust
struct OrderFlow {
    status: Attribute<String>,
    attempts: AttributeMap<u32>,
    commands: Channel<Command>,
}

fn persistence(&self) -> PersistenceSchema {
    PersistenceSchema::new()
        .attribute(&self.status)
        .attribute_map(&self.attempts)
        .channel(&self.commands)
}
```

Reads return `Option<T>` because an Attribute may be absent. Handlers can use
`get_required` when absence is an application error.

## RPC

Rust uses typed RPC tokens instead of reflection or proxy stubs:

```rust
impl OrderFlow {
    const GET_ORDER: Rpc<OrderId, OrderView> = Rpc::new("get_order");

    fn get_order(
        &self,
        context: &mut Context,
        order_id: OrderId,
    ) -> HandlerResult<RpcResult<OrderView>> {
        Ok(RpcResult::new(self.load(context, order_id)?))
    }
}

fn rpcs(&self) -> RpcList<Self> {
    RpcList::new().function(Self::GET_ORDER, Self::get_order)
}

let order: OrderView = client.invoke_rpc(flow_id, OrderFlow::GET_ORDER, order_id)?;
```

RPC registration checks that the method signature agrees with the token.
Attribute and AttributeMap locks are explicit options on the registered token.

## Runtime construction

Client and Worker share an `Arc<BlobCache>` and cloned Registry metadata:

```rust
let registry = Registry::new().register(OrderFlow::new());
let cache = Arc::new(BlobCache::open(cache_config)?);
let client = Client::new(registry.clone(), cache.clone(), ClientOptions::new());
let worker = Worker::new(registry, cache, WorkerOptions::new());
```

## Current scope

The current crate defines and type-checks the public contracts. Client transport,
Registry erasure, WorkerService dispatch, and value encoding deliberately return
not-implemented errors until their runtime phases are implemented.

## Tests

The `dex-sdk` IWF compatibility target ports the Java workflow definitions and
client assertions as compile contracts. `cargo test --no-run` validates the user
experience without claiming that the unfinished runtime passes E2E scenarios.

## Documentation

Keep this document and `sdk-rust/README.md` synchronized with public API changes.

## UI/UX

N/A: no in-repo web UI.
