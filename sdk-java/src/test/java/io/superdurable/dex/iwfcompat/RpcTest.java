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
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;

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
            assertEquals(1, environment.client().invokeRPC(stub::increaseCounter));
            assertEquals(1, environment.client().invokeRPC(stub::getCounter));
            assertEquals(2, environment.client().invokeRPC(stub::increaseCounter));
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
        client.invokeRPC(stub::noPersistence);
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
}
