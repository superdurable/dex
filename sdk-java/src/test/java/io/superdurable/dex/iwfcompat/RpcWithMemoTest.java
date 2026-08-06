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

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Client;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

@Tag("dex-dev")
public final class RpcWithMemoTest {
    private static final RpcWorkflow WORKFLOW = new RpcWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testRpcMemoWorkflowFunc1() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            final String flowId = startFlow(environment, "rpc-attribute-func-1");
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
            assertCompletion(environment, flowId, "rpc-input");
        }
    }

    @Test
    void testRpcMemoWorkflowFunc0() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            final String flowId = startFlow(environment, "rpc-attribute-func-0");
            final RpcWorkflow stub = stub(environment, flowId);
            assertEquals(
                    RpcWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::functionZero));
            assertCompletion(environment, flowId, RpcWorkflow.HARDCODED_VALUE);
        }
    }

    @Test
    void testRpcMemoWorkflowProc1() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            final String flowId = startFlow(environment, "rpc-attribute-proc-1");
            final RpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::procedureOne, "rpc-input");
            assertCompletion(environment, flowId, "rpc-input");
        }
    }

    @Test
    void testRpcMemoWorkflowProc0() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            final String flowId = startFlow(environment, "rpc-attribute-proc-0");
            final RpcWorkflow stub = stub(environment, flowId);
            environment.client().invokeRPC(stub::procedureZero);
            assertCompletion(environment, flowId, RpcWorkflow.HARDCODED_VALUE);
        }
    }

    @Test
    void testRpcMemoWorkflowFunc1ReadOnly() throws Exception {
        try (DexDevTestEnvironment environment = start()) {
            final String flowId = startFlow(environment, "rpc-attribute-read-only");
            final RpcWorkflow stub = stub(environment, flowId);
            assertEquals(
                    RpcWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::readOnly, "rpc-input"));
            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.FAIL, RpcWorkflow.HARDCODED_VALUE));
        }
    }

    void compileMemoReplacement(final Client client) {
        client.startFlow(WORKFLOW, "rpc-cache", 0);
        final RpcWorkflow stub = client.newRpcStub(
                RpcWorkflow.class,
                "rpc-cache");
        client.invokeRPC(stub::setData, "value");
        final String data = client.invokeRPC(stub::getData);
        client.invokeRPC(stub::setKeyword, "keyword");
        final String keyword = client.invokeRPC(stub::getKeyword);
        final Long result = client.invokeRPC(stub::functionOne, "input");
        consume(data, keyword, result);
    }

    private static void consume(final Object... values) {
    }

    private DexDevTestEnvironment start() throws Exception {
        return DexDevTestEnvironment.start(cacheDirectory, WORKFLOW);
    }

    private static String startFlow(
            final DexDevTestEnvironment environment,
            final String prefix) {
        final String flowId = prefix + "-" + UUID.randomUUID();
        environment.client().startFlow(WORKFLOW, flowId, 999);
        return flowId;
    }

    private static RpcWorkflow stub(
            final DexDevTestEnvironment environment,
            final String flowId) {
        return environment.client().newRpcStub(RpcWorkflow.class, flowId);
    }

    private static void assertCompletion(
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
