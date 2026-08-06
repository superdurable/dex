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
import io.superdurable.dex.StartFlowOptions;

import java.time.Instant;

public final class PersistenceTest {
    void compilePersistenceReads(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .addAttribute(IwfFlows.BASIC_PERSISTENCE.initial, "initial")
                .addAttribute(IwfFlows.BASIC_PERSISTENCE.dataMap, "one", "initial")
                .build();
        client.startFlow(IwfFlows.BASIC_PERSISTENCE, "persistence", "input", options);
        final String data = client.getAttribute(
                "persistence",
                IwfFlows.BASIC_PERSISTENCE.data);
        final Integer integer = client.getAttribute(
                "persistence",
                IwfFlows.BASIC_PERSISTENCE.integer);
        final Instant datetime = client.getAttribute(
                "persistence",
                IwfFlows.BASIC_PERSISTENCE.datetime);
        consume(data, integer, datetime);
    }

    void compilePersistenceWrites(final Client client) {
        client.startFlow(IwfFlows.SET_ATTRIBUTES, "set-attributes", "input");
        client.setAttribute("set-attributes", IwfFlows.SET_ATTRIBUTES.data, "value");
        client.setAttribute(
                "set-attributes",
                IwfFlows.SET_ATTRIBUTES.dataMap,
                "one",
                "value");
        client.setAttribute("set-attributes", IwfFlows.SET_ATTRIBUTES.keyword, "keyword");
        client.setAttribute("set-attributes", IwfFlows.SET_ATTRIBUTES.decimal, 1.5);
        client.setAttribute("set-attributes", IwfFlows.SET_ATTRIBUTES.integer, 1);
        client.setAttribute("set-attributes", IwfFlows.SET_ATTRIBUTES.bool, true);
        client.setAttribute(
                "set-attributes",
                IwfFlows.SET_ATTRIBUTES.keywords,
                new String[] {"one", "two"});
        final String output = client.waitForFlow("set-attributes", String.class);
        consume(output);
    }

    private static void consume(final Object... values) {
    }
}
