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
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class NoStartStateTest {
    private static final NoStartStateWorkflow NO_START_WORKFLOW = new NoStartStateWorkflow();
    private static final RpcNoStateWorkflow NO_STEP_WORKFLOW = new RpcNoStateWorkflow();
    private static final NoStartStateDeadEndWorkflow DEAD_END_WORKFLOW =
            new NoStartStateDeadEndWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testNoStartStateWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                NO_START_WORKFLOW)) {
            final String flowId = "no-start-" + UUID.randomUUID();
            environment.client().startFlow(NO_START_WORKFLOW, flowId, null);
            final NoStartStateWorkflow stub = environment.client().newRpcStub(
                    NoStartStateWorkflow.class,
                    flowId);
            assertEquals(
                    NoStartStateWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::invoke, "rpc-input"));
            assertEquals(1, environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(Integer.class));
        }
    }

    @Test
    void testNoStateWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                NO_STEP_WORKFLOW)) {
            final String flowId = "no-state-" + UUID.randomUUID();
            environment.client().startFlow(NO_STEP_WORKFLOW, flowId, null);
            final RpcNoStateWorkflow stub = environment.client().newRpcStub(
                    RpcNoStateWorkflow.class,
                    flowId);
            assertEquals(
                    RpcNoStateWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::invoke, "rpc-input"));
            environment.client().stopFlow(flowId);
        }
    }

    @Test
    void testDeadEndWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                DEAD_END_WORKFLOW)) {
            final String flowId = "dead-end-" + UUID.randomUUID();
            environment.client().startFlow(DEAD_END_WORKFLOW, flowId, null);
            final NoStartStateDeadEndWorkflow stub = environment.client().newRpcStub(
                    NoStartStateDeadEndWorkflow.class,
                    flowId);
            assertEquals(
                    NoStartStateDeadEndWorkflow.RPC_OUTPUT,
                    environment.client().invokeRPC(stub::invoke, "rpc-input"));
            assertTrue(environment.client()
                    .waitForFlow(flowId, Duration.ofSeconds(30))
                    .getCompletions()
                    .isEmpty());
        }
    }

    void compileNoStartStep(final Client client) {
        client.startFlow(NO_START_WORKFLOW, "no-start", null);
        final NoStartStateWorkflow stub = client.newRpcStub(
                NoStartStateWorkflow.class,
                "no-start");
        final Long output = client.invokeRPC(stub::invoke, "input");
        consume(output);
    }

    void compileNoStep(final Client client) {
        client.startFlow(NO_STEP_WORKFLOW, "no-step", null);
        final RpcNoStateWorkflow stub = client.newRpcStub(
                RpcNoStateWorkflow.class,
                "no-step");
        final Integer output = client.invokeRPC(stub::increaseCounter);
        client.stopFlow("no-step");
        consume(output);
    }

    void compileDeadEnd(final Client client) {
        client.startFlow(DEAD_END_WORKFLOW, "dead-end", null);
        final NoStartStateDeadEndWorkflow stub = client.newRpcStub(
                NoStartStateDeadEndWorkflow.class,
                "dead-end");
        final Integer size = client.invokeRPC(stub::publishInternal);
        consume(size);
    }

    private static void consume(final Object value) {
    }
}
