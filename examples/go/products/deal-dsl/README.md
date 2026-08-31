# Deal DSL

This example lets one seller define a reusable finite-state deal process for a
item. Each buyer starts an independent deal execution from the same
stored process.

The seller's DSL is stored as JSON in PostgreSQL. Initialization copies it into
a Dex attribute, and four fixed steps interpret that immutable execution
snapshot. Seller-authored states do not require dynamic SDK registration:

```text
initialize → pre-condition → execute one action → post-condition
                  ↑                  │                 │
                  └──────────────────┴──── next state ─┘
```

- `preCondition` optionally waits for one external channel message before the
  state becomes pending.
- `preActions` run in order before `currentState` changes.
- `postActions` run in order after `currentState` changes.
- `postCondition` may wait for an external message, then evaluates structured
  equality cases against `stateData` and selects the next state.
- Omitting `postCondition` completes the deal execution.

The example uses `map[string]string` for `stateData` to keep the visual builder
generic. A production deal system should use a versioned, strongly typed model
for validation, migrations, and action inputs.

## DSL shape

```json
{
  "processID": "deal-dsl-v1",
  "initialState": "buyer-negotiation",
  "initialStateData": {"acceptedProposedPrice": "false"},
  "states": [
    {
      "name": "seller-counteroffer",
      "preCondition": {"name": "seller-price-response"},
      "preActions": ["transferMoneyFromBuyerToSeller"],
      "postActions": ["deliverItemSampleToBuyer"],
      "postCondition": {
        "decision": {
          "key": "acceptedProposedPrice",
          "cases": [{"equals": "true", "goToState": "process-item-sample"}],
          "elseState": "buyer-negotiation"
        }
      }
    }
  ]
}
```

State and external-condition names must be unique within a process. Conditions
use instances of the `ConditionMessages` channel map. A message and each action
output merge string key/value pairs into `stateData`.

The built-in actions only log simulated work:

- `transferMoneyFromBuyerToSeller`
- `transferMoneyFromSellerToBuyer`
- `deliverItemToBuyer`
- `deliverItemSampleToBuyer`

`currentActionIndexToExecute` schedules exactly one action per step execution,
then loops until the ordered list is complete.

## Persistence and search

PostgreSQL stores only seller-authored process definitions. The initialize step
loads and validates one definition, then stores it in the `processDefinition`
Dex attribute. Later steps and channel validation use that snapshot, so editing
the PostgreSQL definition cannot change an existing execution.

Execution status and state come entirely from Dex visibility plus one batched
attribute read per flow. Visibility is eventually consistent; the REST API,
UI, and E2E checks use bounded retries when a new execution has not appeared.
Seller ProcessID filters and buyer ProcessID filters are combined directly with
the buyer's `BuyerID` in Dex `SearchFlows` queries.

The persistence schema declares these keyword Indexed Attributes, which the
Worker synchronizes automatically before starting:

- `ProcessID`
- `BuyerID`
- `CurrentState`
- `PendingPreConditionState`
- `PendingPreConditionName`

Durable attribute keys are `stateData`, `processDefinition`, `processID`,
`buyerID`, `currentState`, `currentActionIndexToExecute`,
`pendingPreConditionState`, and `pendingPreConditionName`. The channel-map key
is `conditionMessages`.

`schema.sql` creates only `deal_dsl_processes`. The Deal DSL server
runs it idempotently at startup.

## REST API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/products/deal-dsl/api/processes` | Validate and create a process |
| `GET` | `/products/deal-dsl/api/processes` | List seller process definitions |
| `GET` | `/products/deal-dsl/api/processes/:processID` | Read a process |
| `PUT` | `/products/deal-dsl/api/processes/:processID` | Validate and update a process |
| `POST` | `/products/deal-dsl/api/executions` | Start a buyer execution and wait for initialization |
| `GET` | `/products/deal-dsl/api/executions?processID=deal-v1` | List one process's executions |
| `GET` | `/products/deal-dsl/api/executions?buyerID=buyer1&processID=deal-v1` | List one buyer's matching executions |
| `GET` | `/products/deal-dsl/api/executions/:flowID` | Read one execution snapshot |
| `POST` | `/products/deal-dsl/api/executions/:flowID/channels/:conditionName` | Merge external condition data |

The execution flow ID is `<processID>-<UUID>`. The start API initializes the
buyer as an indexed attribute and waits for `dealdsl.initializeStep` to
complete.

## Run and verify

Deal DSL is **not** part of `./dex-samples`. Build `dex-deal-dsl` and
start PostgreSQL first.

From `examples/go`:

```bash
docker compose -f deal-dsl/docker-compose.yml up -d --wait
dexcli dev
make bins
./dex-deal-dsl
```

The binary uses the same default HTTP (`127.0.0.1:8080`) and Worker
(`127.0.0.1:8803`) ports as `dex-samples`. Override `DEAL_DSL_POSTGRES_URL`
when Postgres is not `postgres://deal_dsl:deal_dsl@127.0.0.1:15432/deal_dsl?sslmode=disable`.

Or run the demo script, which starts PostgreSQL, Dex, and `dex-deal-dsl`
on Deal DSL's own ports:

```bash
make dealDSLDemo
```

The script starts PostgreSQL with Docker Compose, initializes the schema,
starts Dex, lets the Worker synchronize Indexed Attributes,
starts the Go worker/API, and drives three executions:

- buyer 1 rejects one counteroffer, accepts the next, then buys the full data;
- buyer 2 accepts and requests a sample refund;
- buyer 3 remains pending at the initial proposal channel.

It also verifies the all-buyers and buyer-filtered list APIs. Services stop
when the script exits. Keep them running for UI inspection with:

```bash
KEEP_DEAL_DSL_DEMO=1 make dealDSLDemo
```

To drive an already-running example API without managing its services:

```bash
DEAL_DSL_API_URL=http://127.0.0.1:20804 make triggerDealDSLDemo
```

The trigger-only script creates or updates the comprehensive process, completes
the full-purchase and refund paths, and leaves buyer 3 pending. Set
`DEAL_DSL_PROCESS_ID` to use a different process ID.

The seller dashboard lists processes beside executions. Buyer dashboards list
only their executions. Process pages provide an editable state graph;
execution pages render the immutable graph, highlight `currentState`, show
runtime data, and provide the shared condition-message form.

## Tests

`make e2eTests` runs the same comprehensive flow against real Dex, Temporal,
WorkerService, FlowService, PostgreSQL, REST handlers, and indexed search. That
path starts Deal DSL PostgreSQL only for these tests, not for the default
`dex-samples` suite.

## Documentation

The comprehensive JSON template is
`products/deal-dsl/ui/deal-dsl/comprehensive-process.json`.

## UI/UX

The embedded UI provides seller DSL editing and buyer 1/2/3 execution controls.
