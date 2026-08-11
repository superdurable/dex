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
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.SearchFlowEntry;
import io.superdurable.dex.SearchFlowsPage;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertThrows;

@Tag("dex-dev")
public final class SearchFlowsTest {
    private static final SearchFlowsWorkflow WORKFLOW = new SearchFlowsWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testSearchFlowsFindsIndexedFlow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final Client client = environment.client();
            final String keywordValue = "sf-" + UUID.randomUUID();
            final String flowId = "search-flows-" + UUID.randomUUID();
            client.startFlow(
                    WORKFLOW,
                    flowId,
                    keywordValue,
                    StartFlowOptions.newBuilder()
                            .idReusePolicy(IdReusePolicy.DISALLOW)
                            .build());
            assertEquals(
                    keywordValue,
                    client.waitForFlow(flowId, String.class, Duration.ofSeconds(30)));

            final String query = SearchFlowsWorkflow.KEYWORD_KEY + " = '" + keywordValue + "'";
            final SearchFlowEntry entry = pollForFlow(client, query, flowId);
            assertEquals(flowId, entry.getFlowId());
            assertFalse(entry.getRunId().isEmpty());
            assertEquals(FlowStatus.COMPLETED, entry.getStatus());
            assertNotNull(entry.getStartedAt());
            assertEquals(
                    keywordValue,
                    entry.getIndexedAttributes().get(SearchFlowsWorkflow.KEYWORD_KEY));
        }
    }

    @Test
    void testSearchFlowsRejectsNegativePageSize() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().searchFlows("CustomKeywordField = 'x'", -1));
        }
    }

    private static SearchFlowEntry pollForFlow(
            final Client client,
            final String query,
            final String flowId) throws InterruptedException {
        final long deadline = System.nanoTime() + Duration.ofSeconds(30).toNanos();
        RuntimeException lastError = null;
        while (System.nanoTime() < deadline) {
            try {
                final SearchFlowsPage page = client.searchFlows(query, 100, "");
                for (final SearchFlowEntry entry : page.getFlows()) {
                    if (flowId.equals(entry.getFlowId())) {
                        return entry;
                    }
                }
            } catch (RuntimeException error) {
                lastError = error;
            }
            Thread.sleep(200L);
        }
        throw new AssertionError("flow " + flowId + " not found via SearchFlows", lastError);
    }
}
