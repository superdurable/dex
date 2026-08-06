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

public final class RpcTest {
    void compileLocking(final Client client) {
        client.startFlow(IwfFlows.NO_STATE, "rpc-lock", null);
        final NoStateFlow stub = client.newRpcStub(
                NoStateFlow.class,
                "rpc-lock");
        final Integer first = client.invokeRPC(stub::increaseCounter);
        final Integer second = client.invokeRPC(stub::getCounter);
        consume(first, second);
    }

    void compileFunctionsAndProcedures(final Client client) {
        client.startFlow(IwfFlows.RPC, "rpc", 0);
        final RpcFlow stub = client.newRpcStub(
                RpcFlow.class,
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
        final NoStateFlow errorStub = client.newRpcStub(
                NoStateFlow.class,
                "rpc-error");
        final Long ignored = client.invokeRPC(errorStub::fail, "error");
        final DeadEndFlow channelStub = client.newRpcStub(
                DeadEndFlow.class,
                "channel-size");
        final Integer published = client.invokeRPC(channelStub::publishInternal);
        final Integer size = client.invokeRPC(channelStub::signalSize);
        consume(ignored, published, size);
    }

    private static void consume(final Object... values) {
    }
}
