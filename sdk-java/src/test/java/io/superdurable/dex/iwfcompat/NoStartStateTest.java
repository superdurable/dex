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

public final class NoStartStateTest {
    void compileNoStartStep(final Client client) {
        client.startFlow(IwfFlows.NO_START, "no-start", null);
        final NoStartFlow stub = client.newRpcStub(
                NoStartFlow.class,
                "no-start");
        final Long output = client.invokeRPC(stub::invoke, "input");
        consume(output);
    }

    void compileNoStep(final Client client) {
        client.startFlow(IwfFlows.NO_STATE, "no-step", null);
        final NoStateFlow stub = client.newRpcStub(
                NoStateFlow.class,
                "no-step");
        final Integer output = client.invokeRPC(stub::increaseCounter);
        client.stopFlow("no-step");
        consume(output);
    }

    void compileDeadEnd(final Client client) {
        client.startFlow(IwfFlows.DEAD_END, "dead-end", null);
        final DeadEndFlow stub = client.newRpcStub(
                DeadEndFlow.class,
                "dead-end");
        final Integer size = client.invokeRPC(stub::publishInternal);
        consume(size);
    }

    private static void consume(final Object value) {
    }
}
