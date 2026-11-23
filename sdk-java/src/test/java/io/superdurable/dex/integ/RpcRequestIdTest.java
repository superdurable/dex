/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Client;
import io.superdurable.dex.RPCInvokeOptions;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

@Tag("dex-dev")
final class RpcRequestIdTest {
    private final RpcRequestIdWorkflow workflow = new RpcRequestIdWorkflow();
    private final String flowId = "rpc-request-id-" + UUID.randomUUID();
    private final RPCInvokeOptions stableRequestOptions = RPCInvokeOptions.newBuilder()
            .requestId("event-" + UUID.randomUUID())
            .build();
    private DexDevTestEnvironment environment;
    private Client client;
    private RpcRequestIdWorkflow rpc;

    @BeforeEach
    void startCounterFlow(@TempDir final Path cacheDirectory) throws Exception {
        environment = DexDevTestEnvironment.start(cacheDirectory, workflow);
        client = environment.client();
        final String runId = client.startFlow(workflow, flowId, null);
        rpc = client.newRpcStub(RpcRequestIdWorkflow.class, flowId, runId);
    }

    @AfterEach
    void closeEnvironment() throws Exception {
        if (environment != null) {
            environment.close();
        }
    }

    @Test
    void transactionalRepeatReturnsRecordedReplyWithoutRunningHandlerAgain() {
        assertEquals(3, client.invokeRPC(rpc::incrementTransactionally, 3, stableRequestOptions));
        assertEquals(3, client.invokeRPC(rpc::incrementTransactionally, 3, stableRequestOptions));
        assertEquals(3, client.invokeRPC(rpc::getCounterTotal));
        assertEquals(1, workflow.getIncrementHandlerCalls());
        client.stopFlow(flowId);
    }

    @Test
    void recordedRequestIdWithDifferentInputReturnsFirstReply() {
        assertEquals(3, client.invokeRPC(rpc::incrementTransactionally, 3, stableRequestOptions));
        assertEquals(3, client.invokeRPC(rpc::incrementTransactionally, 10, stableRequestOptions));
        assertEquals(3, client.invokeRPC(rpc::getCounterTotal));
        assertEquals(1, workflow.getIncrementHandlerCalls());
        client.stopFlow(flowId);
    }

    @Test
    void omittedRequestIdAppliesEveryCall() {
        final RPCInvokeOptions defaultRequestOptions = RPCInvokeOptions.newBuilder().build();
        assertEquals(3, client.invokeRPC(rpc::incrementTransactionally, 3, defaultRequestOptions));
        assertEquals(6, client.invokeRPC(rpc::incrementTransactionally, 3, defaultRequestOptions));
        assertEquals(9, client.invokeRPC(rpc::incrementTransactionally, 3));
        assertEquals(9, client.invokeRPC(rpc::getCounterTotal));
        assertEquals(3, workflow.getIncrementHandlerCalls());
        assertThrows(IllegalArgumentException.class, () -> RPCInvokeOptions.newBuilder().requestId(""));
        client.stopFlow(flowId);
    }

    @Test
    void nonTransactionalRepeatRunsHandlerAgainAndAppliesChangesOnce() {
        assertEquals(4, client.invokeRPC(rpc::incrementNonTransactionally, 4, stableRequestOptions));
        assertEquals(4, client.invokeRPC(rpc::incrementNonTransactionally, 4, stableRequestOptions));
        assertEquals(4, client.invokeRPC(rpc::getCounterTotal));
        assertEquals(2, workflow.getIncrementHandlerCalls());
        client.stopFlow(flowId);
    }

    @Test
    void nonTransactionalRepeatWithNoEffectsReturnsFreshOutput() {
        assertEquals("accepted", client.invokeRPC(rpc::markProcessedNonTransactionally, stableRequestOptions));
        assertEquals("alreadyProcessed", client.invokeRPC(rpc::markProcessedNonTransactionally, stableRequestOptions));
        client.stopFlow(flowId);
    }

    @Test
    void transactionalRepeatReturnsRecordedReplyEvenAfterEventIsMarkedProcessed() {
        assertEquals("accepted", client.invokeRPC(rpc::markProcessedTransactionally, stableRequestOptions));
        assertEquals("alreadyProcessed", client.invokeRPC(rpc::markProcessedTransactionally));
        assertEquals("accepted", client.invokeRPC(rpc::markProcessedTransactionally, stableRequestOptions));
        client.stopFlow(flowId);
    }

    @Test
    void repeatAfterFlowClosesReturnsRecordedReply() {
        assertEquals(7, client.invokeRPC(rpc::incrementTransactionally, 7, stableRequestOptions));
        client.stopFlow(flowId);
        client.waitForFlow(flowId, Duration.ofSeconds(30));
        assertEquals(7, client.invokeRPC(rpc::incrementTransactionally, 7, stableRequestOptions));
        assertThrows(FlowNotActiveOrNotFoundException.class,
                () -> client.invokeRPC(rpc::incrementTransactionally, 7));
        assertEquals(1, workflow.getIncrementHandlerCalls());
    }
}
