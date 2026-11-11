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

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.logging.Logger;
import java.util.stream.Collectors;
import java.util.stream.Stream;

/** Collects the Flow definitions configured on a Worker from files and in-memory documents. */
final class FlowDefinitionSources {
    private static final Logger LOGGER = Logger.getLogger(FlowDefinitionSources.class.getName());
    private static final ObjectMapper JSON = new ObjectMapper();

    private FlowDefinitionSources() {
    }

    /**
     * Returns in-memory documents followed by file documents, one per Flow type.
     *
     * <p>A directory entry that is not a Flow Definition Graph 2.x is logged and skipped.
     *
     * @throws IllegalArgumentException for a missing or unreadable path, an invalid explicit file,
     *     a malformed generatedAt, a Flow type configured twice, or an in-memory document that is
     *     not a Flow Definition Graph 2.x naming its Flow type
     */
    static List<FlowDefinitionDocument> collect(
            final List<Path> paths,
            final List<FlowDefinitionDocument> documents) {
        final Map<String, String> sourceByFlowType = new LinkedHashMap<String, String>();
        final List<FlowDefinitionDocument> collected = new ArrayList<FlowDefinitionDocument>();
        for (FlowDefinitionDocument document : documents) {
            requireValidDocument(document);
            recordSource(sourceByFlowType, document.getFlowType(), "an in-memory FlowDefinitionDocument");
            collected.add(document);
        }
        final Set<Path> seen = new LinkedHashSet<Path>();
        for (Path configured : paths) {
            final Path path = configured.toAbsolutePath().normalize();
            if (Files.isDirectory(path)) {
                for (Path file : listJsonFiles(path)) {
                    if (seen.add(file)) {
                        addDocument(collected, sourceByFlowType, file, readDirectoryEntry(file));
                    }
                }
            } else if (Files.isRegularFile(path) && Files.isReadable(path)) {
                if (seen.add(path)) {
                    addDocument(collected, sourceByFlowType, path, readExplicitFile(path));
                }
            } else {
                throw new IllegalArgumentException("Flow definition path " + configured
                        + " does not exist or cannot be read");
            }
        }
        return Collections.unmodifiableList(collected);
    }

    private static void addDocument(
            final List<FlowDefinitionDocument> collected,
            final Map<String, String> sourceByFlowType,
            final Path file,
            final FlowDefinitionDocument document) {
        if (document != null) {
            recordSource(sourceByFlowType, document.getFlowType(), "file " + file);
            collected.add(document);
        }
    }

    /**
     * Requires an in-memory document to be a Flow Definition Graph 2.x naming its Flow type.
     *
     * @throws IllegalArgumentException if the content is not such a document
     */
    static void requireValidDocument(final FlowDefinitionDocument document) {
        final InspectedDefinition inspected = InspectedDefinition.inspect(document.contentBytes());
        if (inspected.notDefinitionReason != null) {
            throw new IllegalArgumentException("Flow definition for Flow type " + document.getFlowType()
                    + " " + inspected.notDefinitionReason);
        }
        if (!document.getFlowType().equals(inspected.flowName)) {
            throw new IllegalArgumentException("Flow definition for Flow type " + document.getFlowType()
                    + " has flow.name " + inspected.flowName + "; they must be equal");
        }
    }

    private static void recordSource(
            final Map<String, String> sourceByFlowType,
            final String flowType,
            final String source) {
        final String previous = sourceByFlowType.putIfAbsent(flowType, source);
        if (previous != null) {
            throw new IllegalArgumentException("Flow type " + flowType
                    + " has more than one Flow definition: " + previous + " and " + source);
        }
    }

    private static List<Path> listJsonFiles(final Path directory) {
        try (Stream<Path> walk = Files.walk(directory)) {
            return walk
                    .filter(Files::isRegularFile)
                    .filter(file -> file.getFileName().toString().toLowerCase(Locale.ROOT).endsWith(".json"))
                    .sorted()
                    .collect(Collectors.toList());
        } catch (IOException | RuntimeException failure) {
            throw new IllegalArgumentException("Flow definition directory " + directory
                    + " cannot be read", failure);
        }
    }

