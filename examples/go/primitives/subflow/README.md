# SubFlow primitive

Parent Flow waits for a child SubFlow to complete.

HTTP: `GET /primitives/subflow/start?workflowId=...&inputNum=1`

`SubFlowSelectionFlow` also demonstrates a child-only wait, a child-or-Channel
wait with a condition helper, and two child waits in `AllOf`. It is registered
by the example Worker and accepts an integer start input. Each child returns
the input plus one; the parent completes with its original input.

Generate both parent and child definitions to browse their linked graphs:

```bash
dexcli visualize examples/go/primitives/subflow/selection_flow.go --json --out build/subflow-selection
dexcli visualize examples/go/primitives/subflow/child_flow.go --json --out build/subflow-child
dexcli dev --flow-rendering-dir build
```

SubFlow conditions show their child Flow type and zero-based argument index.
The helper keeps the same graph shape as a direct condition call. Parent run
switches preserve pending child waits.
