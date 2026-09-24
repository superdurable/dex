/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Client;
import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeMatch;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.WaitForAttributeOptions;
import io.superdurable.dex.exceptions.RequestTimeoutException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
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
            assertEquals("input", environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(String.class));
        }
    }

    @Test
    void testSetIndexedAttributes() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                SET_ATTRIBUTES_WORKFLOW)) {
            final String flowId = "set-indexed-attributes-" + UUID.randomUUID();
            environment.client().startFlow(SET_ATTRIBUTES_WORKFLOW, flowId, "start");
            final PersistenceSetAttributesWorkflow stub = environment.client().newRpcStub(
                    PersistenceSetAttributesWorkflow.class,
                    flowId);
            environment.client().invokeRPC(stub::setIndexed);
            environment.client().invokeRPC(stub::complete);

            assertEquals("test-result", environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(String.class));
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
            final PersistenceSetAttributesWorkflow stub = environment.client().newRpcStub(
                    PersistenceSetAttributesWorkflow.class,
                    flowId);
            assertThrows(
                    RequestTimeoutException.class,
                    () -> environment.client().waitForAttributeMatch(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.data,
                            AttributeMatch.equalTo("never"),
                            waitOptions(flowId, "never", Duration.ofSeconds(1))));
            final CompletableFuture<String> waiting = CompletableFuture.supplyAsync(
                    () -> environment.client().waitForAttributeMatch(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.data,
                            AttributeMatch.equalTo("query-start"),
                            waitOptions(flowId, "data", Duration.ofSeconds(30))));
            environment.client().invokeRPC(stub::setData, "query-start");
            assertEquals("query-start", waiting.get(30, TimeUnit.SECONDS));
            final CompletableFuture<String> waitingMap = CompletableFuture.supplyAsync(
                    () -> environment.client().waitForAttributeMatch(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.dataMap,
                            "special % key",
                            AttributeMatch.equalTo("mapped-value"),
                            waitOptions(flowId, "map", Duration.ofSeconds(30))));
            environment.client().invokeRPC(stub::setMapOne, "mapped-value");
            environment.client().invokeRPC(stub::setMapSpecial, "mapped-value");
            assertEquals("mapped-value", waitingMap.get(30, TimeUnit.SECONDS));
            environment.client().invokeRPC(stub::setInteger, 3);
            assertEquals(
                    3,
                    environment.client().waitForAttributeMatch(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.integer,
                            AttributeMatch.greaterThan(0),
                            waitOptions(flowId, "integer", Duration.ofSeconds(30))));
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().waitForAttributeMatch(
                            flowId,
                            SET_ATTRIBUTES_WORKFLOW.model,
                            AttributeMatch.equalTo(model),
                            waitOptions(flowId, "model", Duration.ofSeconds(30))));
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().waitForAttributeMatch(
                            flowId,
                            Attribute.define("bytes", byte[].class),
                            AttributeMatch.equalTo(new byte[] {1}),
                            waitOptions(flowId, "bytes", Duration.ofSeconds(30))));
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().waitForAttributeMatch(
                            flowId,
                            Attribute.define("null", Void.class),
                            AttributeMatch.equalTo((Void) null),
                            waitOptions(flowId, "null", Duration.ofSeconds(30))));
            environment.client().invokeRPC(stub::setModel, model);
            environment.client().invokeRPC(stub::complete);

            assertEquals("test-result", environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(String.class));
        }
    }

    void compilePersistenceReads(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .addAttribute(WORKFLOW.initial, "initial")
                .addAttribute(WORKFLOW.dataMap, "one", "initial")
                .build();
        client.startFlow(WORKFLOW, "persistence", "input", options);
        final String output = client.waitForFlow("persistence").getSingleOutput(String.class);
        consume(output);
    }

    void compilePersistenceWrites(final Client client) {
        client.startFlow(SET_ATTRIBUTES_WORKFLOW, "set-attributes", "input");
        final PersistenceSetAttributesWorkflow stub = client.newRpcStub(
                PersistenceSetAttributesWorkflow.class,
                "set-attributes");
        client.invokeRPC(stub::setData, "value");
        client.invokeRPC(stub::setMapOne, "value");
        client.invokeRPC(stub::setIndexed);
        final String matched = client.waitForAttributeMatch(
                "set-attributes",
                SET_ATTRIBUTES_WORKFLOW.data,
                AttributeMatch.equalTo("value"),
                waitOptions("set-attributes", "data", Duration.ofSeconds(30)));
        client.invokeRPC(stub::complete);
        final String matchedMap = client.waitForAttributeMatch(
                "set-attributes",
                SET_ATTRIBUTES_WORKFLOW.dataMap,
                "one",
                AttributeMatch.equalTo("value"),
                waitOptions("set-attributes", "map", Duration.ofSeconds(30)));
        final String output = client.waitForFlow("set-attributes").getSingleOutput(String.class);
        consume(matched, matchedMap, output);
    }

    private static void consume(final Object... values) {
    }

    private static WaitForAttributeOptions waitOptions(
            final String flowId,
            final String suffix,
            final Duration requestTimeout) {
        return WaitForAttributeOptions.newBuilder()
                .requestId(flowId + "-wait-" + suffix)
                .requestTimeout(requestTimeout)
                .build();
    }
}