    private static FlowDefinitionDocument readExplicitFile(final Path file) {
        final byte[] content = readContent(file);
        final InspectedDefinition inspected = InspectedDefinition.inspect(content);
        if (inspected.notDefinitionReason != null) {
            throw new IllegalArgumentException("Flow definition file " + file + " "
                    + inspected.notDefinitionReason);
        }
        return toDocument(file, content, inspected);
    }

    private static FlowDefinitionDocument readDirectoryEntry(final Path file) {
        final byte[] content = readContent(file);
        final InspectedDefinition inspected = InspectedDefinition.inspect(content);
        if (inspected.notDefinitionReason != null) {
            LOGGER.info("skipping " + file + " because it " + inspected.notDefinitionReason);
            return null;
        }
        return toDocument(file, content, inspected);
    }

    private static byte[] readContent(final Path file) {
        try {
            return Files.readAllBytes(file);
        } catch (IOException failure) {
            throw new IllegalArgumentException("Flow definition file " + file + " cannot be read", failure);
        }
    }

    private static FlowDefinitionDocument toDocument(
            final Path file,
            final byte[] content,
            final InspectedDefinition inspected) {
        final Instant generatedAt;
        if (inspected.generatedAt == null) {
            try {
                generatedAt = FlowDefinitionRules.truncateToMillis(Files.getLastModifiedTime(file).toInstant());
            } catch (IOException failure) {
                throw new IllegalArgumentException("cannot read the modification time of Flow definition file "
                        + file, failure);
            }
        } else if (!inspected.generatedAt.isTextual()) {
            throw new IllegalArgumentException("Flow definition file " + file
                    + " has a generatedAt that is not a string");
        } else {
            try {
                generatedAt = FlowDefinitionRules.parseGeneratedAt(inspected.generatedAt.asText());
            } catch (IllegalArgumentException invalid) {
                throw new IllegalArgumentException("Flow definition file " + file + ": " + invalid.getMessage(),
                        invalid);
            }
        }
        return new FlowDefinitionDocument(inspected.flowName, content, generatedAt);
    }

    private static String readFlowName(final JsonNode root) {
        final JsonNode flowName = root == null ? null : root.path("flow").get("name");
        return flowName != null && flowName.isTextual() ? flowName.asText() : null;
    }

    /** Reports whether content is a Flow Definition Graph 2.x and, if so, its fields. */
    private static final class InspectedDefinition {
        private final String notDefinitionReason;
        private final String flowName;
        private final JsonNode generatedAt;

        private InspectedDefinition(
                final String notDefinitionReason,
                final String flowName,
                final JsonNode generatedAt) {
            this.notDefinitionReason = notDefinitionReason;
            this.flowName = flowName;
            this.generatedAt = generatedAt;
        }

        static InspectedDefinition inspect(final byte[] content) {
            final JsonNode root;
            try {
                StandardCharsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(content));
                root = JSON.readTree(content);
            } catch (IOException invalid) {
                return notDefinition("is not a UTF-8 JSON object");
            }
            if (root == null || !root.isObject()) {
                return notDefinition("is not a UTF-8 JSON object");
            }
            final JsonNode schemaVersion = root.get("schemaVersion");
            if (schemaVersion == null || !schemaVersion.isTextual() || !schemaVersion.asText().startsWith("2.")) {
                return notDefinition("has no schemaVersion starting with 2.");
            }
            final String flowName = readFlowName(root);
            if (flowName == null || flowName.isEmpty()) {
                return notDefinition("has no flow.name");
            }
            return new InspectedDefinition(null, flowName, root.get("generatedAt"));
        }

        private static InspectedDefinition notDefinition(final String reason) {
            return new InspectedDefinition(reason, null, null);
        }
    }
}
