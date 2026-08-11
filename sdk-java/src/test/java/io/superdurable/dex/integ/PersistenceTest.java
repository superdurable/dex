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
import io.superdurable.dex.Attribute;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;

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
            assertEquals("initial", environment.client().getAttribute(flowId, WORKFLOW.initial));
            assertNull(environment.client().getAttribute(flowId, WORKFLOW.dataMap, "one"));
            assertEquals("input", environment.client().getAttribute(flowId, WORKFLOW.keyword));
            assertEquals(1, environment.client().getAttribute(flowId, WORKFLOW.integer));
            assertEquals(
                    Instant.parse("2023-04-17T21:17:49Z"),
                    environment.client().getAttribute(flowId, WORKFLOW.datetime));
            assertEquals(
                    0,
                    environment.client().getAttribute(flowId, WORKFLOW.model).value);
            assertThrows(
                    FlowNotActiveException.class,
                    () -> environment.client().setAttribute(flowId, WORKFLOW.data, "closed"));
            assertThrows(
                    FlowNotFoundException.class,
                    () -> environment.client().getAttribute(
                            "missing-" + UUID.randomUUID(),
                            WORKFLOW.data));
        }
    }

    @Test
    void testSetIndexedAttributes() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                SET_ATTRIBUTES_WORKFLOW)) {
            final String flowId = "set-indexed-attributes-" + UUID.randomUUID();
            final String[] keywords = {"keyword-1", "keyword-2"};
            final Instant datetime = Instant.parse("2024-11-13T00:00:01.731455544Z");
            environment.client().startFlow(SET_ATTRIBUTES_WORKFLOW, flowId, "start");
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.keyword,
                    "keyword-1");
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.text,
                    "text-1");
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.decimal,
                    1.0);
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.integer,
                    1);
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.bool,
                    true);
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.keywords,
                    keywords);
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.datetime,
                    datetime);
            environment.client().publish(flowId, SET_ATTRIBUTES_WORKFLOW.proceed, (Void) null);

            assertEquals("test-result", environment.client().waitForFlow(
                    flowId,
                    String.class,
                    Duration.ofSeconds(30)));
            assertEquals(
                    "keyword-1",
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.keyword));
            assertEquals(
                    "text-1",
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.text));
            assertEquals(
                    1.0,
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.decimal));
            assertEquals(
                    1,
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.integer));
            assertEquals(
                    true,
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.bool));
            assertArrayEquals(
                    keywords,
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.keywords));
            assertEquals(
                    datetime,
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.datetime));
        }
    }

    @Test
    void testSetDataAttributes() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                SET_ATTRIBUTES_WORKFLOW)) {
            final String flowId = "set-data-attributes-" + UUID.randomUUID();
            final PersistenceWorkflow.ModelInput model = new PersistenceWorkflow.ModelInput();
            model.value = 7;
            environment.client().startFlow(SET_ATTRIBUTES_WORKFLOW, flowId, "start");
            assertThrows(
                    LongPollTimeoutException.class,
                    () -> environment.client().waitForAttributeEqual(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.data,
                            "never",
                            Duration.ofSeconds(1)));
            final CompletableFuture<Void> waiting = CompletableFuture.runAsync(
                    () -> environment.client().waitForAttributeEqual(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.data,
                            "query-start",
                            Duration.ofSeconds(30)));
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.data,
                    "query-start");
            waiting.get(30, TimeUnit.SECONDS);
            final CompletableFuture<Void> waitingMap = CompletableFuture.runAsync(
                    () -> environment.client().waitForAttributeMapEqual(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.dataMap,
                            "special / key",
                            "mapped-value",
                            Duration.ofSeconds(30)));
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.dataMap,
                    "one",
                    "mapped-value");
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.dataMap,
                    "special / key",
                    "mapped-value");
            waitingMap.get(30, TimeUnit.SECONDS);
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().waitForAttributeEqual(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.model,
                            model,
                            Duration.ofSeconds(30)));
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().waitForAttributeEqual(
                            flowId,
                            Attribute.define("bytes", byte[].class),
                            new byte[] {1},
                            Duration.ofSeconds(30)));
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().waitForAttributeEqual(
                            flowId,
                            Attribute.define("null", Void.class),
                            null,
                            Duration.ofSeconds(30)));
            environment.client().setAttribute(
                    flowId,
                    SET_ATTRIBUTES_WORKFLOW.model,
                    model);
            environment.client().publish(flowId, SET_ATTRIBUTES_WORKFLOW.proceed, (Void) null);

            assertEquals("test-result", environment.client().waitForFlow(
                    flowId,
                    String.class,
                    Duration.ofSeconds(30)));
            assertEquals(
                    "query-start",
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.data));
            assertEquals(
                    "mapped-value",
                    environment.client().getAttribute(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.dataMap,
                            "one"));
            assertEquals(
                    7,
                    environment.client().getAttribute(flowId, SET_ATTRIBUTES_WORKFLOW.model).value);
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
        client.waitForAttributeEqual(
                "set-attributes",
                SET_ATTRIBUTES_WORKFLOW.data,
                "value",
                Duration.ofSeconds(30));
        client.waitForAttributeMapEqual(
                "set-attributes",
                SET_ATTRIBUTES_WORKFLOW.dataMap,
                "one",
                "value",
                Duration.ofSeconds(30));
        final String output = client.waitForFlow("set-attributes", String.class);
        consume(output);
    }

    private static void consume(final Object... values) {
    }
}
