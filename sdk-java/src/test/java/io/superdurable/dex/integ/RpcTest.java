/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Client;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class RpcTest {
    private static final RpcNoStateWorkflow NO_STATE_WORKFLOW = new RpcNoStateWorkflow();
    private static final RpcWorkflow WORKFLOW = new RpcWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testLockingRpc() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                NO_STATE_WORKFLOW)) {
            final String flowId = "rpc-lock-" + UUID.randomUUID();
            environment.client().startFlow(NO_STATE_WORKFLOW, flowId, null);
            final RpcNoStateWorkflow stub = environment.client().newRpcStub(
                    RpcNoStateWorkflow.class,
                    flowId);
            final ExecutorService executor = Executors.newFixedThreadPool(10);
            try {
                final List<Future<Boolean>> futures = new ArrayList<Future<Boolean>>();
                for (int index = 0; index < 100; index++) {
                    futures.add(executor.submit(() -> {
                        try {
                            environment.client().invokeRPC(stub::increaseCounter);
                            return true;
                        } catch (RpcLockConflictException conflict) {
                            return false;
                        }
                    }));
                }
                int succeeded = 0;
                for (Future<Boolean> future : futures) {
                    if (future.get()) {
                        succeeded++;
                    }
                }
                assertTrue(succeeded > 0);
                assertEquals(succeeded, environment.client().invokeRPC(stub::getCounter));
            } finally {
                executor.shutdownNow();
            }
            environment.client().stopFlow(flowId);
        }
    }

    @Test
    void testRpcProcedureWithoutAttributeAccess() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("rpc-no-attributes");
            environment.client().startFlow(WORKFLOW, flowId, 999);
            final RpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::publishWithoutAttributeAccess);
            assertEquals(2, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
            assertThrows(
                    FlowNotActiveException.class,
                    () -> environment.client().invokeRPC(stub::publishWithoutAttributeAccess));
        }
    }

    @Test
    void testRpcWorkflowFunc1() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("rpc-func-1");
            environment.client().startFlow(WORKFLOW, flowId, 999);
            final RpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::setData, "test-value");
            assertEquals("test-value", environment.client().invokeRPC(stub::getData));
            environment.client().invokeRPC(stub::setData, null);
            assertNull(environment.client().invokeRPC(stub::getData));
            environment.client().invokeRPC(stub::setKeyword, "test-value");
            assertEquals("test-value", environment.client().invokeRPC(stub::getKeyword));
            environment.client().invokeRPC(stub::setKeyword, null);
            assertNull(environment.client().invokeRPC(stub::getKeyword));
            assertEquals(
                    RpcWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::functionOne, "rpc-input"));
            assertRpcCompletion(environment, flowId, "rpc-input");
        }
    }

    @Test
    void testRpcWorkflowFunc0() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("rpc-func-0");
            environment.client().startFlow(WORKFLOW, flowId, 999);
            final RpcWorkflow stub = stub(environment, flowId);
            assertEquals(
                    RpcWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::functionZero));
            assertRpcCompletion(environment, flowId, RpcWorkflow.HARDCODED_VALUE);
        }
    }

    @Test
    void testRpcWorkflowProc1() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("rpc-proc-1");
            environment.client().startFlow(WORKFLOW, flowId, 999);
            final RpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::procedureOne, "rpc-input");
            assertRpcCompletion(environment, flowId, "rpc-input");
        }
    }

    @Test
    void testRpcWorkflowProc0() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("rpc-proc-0");
            environment.client().startFlow(WORKFLOW, flowId, 999);
            final RpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::procedureZero);
            assertRpcCompletion(environment, flowId, RpcWorkflow.HARDCODED_VALUE);
        }
    }

    @Test
    void testRpcWorkflowFunc1ReadOnly() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("rpc-read-only");
            environment.client().startFlow(WORKFLOW, flowId, 999);
            final RpcWorkflow stub = stub(environment, flowId);
            assertEquals(
                    RpcWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::readOnly, "rpc-input"));
            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.FAIL, RpcWorkflow.HARDCODED_VALUE));
        }
    }

    @Test
    void testRpcError() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                NO_STATE_WORKFLOW)) {
            final String flowId = flowId("rpc-error");
            environment.client().startFlow(NO_STATE_WORKFLOW, flowId, null);
            final RpcNoStateWorkflow stub = environment.client().newRpcStub(
                    RpcNoStateWorkflow.class,
                    flowId);
            final WorkerInvocationException failure = assertThrows(
                    WorkerInvocationException.class,
                    () -> environment.client().invokeRPC(stub::fail, "this is an error"));
            assertTrue(failure.getWorkerErrorType().contains("IllegalArgumentException"));
            assertTrue(failure.getWorkerErrorDetail().contains("this is an error"));
            assertTrue(failure.getWorkerStackTrace().contains("IllegalArgumentException"));
            assertTrue(failure.getWorkerStackTrace().contains("this is an error"));
            environment.client().stopFlow(flowId);
        }
    }

    @Test
    void testSignalChannelSizeInfo() throws Exception {
        final NoStartStateDeadEndWorkflow workflow = new NoStartStateDeadEndWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = flowId("channel-size");
            environment.client().startFlow(workflow, flowId, null);
            final NoStartStateDeadEndWorkflow stub = environment.client().newRpcStub(
                    NoStartStateDeadEndWorkflow.class,
                    flowId);
            environment.client().invokeRPC(stub::publishInternal);
            assertEquals(2, environment.client().invokeRPC(stub::publishInternal));
            environment.client().publish(
                    flowId,
                    workflow.idleSignal,
                    (Void) null,
                    (Void) null,
                    (Void) null);
            assertEquals(3, environment.client().invokeRPC(stub::signalSize));
            environment.client().stopFlow(flowId);
        }
    }

    void compileLocking(final Client client) {
        client.startFlow(NO_STATE_WORKFLOW, "rpc-lock", null);
        final RpcNoStateWorkflow stub = client.newRpcStub(
                RpcNoStateWorkflow.class,
                "rpc-lock");
        final Integer first = client.invokeRPC(stub::increaseCounter);
        final Integer second = client.invokeRPC(stub::getCounter);
        consume(first, second);
    }

    void compileFunctionsAndProcedures(final Client client) {
        client.startFlow(WORKFLOW, "rpc", 0);
        final RpcWorkflow stub = client.newRpcStub(
                RpcWorkflow.class,
                "rpc");
        client.invokeRPC(stub::publishWithoutAttributeAccess);
        final Long one = client.invokeRPC(stub::functionOne, "input");
        final Long zero = client.invokeRPC(stub::functionZero);
        client.invokeRPC(stub::procedureOne, "input");
        client.invokeRPC(stub::procedureZero);
        final Long readOnly = client.invokeRPC(stub::readOnly, "input");
        client.invokeRPC(stub::setData, "value");
        final String data = client.invokeRPC(stub::getData);
        client.invokeRPC(stub::setKeyword, "value");
        final String keyword = client.invokeRPC(stub::getKeyword);
        consume(one, zero, readOnly, data, keyword);
    }

    void compileRpcErrorAndChannelSize(final Client client) {
        final RpcNoStateWorkflow errorStub = client.newRpcStub(
                RpcNoStateWorkflow.class,
                "rpc-error");
        final Long ignored = client.invokeRPC(errorStub::fail, "error");
        final NoStartStateDeadEndWorkflow channelStub = client.newRpcStub(
                NoStartStateDeadEndWorkflow.class,
                "channel-size");
        final Integer published = client.invokeRPC(channelStub::publishInternal);
        final Integer size = client.invokeRPC(channelStub::signalSize);
        consume(ignored, published, size);
    }

    private static void consume(final Object... values) {
    }

    private static String flowId(final String prefix) {
        return prefix + "-" + UUID.randomUUID();
    }

    private static RpcWorkflow stub(
            final DexDevTestEnvironment environment,
            final String flowId) {
        return environment.client().newRpcStub(RpcWorkflow.class, flowId);
    }

    private static void assertRpcCompletion(
            final DexDevTestEnvironment environment,
            final String flowId,
            final String expectedValue) {
        assertEquals(2, environment.client().waitForFlow(
                flowId,
                Integer.class,
                Duration.ofSeconds(30)));
        assertEquals(expectedValue, environment.client().getAttribute(flowId, WORKFLOW.data));
        assertEquals(expectedValue, environment.client().getAttribute(flowId, WORKFLOW.keyword));
        assertEquals(
                Math.toIntExact(RpcWorkflow.RPC_OUTPUT),
                environment.client().getAttribute(flowId, WORKFLOW.integer));
    }
}
