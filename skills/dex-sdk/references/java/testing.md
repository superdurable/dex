# Testing Java Dex applications

Read [../core/testing.md](../core/testing.md) for what to test and why. Durable
behavior needs a real Dex Server: start one locally with `dexcli dev`. Unit
tests cover only pure helpers and DTO mapping; they cannot prove commit
boundaries, retries, Worker replacement, Channel delivery, RPC locks, Timers,
or terminal behavior.

## Harness

Reuse the application's own bootstrap (Registry, BlobCache, Worker, Client).
Do not import `DexDevTestEnvironment`; it lives only in the Dex repository's
SDK tests and is not part of the published SDK.

```java
class OrderFlowIntegTest {
    private static OrderFlow orderFlow;
    private static DexRuntime dex;

    @BeforeAll
    static void startDex() throws Exception {
        orderFlow = new OrderFlow(new FakePaymentService());
        String serverAddress = System.getenv().getOrDefault("DEX_SERVER_ADDRESS", "127.0.0.1:8801");
        dex = DexRuntime.start(
                serverAddress,
                Files.createTempDirectory("dex-cache").toString(),
                List.<Flow<?>>of(orderFlow, new ShipmentFlow()));
    }

    @AfterAll
    static void stopDex() throws InterruptedException {
        dex.close();
    }

    @Test
    void approvedOrderIsCharged() {
        String flowId = "order-" + UUID.randomUUID();
        dex.client().startFlow(orderFlow, flowId, new OrderRequest(flowId, 1200L));
        OrderFlow stub = dex.client().newRpcStub(OrderFlow.class, flowId);
        dex.client().invokeRPC(stub::approveOrder, "alice");

        FlowResult result = dex.client().waitForFlow(flowId, Duration.ofSeconds(45));
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(flowId, result.getSingleOutput(String.class));
    }
}
```

- `DexRuntime` is the bootstrap class from [java.md](java.md#worker-and-client-bootstrap).
- Give every test a unique Flow ID. Never reuse a fixed ID across tests or runs.
- Use a temporary `BlobCache` directory per test run.
- Register every Flow the tests start, including SubFlow children.
- Replace external services with fakes injected through the Flow constructor.
  Keep the Dex Server real.
- One Worker per JVM is enough. Use a JUnit extension or `@BeforeAll` to share
  it across test classes.

## Waiting in tests

Wait on Dex state with a deadline. Never use a fixed sleep as the assertion.

| Wait for | Use |
| --- | --- |
| Terminal result | `client.waitForFlow(flowId, timeout)`, then check `getStatus()` |
| A Step's commit | `client.waitForStepCompletion(flowId, StepExecutionId.of("X"), options)` |
| A stored value | `client.waitForAttributeMatch(...)` with a request timeout |
| A computed state | poll a read-only RPC until a deadline |

```java
private static void awaitStatus(String flowId, String expected) {
    OrderFlow stub = dex.client().newRpcStub(OrderFlow.class, flowId);
    long deadline = System.nanoTime() + Duration.ofSeconds(20).toNanos();
    String lastStatus = null;
    while (System.nanoTime() < deadline) {
        lastStatus = dex.client().invokeRPC(stub::getOrderStatus);
        if (expected.equals(lastStatus)) {
            return;
        }
        Thread.onSpinWait();
    }
    fail("order " + flowId + " stayed at " + lastStatus + ", expected " + expected);
}
```

Report the last observed value in the failure message. Keep deadlines short
but not flaky.

## Required scenarios

Cover the scenario list in [../core/testing.md](../core/testing.md). In
this language:

- Worker replacement: stop the Worker while the Flow waits or retries, start
  a new Worker with the same Registry, and assert progress continues.
- Timers: assert a generous bound, and use `client.skipTimer` to shorten long ones.
- Failure paths: make the fake dependency fail deterministically and assert
  the recovery Step or the terminal status.

## Running

- Start a local server with `dexcli dev`. A short-lived test server can use the
  faster development durability mode described in
  [../core/testing.md](../core/testing.md).
- Point the tests at it with an environment variable, and run them with the
  application's Gradle or Maven integration-test task.
- Fail, do not skip, when the server is missing in CI. Start it in the CI job.
- The Dex repository's own Java suite runs with
  [run-integration-tests.sh](https://github.com/superdurable/dex/blob/main/examples/java/run-integration-tests.sh);
  its tests under
  [examples/java/src/test](https://github.com/superdurable/dex/tree/main/examples/java/src/test/java/io/superdurable/dex/integ)
  show the shared-Worker extension and scenario style.

## Checklist

- [ ] Tests use the application's real bootstrap and a real Dex Server.
- [ ] Unique Flow IDs and a temporary cache directory.
- [ ] Waits use Dex APIs or deadline polling, never fixed sleeps.
- [ ] Failure, recovery, Worker replacement, and terminal paths are covered.
- [ ] Assertions check `FlowStatus` before decoding output.
