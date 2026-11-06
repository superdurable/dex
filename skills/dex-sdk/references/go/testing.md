# Testing Go applications

Read [core testing](../core/testing.md) for the required scenarios and the
`dexcli dev` setup. This page gives the Go harness.

## Rules

- Test durable behavior against a real Dex Server, such as `dexcli dev`. Mocks
  cannot prove registration, serialization, waits, retries, Worker
  replacement, or terminal behavior. Unit-test only pure helpers.
- Build the test Worker and Client from the same Registry constructor the
  application uses.
- Give every test a unique Flow ID and unique AttributeMap instances.
- Bound every call with `context.WithTimeout`.
- Wait with Client long polls (`WaitForFlow`, `WaitForAttributeMatch`,
  `WaitForStepCompletion`, `ReadStream`) or `require.Eventually`. Never sleep
  for convergence.
- On failure, report the Flow ID, run ID, status, and error message.
- Assert business invariants, not the scheduling order of parallel Steps.

## Harness

```go
var testApplication *Application

func TestMain(m *testing.M) {
	cacheDir, err := os.MkdirTemp("", "order-flow-test-")
	if err != nil {
		panic(err)
	}
	testApplication, err = StartApplication("127.0.0.1:8801", cacheDir, fakeShipping{})
	if err != nil {
		panic(err)
	}
	code := m.Run()
	stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	stopErr := testApplication.Stop(stopContext)
	cancel()
	if stopErr != nil {
		panic(stopErr)
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		panic(err)
	}
	os.Exit(code)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func newFlowID(t *testing.T, prefix string) string {
	t.Helper()
	return prefix + "-" + t.Name() + "-" + time.Now().UTC().Format("150405.000000000")
}

func TestOrderApprovalShipsApprovedOrder(t *testing.T) {
	ctx := testContext(t)
	flow := &OrderApprovalFlow{}
	flowID := newFlowID(t, "order")
	order := Order{OrderID: flowID, Amount: 100}

	_, err := testApplication.client.StartFlow(ctx, flow, flowID, order, dex.StartFlowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = testApplication.client.InvokeRPC(ctx, flowID, flow.SubmitApproval,
		ApprovalDecision{IsApproved: true, Approver: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := testApplication.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != dex.FlowCompleted {
		t.Fatalf("flow %s ended with %s: %s", flowID, result.Status, result.ErrorMessage)
	}
}
```

`StartApplication` is the bootstrap from [go.md](go.md#bootstrap). A Client
call may pass any value of the registered Flow type; the Registry resolves
Flows by type.

## Crash and replacement tests

- `Worker.Stop` drains running handlers, so it cannot simulate a crash during
  `Execute`. Run the Worker as a subprocess and send `SIGKILL`.
- Replace the Worker with one built from identical definitions, then prove the
  Flow resumes from its last durable boundary.
- To prove a type rename is safe, start a Flow on the old code, stop at a
  durable boundary, and finish it on the renamed code.

## Commands

The runnable suite is [examples/go/integ](https://github.com/superdurable/dex/tree/main/examples/go/integ).

```bash
dexcli dev
cd examples/go
make e2eTests
```
