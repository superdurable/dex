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

public final class RpcWithMemoTest {
    void compileMemoReplacement(final Client client) {
        client.startFlow(IwfFlows.RPC, "rpc-cache", 0);
        final RpcFlow stub = client.newRpcStub(
                RpcFlow.class,
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
}
