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
import io.superdurable.dex.FlowDefinitionDocument;
import io.superdurable.dex.FlowDefinitionUploadOutcome;
import io.superdurable.dex.FlowDefinitionUploadResult;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import io.superdurable.gen.FlowDefinition;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetFlowDefinitionsRequest;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.nio.file.attribute.FileTime;
import java.security.MessageDigest;
import java.time.Duration;
import java.time.Instant;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class FlowDefinitionUploadTest {
    @TempDir
    Path cacheDirectory;

    @TempDir
    Path definitionDirectory;

    @Test
    void testWorkerUploadsRegisteredDefinitionsFromPathsAndMemory() throws Exception {
        final String pathFlowType = flowType("Path");
        final String mtimeFlowType = flowType("Mtime");
        final String memoryFlowType = flowType("Memory");
        final String unregisteredFlowType = flowType("Unregistered");
        final Path workingDirectory = Paths.get("").toAbsolutePath().normalize();
        final String pathContent = definition(pathFlowType,
                workingDirectory.resolve("src/main/java/PathFlow.java").toString(),
                "\"generatedAt\": \"2026-11-01T08:00:00+08:00\", ");
        final Path nested = Files.createDirectories(definitionDirectory.resolve("nested"));
        Files.write(nested.resolve("path-flow.json"), pathContent.getBytes(StandardCharsets.UTF_8));
        final Path mtimeFile = definitionDirectory.resolve("mtime-flow.json");
        Files.write(mtimeFile, definition(mtimeFlowType, "relative/MtimeFlow.java", "")
                .getBytes(StandardCharsets.UTF_8));
        final Instant mtime = Instant.parse("2026-10-30T12:34:56Z");
        Files.setLastModifiedTime(mtimeFile, FileTime.from(mtime));
        Files.write(definitionDirectory.resolve("unregistered-flow.json"),
                definition(unregisteredFlowType, "/elsewhere/Unregistered.java", "")
                        .getBytes(StandardCharsets.UTF_8));
        Files.write(definitionDirectory.resolve("package.json"),
                "{\"name\": \"not-a-flow-definition\"}".getBytes(StandardCharsets.UTF_8));
        Files.write(definitionDirectory.resolve("notes.txt"), "not JSON".getBytes(StandardCharsets.UTF_8));
        final String memoryContent = definition(memoryFlowType, "/opt/elsewhere/app/memory_flow.go", "");
        final Instant memoryGeneratedAt = Instant.parse("2026-11-02T00:00:00Z");
        final FlowDefinitionUploadWorkflow pathWorkflow = new FlowDefinitionUploadWorkflow(pathFlowType);
        final WorkerOptions.Builder options = WorkerOptions.newBuilder()
                .flowDefinitionPaths(Collections.singletonList(definitionDirectory))
                .flowDefinitions(Collections.singletonList(new FlowDefinitionDocument(
                        memoryFlowType, memoryContent.getBytes(StandardCharsets.UTF_8), memoryGeneratedAt)));

        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                options,
                pathWorkflow,
                new FlowDefinitionUploadWorkflow(mtimeFlowType),
                new FlowDefinitionUploadWorkflow(memoryFlowType))) {
            final Map<String, FlowDefinition> stored = getFlowDefinitions(
                    pathFlowType, mtimeFlowType, memoryFlowType, unregisteredFlowType);
            assertEquals(3, stored.size());
            assertFalse(stored.containsKey(unregisteredFlowType));

            final FlowDefinition pathDefinition = stored.get(pathFlowType);
            assertEquals(pathContent.replace(
                            workingDirectory.resolve("src/main/java/PathFlow.java").toString(),
                            "src/main/java/PathFlow.java"),
                    pathDefinition.getContent().toStringUtf8());
            assertEquals(Instant.parse("2026-11-01T00:00:00Z").getEpochSecond(),
                    pathDefinition.getInfo().getGeneratedAt().getSeconds());
            assertTrue(pathDefinition.getInfo().getUploader().startsWith("java/"));

            final FlowDefinition mtimeDefinition = stored.get(mtimeFlowType);
            assertEquals(definition(mtimeFlowType, "relative/MtimeFlow.java", ""),
                    mtimeDefinition.getContent().toStringUtf8());
            assertEquals(mtime.getEpochSecond(), mtimeDefinition.getInfo().getGeneratedAt().getSeconds());

            final FlowDefinition memoryDefinition = stored.get(memoryFlowType);
            assertEquals(memoryContent.replace("/opt/elsewhere/app/memory_flow.go", "memory_flow.go"),
                    memoryDefinition.getContent().toStringUtf8());
            assertEquals(memoryGeneratedAt.getEpochSecond(),
                    memoryDefinition.getInfo().getGeneratedAt().getSeconds());

            assertFlowCompletes(environment, pathWorkflow);
        }
    }

    @Test
    void testWorkerRejectsOneFlowTypeFromTwoSources() throws Exception {
        final String duplicateFlowType = flowType("Duplicate");
        Files.write(definitionDirectory.resolve("duplicate.json"),
                definition(duplicateFlowType, "Duplicate.java", "").getBytes(StandardCharsets.UTF_8));
        final WorkerOptions.Builder options = WorkerOptions.newBuilder()
                .flowDefinitionPaths(Collections.singletonList(definitionDirectory))
                .flowDefinitions(Collections.singletonList(new FlowDefinitionDocument(
                        duplicateFlowType,
                        definition(duplicateFlowType, "Duplicate.java", "").getBytes(StandardCharsets.UTF_8),
                        Instant.parse("2026-11-01T00:00:00Z"))));

        final IllegalArgumentException failure = assertThrows(
                IllegalArgumentException.class,
                () -> DexDevTestEnvironment.start(
                        cacheDirectory, options, new FlowDefinitionUploadWorkflow(duplicateFlowType)));

        assertTrue(failure.getMessage().contains("more than one Flow definition"));
        assertTrue(failure.getMessage().contains(duplicateFlowType));
    }

    @Test
    void testWorkerProcessesTasksWhenServerRejectsUpload() throws Exception {
        final String rejectedFlowType = flowType("Rejected");
        final String invalidSchemaVersion = "{\"schemaVersion\": \"1.0\", \"flow\": {\"name\": \""
                + rejectedFlowType + "\"}}";
        final FlowDefinitionUploadWorkflow workflow = new FlowDefinitionUploadWorkflow(rejectedFlowType);
        final WorkerOptions.Builder options = WorkerOptions.newBuilder()
                .flowDefinitions(Collections.singletonList(new FlowDefinitionDocument(
                        rejectedFlowType,
                        invalidSchemaVersion.getBytes(StandardCharsets.UTF_8),
                        Instant.parse("2026-11-01T00:00:00Z"))));

        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory, options, workflow)) {
            assertFlowCompletes(environment, workflow);
            assertTrue(getFlowDefinitions(rejectedFlowType).isEmpty());
        }
    }

    @Test
    void testClientUploadReportsEachOutcome() throws Exception {
        final String clientFlowType = flowType("Client");
        final byte[] first = definition(clientFlowType, "ClientFlow.java", "").getBytes(StandardCharsets.UTF_8);
        final byte[] second = definition(clientFlowType, "ClientFlowV2.java", "")
                .getBytes(StandardCharsets.UTF_8);
        final Instant firstGeneratedAt = Instant.parse("2026-11-03T00:00:00Z");
        final Instant olderGeneratedAt = Instant.parse("2026-11-02T00:00:00Z");

        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory, new FlowDefinitionUploadWorkflow(flowType("ClientWorker")))) {
            final FlowDefinitionUploadResult created = uploadOne(environment,
                    new FlowDefinitionDocument(clientFlowType, first, firstGeneratedAt), false);
            assertEquals(FlowDefinitionUploadOutcome.CREATED, created.getOutcome());
            assertEquals(clientFlowType, created.getFlowType());
            assertEquals(sha256Hex(first), created.getStoredDigest());
            assertEquals(firstGeneratedAt, created.getStoredGeneratedAt());

            assertEquals(FlowDefinitionUploadOutcome.UNCHANGED, uploadOne(environment,
                    new FlowDefinitionDocument(clientFlowType, first, firstGeneratedAt), false).getOutcome());

            final FlowDefinitionUploadResult kept = uploadOne(environment,
                    new FlowDefinitionDocument(clientFlowType, second, olderGeneratedAt), false);
            assertEquals(FlowDefinitionUploadOutcome.KEPT_EXISTING, kept.getOutcome());
            assertEquals(sha256Hex(first), kept.getStoredDigest());
            assertEquals(firstGeneratedAt, kept.getStoredGeneratedAt());

            final FlowDefinitionUploadResult replaced = uploadOne(environment,
                    new FlowDefinitionDocument(clientFlowType, second, olderGeneratedAt), true);
            assertEquals(FlowDefinitionUploadOutcome.REPLACED, replaced.getOutcome());
            assertEquals(sha256Hex(second), replaced.getStoredDigest());
            assertEquals(olderGeneratedAt, replaced.getStoredGeneratedAt());
        }
    }

    private static FlowDefinitionUploadResult uploadOne(
            final DexDevTestEnvironment environment,
            final FlowDefinitionDocument document,
            final boolean force) {
        final List<FlowDefinitionUploadResult> results = environment.client()
                .uploadFlowDefinitions(Collections.singletonList(document), force);
        assertEquals(1, results.size());
        return results.get(0);
    }

    private static void assertFlowCompletes(
            final DexDevTestEnvironment environment,
            final FlowDefinitionUploadWorkflow workflow) {
        final String flowId = "flow-definition-upload-" + UUID.randomUUID();
        environment.client().startFlow(workflow, flowId, 41);
        assertEquals(Integer.valueOf(42), environment.client()
                .waitForFlow(flowId, Duration.ofSeconds(30))
                .getSingleOutput(Integer.class));
    }

    private static Map<String, FlowDefinition> getFlowDefinitions(final String... flowTypes)
            throws InterruptedException {
        final ManagedChannel channel = ManagedChannelBuilder
                .forTarget(DexDevTestEnvironment.serverAddress())
                .usePlaintext()
                .build();
        try {
            final List<FlowDefinition> definitions = FlowServiceGrpc.newBlockingStub(channel)
                    .getFlowDefinitions(GetFlowDefinitionsRequest.newBuilder()
                            .addAllFlowTypes(Arrays.asList(flowTypes))
                            .build())
                    .getDefinitionsList();
            final Map<String, FlowDefinition> byFlowType = new HashMap<String, FlowDefinition>();
            for (FlowDefinition definition : definitions) {
                byFlowType.put(definition.getInfo().getFlowType(), definition);
            }
            return byFlowType;
        } finally {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    private static String definition(final String flowType, final String sourcePath, final String extraFields) {
        return "{\n  \"schemaVersion\": \"2.0\", " + extraFields
                + "\"source\": {\"language\": \"java\", \"path\": \"" + sourcePath + "\"},\n"
                + "  \"flow\": {\"name\": \"" + flowType + "\", \"weight\": 1.50},\n"
                + "  \"nodes\": [], \"edges\": []\n}\n";
    }

    private static String flowType(final String purpose) {
        return "FlowDefinitionUpload" + purpose + UUID.randomUUID().toString().replace("-", "");
    }

    private static String sha256Hex(final byte[] content) throws Exception {
        final StringBuilder hex = new StringBuilder();
        for (byte value : MessageDigest.getInstance("SHA-256").digest(content)) {
            hex.append(String.format("%02x", value));
        }
        return hex.toString();
    }
}
