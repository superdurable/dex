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
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;

@Tag("dex-dev")
public final class PersistenceTest {
    private static final PersistenceWorkflow WORKFLOW = new PersistenceWorkflow();
    private static final PersistenceSetAttributesWorkflow SET_ATTRIBUTES_WORKFLOW =
            new PersistenceSetAttributesWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testPersistenceReads() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "persistence-" + UUID.randomUUID();
            final StartFlowOptions options = StartFlowOptions.newBuilder()
                    .addAttribute(WORKFLOW.initial, "initial")
                    .addAttribute(WORKFLOW.dataMap, "one", "initial")
                    .build();
            environment.client().startFlow(WORKFLOW, flowId, "input", options);
            assertEquals("input", environment.client().waitForFlow(
                    flowId,
                    String.class,
                    Duration.ofSeconds(30)));
            assertEquals("input", environment.client().getAttribute(flowId, WORKFLOW.data));
            assertEquals(1, environment.client().getAttribute(flowId, WORKFLOW.integer));
            assertEquals(
                    Instant.parse("2023-04-17T21:17:49Z"),
                    environment.client().getAttribute(flowId, WORKFLOW.datetime));
        }
    }

    void compilePersistenceReads(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .addAttribute(WORKFLOW.initial, "initial")
                .addAttribute(WORKFLOW.dataMap, "one", "initial")
                .build();
        client.startFlow(WORKFLOW, "persistence", "input", options);
        final String data = client.getAttribute(
                "persistence",
                WORKFLOW.data);
        final Integer integer = client.getAttribute(
                "persistence",
                WORKFLOW.integer);
        final Instant datetime = client.getAttribute(
                "persistence",
                WORKFLOW.datetime);
        consume(data, integer, datetime);
    }

    void compilePersistenceWrites(final Client client) {
        client.startFlow(SET_ATTRIBUTES_WORKFLOW, "set-attributes", "input");
        client.setAttribute("set-attributes", SET_ATTRIBUTES_WORKFLOW.data, "value");
        client.setAttribute(
                "set-attributes",
                SET_ATTRIBUTES_WORKFLOW.dataMap,
                "one",
                "value");
        client.setAttribute("set-attributes", SET_ATTRIBUTES_WORKFLOW.keyword, "keyword");
        client.setAttribute("set-attributes", SET_ATTRIBUTES_WORKFLOW.decimal, 1.5);
        client.setAttribute("set-attributes", SET_ATTRIBUTES_WORKFLOW.integer, 1);
        client.setAttribute("set-attributes", SET_ATTRIBUTES_WORKFLOW.bool, true);
        client.setAttribute(
                "set-attributes",
                SET_ATTRIBUTES_WORKFLOW.keywords,
                new String[] {"one", "two"});
        final String output = client.waitForFlow("set-attributes", String.class);
        consume(output);
    }

    private static void consume(final Object... values) {
    }
}
