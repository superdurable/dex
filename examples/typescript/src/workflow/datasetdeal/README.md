# Dataset deal DSL

This is the TypeScript port of the Go dataset-deal workflow. Four fixed Steps
interpret a seller-defined finite-state process:

```text
initialize → pre-condition → execute one action → post-condition
                  ↑                  │                 │
                  └──────────────────┴──── next state ─┘
```

Unlike the Go application, this example intentionally has no database or UI.
The controller accepts the complete process definition when it starts a Flow.
It passes the definition as an initial durable attribute; the initialize Step
reads and validates that immutable execution snapshot.

## Controller

The controller exposes only start and trigger operations:

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/dataset-deal/start` | Start a deal and wait for initialization |
| `POST` | `/dataset-deal/:flowId/trigger/:conditionName` | Publish external condition data |

Start request:

```json
{
  "buyerId": "buyer-1",
  "process": {
    "processId": "dataset-deal-v1",
    "initialState": "buyer-negotiation",
    "initialStateData": {},
    "states": [
      {
        "name": "buyer-negotiation",
        "preActions": [],
        "postActions": [],
        "postCondition": {
          "waitFor": {"name": "buyer-proposal"},
          "decision": {"key": "", "cases": [], "elseState": "completed"}
        }
      },
      {
        "name": "completed",
        "preActions": [],
        "postActions": []
      }
    ]
  }
}
```

Trigger request:

```json
{
  "data": {
    "proposedSamplePrice": "10",
    "proposedFullPrice": "100"
  }
}
```

The start response is returned only after `DatasetDealInitialize` completes.
At that point `processDefinition`, `processID`, `buyerID`, and initial
`stateData` are available to trigger requests.

## Persistence and search

Register these Temporal keyword search attributes before starting the example:

- `ProcessID`
- `BuyerID`
- `CurrentState`
- `PendingPreConditionState`
- `PendingPreConditionName`

The durable attributes and channel map match the Go workflow's naming contract.
The built-in actions simulate transfers and deliveries by logging their input
and merging their results into `stateData`.
