/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
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
