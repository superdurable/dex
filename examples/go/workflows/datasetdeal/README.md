# Dataset Deal DSL

This example lets one seller define a reusable finite-state deal process for a
dataset product. Each buyer starts an independent deal execution from the same
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
  "processID": "dataset-deal-v1",
  "initialState": "buyer-negotiation",
  "initialStateData": {"acceptedProposedPrice": "false"},
  "states": [
    {
      "name": "seller-counteroffer",
      "preCondition": {"name": "seller-price-response"},
      "preActions": ["transferMoneyFromBuyerToSeller"],
      "postActions": ["transportSampleDatasetToBuyer"],
      "postCondition": {
        "decision": {
          "key": "acceptedProposedPrice",
          "cases": [{"equals": "true", "goToState": "process-sample-order"}],
          "elseState": "seller-counteroffer"
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
- `transportFullDatasetToBuyer`
- `transportSampleDatasetToBuyer`

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

Register these Temporal keyword search attributes before starting executions:

- `ProcessID`
- `BuyerID`
- `CurrentState`
- `PendingPreConditionState`
- `PendingPreConditionName`

Durable attribute keys are `stateData`, `processDefinition`, `processID`,
`buyerID`, `currentState`, `currentActionIndexToExecute`,
`pendingPreConditionState`, and `pendingPreConditionName`. The channel-map key
is `conditionMessages`.

`schema.sql` creates only `dataset_deal_processes`. The Go server runs it
idempotently at startup.

## REST API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/dataset-deal/processes` | Validate and create a process |
| `GET` | `/api/dataset-deal/processes/:processID` | Read a process |
| `POST` | `/api/dataset-deal/executions` | Start a buyer execution and wait for initialization |
| `GET` | `/api/dataset-deal/executions` | List all executions |
| `GET` | `/api/dataset-deal/executions?buyerID=buyer1` | List one buyer's executions |
| `GET` | `/api/dataset-deal/executions/:flowID` | Read one execution snapshot |
| `POST` | `/api/dataset-deal/executions/:flowID/channels/:conditionName` | Merge external condition data |

The execution flow ID is `<processID>-<UUID>`. The start API initializes the
buyer as an indexed attribute and waits for `datasetdeal.initializeStep` to
complete.

## Run and verify

From `examples/go`:

```bash
make datasetDealDemo
```

The script starts PostgreSQL with Docker Compose, initializes the schema,
starts Dex with its bundled Temporal dev server, registers search attributes,
starts the Go worker/API, and drives three executions:

- buyer 1 rejects one counteroffer, accepts the next, then buys the full data;
- buyer 2 accepts and requests a sample refund;
- buyer 3 remains pending at the initial proposal channel.

It also verifies the all-buyers and buyer-filtered list APIs. Services stop
when the script exits. Keep them running for UI inspection with:

```bash
KEEP_DATASET_DEAL_DEMO=1 make datasetDealDemo
```

The seller/buyer UI is then available at the URL printed by the script.

## Tests

`make e2eTests` runs the same comprehensive flow against real Dex, Temporal,
WorkerService, FlowService, PostgreSQL, REST handlers, and indexed search.

## Documentation

The comprehensive JSON template is
`cmd/server/dex/ui/dataset-deal/comprehensive-process.json`.

## UI/UX

The embedded UI provides seller DSL editing and buyer 1/2/3 execution controls.
