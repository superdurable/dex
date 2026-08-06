/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Client;

public final class RpcWithMemoTest {
    void compileMemoReplacement(final Client client) {
        client.startFlow(IwfFlows.RPC_MEMO_REPLACEMENT, "rpc-cache", 0);
        final RpcMemoReplacementFlow stub = client.newRpcStub(
                RpcMemoReplacementFlow.class,
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
