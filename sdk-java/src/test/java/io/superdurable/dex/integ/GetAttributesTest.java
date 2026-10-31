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

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.AttributeMapInstance;
import io.superdurable.dex.AttributeRead;
import io.superdurable.dex.AttributeValues;
import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.Registry;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowDefinitionException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.integ.GetAttributesWorkflow.GetAttributesNote;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetAttributesRequest;
import io.superdurable.gen.KV;
import io.superdurable.gen.Value;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.stream.Collectors;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class GetAttributesTest {
    private static final GetAttributesWorkflow WORKFLOW = new GetAttributesWorkflow();
    private static final String SET_QUANTITY_INSTANCE = "line 1";
    private static final String UNSET_QUANTITY_INSTANCE = "line-2";
    private static final GetAttributesNote NOTE = new GetAttributesNote("ship today", 2);
    private static final String SECRET = repeat("hard-delete-value-", 16);
    private static final String LARGE_SECRET = repeat("large-hard-delete-value-", 64);
    private static final String LARGE_VALUE = repeat("large-value-", 64);
    private static final String CLOSED_RUN_LARGE_VALUE = repeat("closed-run-large-value-", 32);
    private static final String SERVER_ADDRESS =
            System.getProperty("dex.test.serverAddress", "127.0.0.1:8801");

    @TempDir
    Path cacheDirectory;

    @TempDir
    Path freshCacheDirectory;

    @Test
    void testGetAttributesReadsCommittedValues() throws Exception {
        final ManagedChannel channel = ManagedChannelBuilder
                .forTarget(SERVER_ADDRESS)
                .usePlaintext()
                .build();
        try (DexDevTestEnvironment environment =
                DexDevTestEnvironment.start(cacheDirectory, WORKFLOW)) {
            final FlowServiceGrpc.FlowServiceBlockingStub flowService =
                    FlowServiceGrpc.newBlockingStub(channel);
            final Client client = environment.client();
            final String flowId = "get-attributes-" + UUID.randomUUID();
            client.startFlow(WORKFLOW, flowId, null, startOptions());

            final Map<String, Value> stored = readRawAttributes(
                    flowService,
                    flowId,
                    GetAttributesWorkflow.LARGE_VALUE_NAME,
                    GetAttributesWorkflow.CLOSED_RUN_LARGE_VALUE_NAME,
                    GetAttributesWorkflow.SECRET_NAME,
                    GetAttributesWorkflow.LARGE_SECRET_NAME);
            assertEquals(
                    Value.KindCase.INTERNAL_BLOB_ID_FOR_STRING_VALUE,
                    stored.get(GetAttributesWorkflow.LARGE_VALUE_NAME).getKindCase());
            assertEquals(
                    Value.KindCase.INTERNAL_BLOB_ID_FOR_STRING_VALUE,
                    stored.get(GetAttributesWorkflow.CLOSED_RUN_LARGE_VALUE_NAME).getKindCase());
            assertEquals(
                    Value.KindCase.STRING_VALUE,
                    stored.get(GetAttributesWorkflow.SECRET_NAME).getKindCase());
            assertEquals(
                    Value.KindCase.STRING_VALUE,
                    stored.get(GetAttributesWorkflow.LARGE_SECRET_NAME).getKindCase());

            final AttributeValues values = client.getAttributes(
                    flowId,
                    WORKFLOW.label,
                    WORKFLOW.count,
                    WORKFLOW.note,
                    WORKFLOW.emptyLabel,
                    WORKFLOW.zeroCount,
                    WORKFLOW.unsetCount,
                    WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE),
                    WORKFLOW.quantities.instance(UNSET_QUANTITY_INSTANCE),
                    WORKFLOW.secret,
                    WORKFLOW.largeSecret,
                    WORKFLOW.largeValue);
            assertEquals(Optional.of("ready"), values.get(WORKFLOW.label));
            assertEquals(Optional.of(7), values.get(WORKFLOW.count));
            assertEquals(Optional.of(NOTE), values.get(WORKFLOW.note));
            assertEquals(Optional.of(""), values.get(WORKFLOW.emptyLabel));
            assertEquals(Optional.of(0), values.get(WORKFLOW.zeroCount));
            assertEquals(Optional.empty(), values.get(WORKFLOW.unsetCount));
            assertEquals(
                    Optional.of(0),
                    values.get(WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE)));
            assertEquals(
                    Optional.empty(),
                    values.get(WORKFLOW.quantities.instance(UNSET_QUANTITY_INSTANCE)));
            assertEquals(Optional.of(SECRET), values.get(WORKFLOW.secret));
            assertEquals(Optional.of(LARGE_SECRET), values.get(WORKFLOW.largeSecret));
            assertEquals(Optional.of(LARGE_VALUE), values.get(WORKFLOW.largeValue));

            final IllegalArgumentException notRead = assertThrows(
                    IllegalArgumentException.class,
                    () -> values.get(WORKFLOW.closedRunLargeValue));
            assertTrue(notRead.getMessage().contains(
                    GetAttributesWorkflow.CLOSED_RUN_LARGE_VALUE_NAME));
            final IllegalArgumentException otherInstance = assertThrows(
                    IllegalArgumentException.class,
                    () -> values.get(WORKFLOW.quantities.instance("line-3")));
            assertTrue(otherInstance.getMessage().contains(
                    GetAttributesWorkflow.QUANTITIES_NAME + "/line-3"));
            final Attribute<String> sameNameLabel =
                    Attribute.define(GetAttributesWorkflow.LABEL_NAME, String.class);
            final IllegalArgumentException sameNameError = assertThrows(
                    IllegalArgumentException.class,
                    () -> values.get(sameNameLabel));
            assertTrue(sameNameError.getMessage().contains(flowId), sameNameError.getMessage());
            assertTrue(
                    sameNameError.getMessage().contains(GetAttributesWorkflow.LABEL_NAME),
                    sameNameError.getMessage());

            final List<AttributeRead<?>> listReads =
                    new ArrayList<AttributeRead<?>>();
            listReads.add(WORKFLOW.count);
            listReads.add(WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE));
            final AttributeValues listValues = client.getAttributes(flowId, listReads);
            assertEquals(Optional.of(7), listValues.get(WORKFLOW.count));
            assertEquals(
                    Optional.of(0),
                    listValues.get(WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE)));

            assertRejectedReads(client, flowId);
            assertMissingFlow(client);
            assertHardDeleteSetThenDeleted(client);

            client.stopFlow(flowId);
            assertEquals(
                    FlowStatus.CANCELED,
                    client.waitForFlow(flowId, Duration.ofSeconds(30)).getStatus());
            assertClosedRunReadWithFreshCache(flowId);
            assertCacheExcludes(cacheDirectory, SECRET);
            assertCacheExcludes(cacheDirectory, LARGE_SECRET);
        } finally {
            channel.shutdownNow();
            channel.awaitTermination(10, TimeUnit.SECONDS);
        }
    }

    private static StartFlowOptions startOptions() {
        return StartFlowOptions.newBuilder()
                .addAttribute(WORKFLOW.label, "ready")
                .addAttribute(WORKFLOW.count, 7)
                .addAttribute(WORKFLOW.note, NOTE)
                .addAttribute(WORKFLOW.emptyLabel, "")
                .addAttribute(WORKFLOW.zeroCount, 0)
                .addAttribute(WORKFLOW.quantities, SET_QUANTITY_INSTANCE, 0)
                .addAttribute(WORKFLOW.secret, SECRET)
                .addAttribute(WORKFLOW.largeSecret, LARGE_SECRET)
                .addAttribute(WORKFLOW.largeValue, LARGE_VALUE)
                .addAttribute(WORKFLOW.closedRunLargeValue, CLOSED_RUN_LARGE_VALUE)
                .build();
    }

    private static void assertRejectedReads(final Client client, final String flowId) {
        final IllegalArgumentException duplicate = assertThrows(
                IllegalArgumentException.class,
                () -> client.getAttributes(flowId, WORKFLOW.count, WORKFLOW.count));
        assertTrue(duplicate.getMessage().contains(flowId));
        assertTrue(duplicate.getMessage().contains(
                "duplicate Attribute read " + GetAttributesWorkflow.COUNT_NAME));
        final IllegalArgumentException duplicateInstance = assertThrows(
                IllegalArgumentException.class,
                () -> client.getAttributes(
                        flowId,
                        WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE),
                        WORKFLOW.quantities.instance(SET_QUANTITY_INSTANCE)));
        assertTrue(duplicateInstance.getMessage().contains(GetAttributesWorkflow.QUANTITIES_NAME + "/line%201"));

        final IllegalArgumentException empty = assertThrows(
                IllegalArgumentException.class,
                () -> client.getAttributes(flowId));
        assertTrue(empty.getMessage().contains(flowId));
        assertTrue(empty.getMessage().contains("at least one Attribute read"));

        final Attribute<Integer> unregistered =
                Attribute.define("get-attributes-unregistered", Integer.class);
        final FlowDefinitionException unregisteredError = assertThrows(
                FlowDefinitionException.class,
                () -> client.getAttributes(flowId, unregistered));
        assertTrue(unregisteredError.getMessage().contains(flowId));
        assertTrue(unregisteredError.getMessage().contains("get-attributes-unregistered"));

        final Attribute<String> mismatchedCount =
                Attribute.define(GetAttributesWorkflow.COUNT_NAME, String.class);
        final FlowDefinitionException mismatchedError = assertThrows(
                FlowDefinitionException.class,
                () -> client.getAttributes(flowId, mismatchedCount));
        assertTrue(mismatchedError.getMessage().contains(flowId));
        assertTrue(mismatchedError.getMessage().contains(GetAttributesWorkflow.COUNT_NAME
                + " differs from the definition registered by Flow GetAttributesWorkflow"));

        final AttributeMap<Integer> mismatchedQuantities =
                AttributeMap.define(GetAttributesWorkflow.QUANTITIES_NAME, Integer.class);
        final FlowDefinitionException mismatchedQuantitiesError = assertThrows(
                FlowDefinitionException.class,
                () -> client.getAttributes(
                        flowId,
                        mismatchedQuantities.instance(SET_QUANTITY_INSTANCE)));
        assertTrue(
                mismatchedQuantitiesError.getMessage().contains(flowId),
                mismatchedQuantitiesError.getMessage());
        assertTrue(
                mismatchedQuantitiesError.getMessage().contains(
                        GetAttributesWorkflow.QUANTITIES_NAME + "/line%201"),
                mismatchedQuantitiesError.getMessage());
    }

    private static void assertMissingFlow(final Client client) {
        final String missingFlowId = "get-attributes-missing-" + UUID.randomUUID();
        final FlowNotFoundException missing = assertThrows(
                FlowNotFoundException.class,
                () -> client.getAttributes(missingFlowId, WORKFLOW.count));
        assertTrue(missing.getMessage().contains(missingFlowId), missing.getMessage());
        assertTrue(
                missing.getMessage().contains(GetAttributesWorkflow.COUNT_NAME),
                missing.getMessage());
    }

    private static void assertHardDeleteSetThenDeleted(final Client client) {
        final String flowId = "get-attributes-hard-delete-" + UUID.randomUUID();
        client.startFlow(WORKFLOW, flowId, null, startOptions());
        final GetAttributesWorkflow stub =
                client.newRpcStub(GetAttributesWorkflow.class, flowId);
        client.invokeRPC(stub::setSecret, "rotated-secret");
        assertEquals(
                Optional.of("rotated-secret"),
                client.getAttributes(flowId, WORKFLOW.secret).get(WORKFLOW.secret));

        client.invokeRPC(stub::clearSecretWithNull);
        final AttributeValues afterDeletion =
                client.getAttributes(flowId, WORKFLOW.secret, WORKFLOW.label);
        assertEquals(Optional.empty(), afterDeletion.get(WORKFLOW.secret));
        assertEquals(Optional.of("ready"), afterDeletion.get(WORKFLOW.label));

        final AttributeMapInstance<String> secretNote =
                WORKFLOW.secretNotes.instance(GetAttributesWorkflow.SECRET_NOTE_INSTANCE);
        assertEquals(Optional.empty(), client.getAttributes(flowId, secretNote).get(secretNote));
        client.invokeRPC(stub::setSecretNote, "secret-note");
        assertEquals(
                Optional.of("secret-note"),
                client.getAttributes(flowId, secretNote).get(
                        WORKFLOW.secretNotes.instance(GetAttributesWorkflow.SECRET_NOTE_INSTANCE)));
        client.invokeRPC(stub::clearSecretNoteWithNull);
        final AttributeValues afterNoteDeletion =
                client.getAttributes(flowId, secretNote, WORKFLOW.label);
        assertEquals(Optional.empty(), afterNoteDeletion.get(secretNote));
        assertEquals(Optional.of("ready"), afterNoteDeletion.get(WORKFLOW.label));
        client.stopFlow(flowId);
    }

    private void assertClosedRunReadWithFreshCache(final String flowId) throws Exception {
        try (BlobCache freshCache = BlobCache.open(new BlobCacheConfig(
                        freshCacheDirectory.toString(),
                        64L * 1024L * 1024L));
                Client freshClient = new Client(
                        new Registry(Collections.<Flow<?>>singletonList(WORKFLOW)),
                        freshCache,
                        new ClientOptions(SERVER_ADDRESS))) {
            final AttributeValues closed = freshClient.getAttributes(
                    flowId,
                    WORKFLOW.secret,
                    WORKFLOW.largeSecret,
                    WORKFLOW.closedRunLargeValue,
                    WORKFLOW.unsetCount);
            assertEquals(Optional.of(SECRET), closed.get(WORKFLOW.secret));
            assertEquals(Optional.of(LARGE_SECRET), closed.get(WORKFLOW.largeSecret));
            assertEquals(
                    Optional.of(CLOSED_RUN_LARGE_VALUE),
                    closed.get(WORKFLOW.closedRunLargeValue));
            assertEquals(Optional.empty(), closed.get(WORKFLOW.unsetCount));
        }
        assertTrue(
                cacheContains(freshCacheDirectory, CLOSED_RUN_LARGE_VALUE),
                "the closed-run large value was not loaded through LoadBlobs");
        assertCacheExcludes(freshCacheDirectory, SECRET);
        assertCacheExcludes(freshCacheDirectory, LARGE_SECRET);
    }

    private static void assertCacheExcludes(final Path directory, final String value)
            throws IOException {
        assertFalse(cacheContains(directory, value), "BlobCache stored a hard-delete value");
    }

    private static boolean cacheContains(final Path directory, final String value)
            throws IOException {
        final byte[] needle = value.getBytes(StandardCharsets.UTF_8);
        final List<Path> files;
        try (java.util.stream.Stream<Path> paths = Files.walk(directory)) {
            files = paths.filter(Files::isRegularFile).collect(Collectors.toList());
        }
        for (Path file : files) {
            if (indexOf(Files.readAllBytes(file), needle) >= 0) {
                return true;
            }
        }
        return false;
    }

    private static int indexOf(final byte[] haystack, final byte[] needle) {
        for (int start = 0; start + needle.length <= haystack.length; start++) {
            boolean isMatch = true;
            for (int offset = 0; offset < needle.length; offset++) {
                if (haystack[start + offset] != needle[offset]) {
                    isMatch = false;
                    break;
                }
            }
            if (isMatch) {
                return start;
            }
        }
        return -1;
    }

    private static Map<String, Value> readRawAttributes(
            final FlowServiceGrpc.FlowServiceBlockingStub flowService,
            final String flowId,
            final String... keys) {
        final Map<String, Value> attributes = new HashMap<String, Value>();
        for (KV attribute : flowService
                .withDeadlineAfter(30, TimeUnit.SECONDS)
                .getAttributes(GetAttributesRequest.newBuilder()
                        .setFlowId(flowId)
                        .addAllKeys(Arrays.asList(keys))
                        .build())
                .getAttributesList()) {
            attributes.put(attribute.getKey(), attribute.getValue());
        }
        return attributes;
    }

    private static String repeat(final String value, final int count) {
        return String.join("", Collections.nCopies(count, value));
    }
}
