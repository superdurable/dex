/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import io.superdurable.gen.FlowStartOptions;
import org.junit.jupiter.api.Test;

import java.util.Arrays;
import java.util.Collections;
import java.util.Optional;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class AttributeStoreSyncTest {
    @Test
    void preservesDefinitionAndFlowConfigPresence() {
        final Attribute<String> plain = Attribute.define("plain", String.class);
        final Attribute<String> synced = Attribute.define("synced", String.class)
                .syncToAttributeStore();
        final AttributeMap<String> syncedMap = AttributeMap.define("mapped", String.class)
                .syncToAttributeStore();

        assertFalse(plain.isSyncToAttributeStore());
        assertTrue(synced.isSyncToAttributeStore());
        assertTrue(syncedMap.isSyncToAttributeStore());

        final Client client = new Client(
                new Registry(Collections.<Flow<?>>emptyList()),
                new NoopBlobCache());
        try {
            final io.superdurable.gen.FlowConfig absent =
                    client.mapFlowConfig(FlowConfig.newBuilder().build());
            assertFalse(absent.hasAttributeStoreNames());

            final io.superdurable.gen.FlowConfig selected = client.mapFlowConfig(
                    FlowConfig.newBuilder().attributeStoreNames("reporting", "audit").build());
            assertTrue(selected.hasAttributeStoreNames());
            assertEquals(Arrays.asList("reporting", "audit"), selected.getAttributeStoreNames().getNamesList());

            final io.superdurable.gen.FlowConfig selectedSingle = client.mapFlowConfig(
                    FlowConfig.newBuilder().attributeStoreNames("reporting").build());
            assertTrue(selectedSingle.hasAttributeStoreNames());
            assertEquals(Collections.singletonList("reporting"), selectedSingle.getAttributeStoreNames().getNamesList());

            final io.superdurable.gen.FlowConfig disabled = client.mapFlowConfig(
                    FlowConfig.newBuilder().attributeStoreNames().build());
            assertTrue(disabled.hasAttributeStoreNames());
            assertEquals(Collections.emptyList(), disabled.getAttributeStoreNames().getNamesList());
            assertFalse(absent.hasHistoryWindowEvents());
            assertFalse(absent.hasDeleteExpiredHistoryObjects());
            for (boolean shouldDelete : new boolean[] {true, false}) {
                @SuppressWarnings("deprecation")
                final FlowConfig config = FlowConfig.newBuilder()
                        .deleteExpiredHistoryObjects(shouldDelete).build();
                final io.superdurable.gen.FlowConfig mapped = client.mapFlowConfig(config);
                assertFalse(mapped.hasDeleteExpiredHistoryObjects());
                final FlowStartOptions configuredStart = client.mapStartOptions(
                        StartFlowOptions.newBuilder().configOverride(config).build());
                assertFalse(configuredStart.getFlowConfigOverride().hasDeleteExpiredHistoryObjects());
            }

            final io.superdurable.gen.FlowConfig windowed = client.mapFlowConfig(
                    FlowConfig.newBuilder().historyWindowEvents(2500).build());
            assertTrue(windowed.hasHistoryWindowEvents());
            assertEquals(2500, windowed.getHistoryWindowEvents());
            final FlowStartOptions windowedStart = client.mapStartOptions(
                    StartFlowOptions.newBuilder()
                            .configOverride(FlowConfig.newBuilder().historyWindowEvents(2500).build())
                            .build());
            assertEquals(2500, windowedStart.getFlowConfigOverride().getHistoryWindowEvents());

            assertFalse(absent.hasOffloadHistory());
            final FlowConfig localHistoryConfig = FlowConfig.newBuilder().offloadHistory(false).build();
            final io.superdurable.gen.FlowConfig localHistory = client.mapFlowConfig(localHistoryConfig);
            assertTrue(localHistory.hasOffloadHistory());
            assertFalse(localHistory.getOffloadHistory());
            final FlowStartOptions localHistoryStart = client.mapStartOptions(
                    StartFlowOptions.newBuilder().configOverride(localHistoryConfig).build());
            assertTrue(localHistoryStart.getFlowConfigOverride().hasOffloadHistory());
            assertFalse(localHistoryStart.getFlowConfigOverride().getOffloadHistory());

            final FlowStartOptions start = client.mapStartOptions(
                    StartFlowOptions.newBuilder()
                            .addAttribute(plain, "plain")
                            .addAttribute(synced, "synced")
                            .addAttribute(syncedMap, "tenant-1", "mapped")
                            .build());
            assertFalse(start.getAttributes(0).hasSyncConfig());
            assertTrue(start.getAttributes(1).getSyncConfig().getEnabled());
            assertTrue(start.getAttributes(2).getSyncConfig().getEnabled());
        } finally {
            client.close();
        }
    }

    private static final class NoopBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobId) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            return false;
        }

        @Override
        public void delete(final String blobId) {
        }

        @Override
        public void deleteAll() {
        }

        @Override
        public void close() {
        }
    }
}
