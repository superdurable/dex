# Dataset Deal DSL

This example lets one seller define a reusable finite-state deal process for a
dataset product. Each buyer starts an independent deal execution from the same
stored process.

The seller's DSL and each execution are stored in PostgreSQL. Each short Dex
Run processes one trigger against the execution's immutable process snapshot.
Seller-authored states do not require dynamic SDK registration:

```text
trigger → advance state → execute one action → advance state
             ↑                     │                 │
             └── next state ─────┴── next action ──┘
```

- `preCondition` optionally pauses the execution before the state is entered.
- `preActions` run in order before `currentState` changes.
- `postActions` run in order after `currentState` changes.
- `postCondition` may pause for an external message, then evaluates structured
  equality cases against `stateData` and selects the next state.
- Omitting `postCondition` completes the deal execution.

Starting an execution and submitting a pre/post condition each create a new Dex
Run under the same FlowID. Runs finish at the next external condition or the
terminal state; they never remain open waiting for a channel.

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
          "elseState": "buyer-negotiation"
        }
      }
    }
  ]
}
```

State and external-condition names must be unique within a process. A condition
message and each action output merge string key/value pairs into `stateData`.

The built-in actions only log simulated work:

- `transferMoneyFromBuyerToSeller`
- `transferMoneyFromSellerToBuyer`
- `transportFullDatasetToBuyer`
- `transportSampleDatasetToBuyer`

Each action has its own step execution and PostgreSQL commit. The action receives
the RunID and StepExecutionID as an idempotency key. A retry whose step ID was
already committed rebuilds the next movement without applying the action again.

## Persistence and execution state

PostgreSQL stores seller-authored process definitions and execution rows. The
start trigger loads and validates one definition, then copies it into the
execution row. Later triggers use that snapshot, so editing the seller's process
cannot change an existing execution.

PostgreSQL is the execution source of truth. `PROCESSING` means a trigger Run is
advancing, `WAITING` means an external pre/post condition is required, and
`COMPLETED` means a terminal state was reached. Version-checked updates serialize
each state/action commit. A repeated StepExecutionID is treated as a replay.

Execution list filters query PostgreSQL directly and can combine:

- `buyerID`
- `processID`
- `status`
- `currentState`
- `pendingConditionName`

`schema.sql` creates both Dataset Deal tables and the indexes used by these
filters. The Go server runs it idempotently at startup.

## REST API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/dataset-deal/processes` | Validate and create a process |
| `GET` | `/api/dataset-deal/processes` | List seller process definitions |
| `GET` | `/api/dataset-deal/processes/:processID` | Read a process |
| `PUT` | `/api/dataset-deal/processes/:processID` | Validate and update a process |
| `POST` | `/api/dataset-deal/executions` | Start a buyer execution and wait for initialization |
| `GET` | `/api/dataset-deal/executions?processID=deal-v1&status=WAITING` | List matching executions |
| `GET` | `/api/dataset-deal/executions?buyerID=buyer1&currentState=review` | List one buyer's matching executions |
| `GET` | `/api/dataset-deal/executions/:flowID` | Read one execution snapshot |
| `POST` | `/api/dataset-deal/executions/:flowID/conditions/:conditionName` | Run one pending condition trigger |

The execution FlowID is `<processID>-<UUID>`. The start API waits only for the
starting trigger step, guaranteeing the execution row exists before returning.
Its remaining steps continue asynchronously. A condition API waits for its full
Run and returns the latest execution snapshot.

All triggers use the same FlowID, a new RunID and `IDReuseAllowIfNotRunning`.
Submitting while another Run is active, using the wrong condition, or submitting
after completion returns HTTP 409.

## Run and verify

From `examples/go`:

```bash
make datasetDealDemo
```

The script starts PostgreSQL with Docker Compose, initializes the schema,
starts Dex with its bundled Temporal dev server, starts the Go worker/API, and
drives three executions:

- buyer 1 rejects one counteroffer, accepts the next, then buys the full data;
- buyer 2 accepts and requests a sample refund;
- buyer 3 remains pending at the initial proposal channel.

It also verifies the all-buyers and buyer-filtered list APIs. Services stop
when the script exits. Keep them running for UI inspection with:

```bash
KEEP_DATASET_DEAL_DEMO=1 make datasetDealDemo
```

To drive an already-running example API without managing its services:

```bash
DATASET_DEAL_API_URL=http://127.0.0.1:28804 make triggerDatasetDealDemo
```

The trigger-only script creates or updates the comprehensive process, completes
the full-purchase and refund paths, and leaves buyer 3 pending. Set
`DATASET_DEAL_PROCESS_ID` to use a different process ID.

The seller dashboard lists processes beside executions. Buyer dashboards list
only their executions. Process pages provide an editable state graph;
execution pages render the immutable graph, highlight `currentState`, show
runtime data, and provide the shared condition-message form.

## Tests

`make e2eTests` runs the same comprehensive flow against real Dex, Temporal,
WorkerService, FlowService, PostgreSQL, REST handlers, and execution indexes.

Temporal `SearchFlows` can inspect trigger Run history grouped by FlowID. REST
execution lists always come from PostgreSQL.

## Documentation

The comprehensive JSON template is
`cmd/server/dex/ui/dataset-deal/comprehensive-process.json`.

## UI/UX

The embedded UI provides seller DSL editing and buyer 1/2/3 execution controls.
