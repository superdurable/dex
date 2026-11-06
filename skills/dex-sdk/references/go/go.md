# Go

Read this page first for a Go Dex application. It covers the Go rules only.
The concepts live in core: [Flow](../core/flow.md), [Step](../core/step.md),
[Attribute](../core/attribute.md), [Channel](../core/channel.md),
[RPC](../core/rpc.md), [Stream](../core/stream.md), [Timer](../core/timer.md),
[SubFlow](../core/subflow.md), [Client](../core/client.md),
[Connector](../core/connector.md), [application](../core/application.md),
[error handling](../core/error-handling.md), [testing](../core/testing.md), and
[patterns](../core/patterns.md).

| Task | Go page |
| --- | --- |
| Write Flows, Steps, state, RPCs, Streams, SubFlows, Client calls | [primitives](primitives.md) |
| Apply a design pattern | [patterns](patterns.md) |
| Classify errors, start idempotently, recover | [error handling](error-handling.md) |
| Test against a real Dex Server | [testing](testing.md) |

## Version and reference

Find the SDK version the project uses before you rely on an API detail:

```bash
go list -m github.com/superdurable/dex/sdk-go
```

Open the [Go SDK reference](https://docs.superdurable.io/reference/go/) and
select that minor version in the left-sidebar version selector. The installed
module's doc comments are the API authority. Read them with
`go doc github.com/superdurable/dex/sdk-go/dex.<Name>`. They give every
parameter, default, unit, and error. This skill gives only the rules that hold
across versions.

- The import path is `github.com/superdurable/dex/sdk-go/dex`. Pointer helpers
  are in `github.com/superdurable/dex/sdk-go/dex/ptr`.
- The Worker and Client check the Server's protocol at startup or on the first
  call. An incompatible Server fails that call. Upgrade the Server before the
  SDK.

## Project shape

- Put Flow and Step definitions in domain packages. Compose every Flow in one
  registry package. Keep HTTP handlers and `cmd/` bootstrap outside Flow types.
- Keep schema and Flow packages below the registry package. This avoids import
  cycles.
- Inject business services through Flow constructors. A Flow passes them into
  its Step structs in `GetSteps`.
- Create one `blobcache.Cache` and one `dex.Registry` per process. The Worker
  and the Client share both.
- A Step that starts or calls other Flows needs the Client, but the Client
  needs the Registry first. Pass a late-bound provider, such as
  `func() *dex.Client`, into the Flow constructor.
- The runnable layout is [examples/go](https://github.com/superdurable/dex/tree/main/examples/go):
  `primitives/`, `patterns/`, `products/`, `registry/`, and `cmd/server/`.

## Bootstrap

```go
type Application struct {
	cache        *blobcache.Cache
	worker       *dex.Worker
	client       *dex.Client
	workerResult chan error
}

func StartApplication(flowServiceAddress string, cacheDir string, shipping ShippingService) (*Application, error) {
	cache, err := blobcache.New(&blobcache.Config{Dir: cacheDir, MaxBytes: 1 << 30})
	if err != nil {
		return nil, err
	}
	registry, err := dex.NewRegistry([]dex.Flow{NewOrderApprovalFlow(shipping)})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress,
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: 16},
	})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress,
	})
	if err != nil {
		return nil, errors.Join(err, cache.Close())
	}
	application := &Application{cache: cache, worker: worker, client: client, workerResult: make(chan error, 1)}
	go func() { application.workerResult <- worker.Start() }()

	startupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := worker.WaitPullRegistered(startupContext); err != nil {
		return nil, errors.Join(err, application.Stop(context.Background()))
	}
	return application, nil
}

func (application *Application) Stop(ctx context.Context) error {
	clientErr := application.client.Close()
	stopErr := application.worker.Stop(ctx)
	var runErr error
	select {
	case runErr = <-application.workerResult:
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	return errors.Join(clientErr, stopErr, runErr, application.cache.Close())
}
```

`blobcache` is `github.com/superdurable/dex/blob-cache-go/blobcache`. The
runnable versions are the [pull Worker example](https://github.com/superdurable/dex/blob/main/examples/go/production/pull-workers/worker.go)
and the [examples bootstrap](https://github.com/superdurable/dex/blob/main/examples/go/cmd/server/dex/dex.go).

## Worker lifecycle

- The Worker opens outbound streams to the Dex Server; it needs no listen
  address.
- `Worker.Start()` blocks until the Worker stops. Run it on a goroutine and
  keep its result.
- Gate traffic on `WaitPullRegistered(ctx)` with a deadline. Serve readiness
  from `worker.PullRegistrationState().IsReady` plus application health.
- `Worker.Stop(ctx)` takes no new tasks and lets running handlers finish. It
  cancels handlers still running at the drain deadline, and Dex fails those
  attempts as possibly run. Handlers must honor `ctx` cancellation.
- Close the Client, stop the Worker, then close the BlobCache.
- `PullWorkerOptions.MaxConcurrentTasks` is the number of execution slots. A
  slot runs every task it receives; Dex never declines a Step for capacity.
  `ReservedRPCTasks` keeps some slots for RPCs only, so RPCs stay responsive
  while long Steps fill the rest. It is a `*int`; use `ptr.Any(0)` to opt out.
  Defaults and Server limits are in the [Go Workers reference](https://docs.superdurable.io/reference/go/workers).

## Authentication and TLS

- Set `ClientOptions.Credential` and `WorkerOptions.Credential` separately.
  One credential value may serve several Clients and Workers.
- `dex.FileCredential(path)` reads a rotating bearer-token file.
  `dex.APIKeyCredential(...)` exchanges an API key for tokens. A custom
  `dex.Credential` returns the current token for each call.
- Set `FlowServiceTLS` on each options struct for TLS. Nil keeps plaintext.
  TLS never falls back to plaintext.
- Never put a token or API key in Flow input, ordinary Attributes, Streams, or
  logs.
- See the [authentication reference](https://docs.superdurable.io/reference/server/authentication).

## Default Flow and Step type names

- The Flow type and Step type are durable identities. Each run stores them.
- The default type is the Go type name without its package or pointer:
  `*orders.OrderFlow` registers as `OrderFlow`.
- Name each Flow type after its domain, such as `ApprovalFlow`. Never name it
  just `Flow`.
- Flow types are unique within one Registry. Step types are unique within one
  Flow. `NewRegistry` rejects two same-named Go types from different packages.
- Embed `dex.FlowDefaults` and the Step defaults. Do not implement
  `GetFlowType` or `GetStepType` for style, display labels, or a Dex Web
  mismatch.
- Exceptions:
  - A generic Flow or Step type has no default name. It must return one
    compile-time string.
  - A production Flow or Step whose Go type you rename keeps its durable
    identity. Return the exact type string that running executions already
    use. Read it from the deployed registration or real runs.
- Prove a rename by resuming an execution started on the old code. A new
  execution proves nothing.

## Types and payloads

- Definitions are generic and typed: `dex.DefineAttribute[T]`,
  `dex.DefineChannel[T]`, `dex.DefineStream[T]`, `dex.Step[IN]`,
  `dex.RPCResult[OUT]`.
- Payloads cross the wire as JSON through `encoding/json`. Use structs with
  exported fields and `json` tags. Concrete types beat `any`.
- Use `dex.None` for "no input" or "no output", and pass `nil` for it.
- A nil Attribute write deletes the Attribute. A nil Flow output is
  discarded. Return a result struct when "no output" must differ from a null
  result.
- A typed value that must distinguish "omitted" from zero uses a pointer field.
  Build pointers with `ptr.Any(value)`.
- Open Flows resume on new code, so `json` field names are durable. Add
  fields; never rename or repurpose one.

## Connectors

- Use a released official connector only. Go has the full Connector SDK.
- At startup, load the project configuration once with
  `projectconfig.LoadFromEnvironment(ctx)` from the Connector SDK's `sdkgo/projectconfig`.
  Open each connection with the connector package's generated
  `NewProjectConnection(project, "connection-name")`.
- `MapToOperationInput` is a pure function from the Step input to the
  provider input. Set `ResultAttribute` only when a path outside the
  transition chain must read the raw result.
- A Trigger's filter, Flow ID resolver, and input mapper are pure functions.
- Runnable examples: [connector factory](https://github.com/superdurable/dex/blob/main/examples/go/products/connector-factory/workflow.go)
  and the [AI agent Slack wiring](https://github.com/superdurable/dex/blob/main/examples/go/products/ai-agent/slack.go).

## Dex Web and FDG metadata

- The FDG 2.0 analyzer reads Go source for the full Dex Web management
  metadata: groups, explanations, Indexed Attribute presentation, Summary and
  Display RPCs, and Actions.
- Every Step type has one `// dex:group ...` directive and exactly one
  `// dex:explanation text:"..."` directive above its type. The explanation is
  one sentence that says what the Step does.
- An RPC that Dex Web calls is named by `RPCOptions.Name` when set. That name
  must be a compile-time string. Do not rename the Summary and Display view
  RPCs.
- Validate with `dexcli visualize <source> --schema-version 2.0`. See the
  [Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization).

## Go gotchas

- Define Attributes, Channels, Streams, and maps once at package scope. A
  definition carries identity.
- Register every Step that a decision, failure route, or RPC can reach, and
  register both parent and child Flows.
- `GoTo(shipOrder{}, input)` selects the Step by type. The registered value,
  with its injected services, runs.
- Embed `StepDefaultsNoWaitFor[T]` only for a Step with no wait.
- Return every error from a Context operation: Attribute, Channel, Stream,
  heartbeat. A failed write did not commit.
- `Attribute.Get` returns `*dex.AttributeNotFoundError` for an unset value.
  Match it with `errors.As`; do not treat the zero value as "unset".
- Do not coordinate durable work with goroutines, mutexes, channels, or maps.
  They vanish with the Worker.
- `time.Sleep` in `Execute` holds the attempt. Durable waits use a Timer in
  `WaitFor`. Sleep only between rounds of a heartbeating polling loop.
- Give every Client call a bounded `context.Context`.
- Map instance names are non-empty and contain no `/`.

## Completion checklist

- [ ] Every Flow, Step, Attribute, Channel, Stream, and map is registered.
- [ ] Type names follow the default-name rule.
- [ ] Every error is returned or matched with `errors.As`; none is parsed from text.
- [ ] Every Client call has a deadline.
- [ ] External effects use an idempotency key from durable identity.
- [ ] Tests run against a real Dex Server with unique Flow IDs.
- [ ] Application code uses only the public SDK.
