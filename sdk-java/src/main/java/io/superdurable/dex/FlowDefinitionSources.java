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
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.logging.Level;
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
     * Returns in-memory documents followed by readable file documents, one per Flow type.
     *
     * <p>Unreadable or non-definition files are logged and skipped. A Flow type configured twice,
     * or an in-memory document that is not a JSON object naming its Flow type, throws.
     */
    static List<FlowDefinitionDocument> collect(
            final List<Path> paths,
            final List<FlowDefinitionDocument> documents) {
        final Map<String, String> sourceByFlowType = new LinkedHashMap<String, String>();
        final List<FlowDefinitionDocument> collected = new ArrayList<FlowDefinitionDocument>();
        for (FlowDefinitionDocument document : documents) {
            requireMatchingFlowName(document);
            recordSource(sourceByFlowType, document.getFlowType(), "an in-memory FlowDefinitionDocument");
            collected.add(document);
        }
        for (Path file : listDefinitionFiles(paths)) {
            final FlowDefinitionDocument document = readFile(file);
            if (document != null) {
                recordSource(sourceByFlowType, document.getFlowType(), "file " + file);
                collected.add(document);
            }
        }
        return Collections.unmodifiableList(collected);
    }

    private static void requireMatchingFlowName(final FlowDefinitionDocument document) {
        final String flowName;
        try {
            flowName = readFlowName(JSON.readTree(document.contentBytes()));
        } catch (IOException invalid) {
            throw new IllegalArgumentException("Flow definition for Flow type " + document.getFlowType()
                    + " is not valid JSON", invalid);
        }
        if (!document.getFlowType().equals(flowName)) {
            throw new IllegalArgumentException("Flow definition for Flow type " + document.getFlowType()
                    + " has flow.name " + flowName + "; they must be equal");
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

    private static List<Path> listDefinitionFiles(final List<Path> paths) {
        final Set<Path> files = new LinkedHashSet<Path>();
        for (Path configured : paths) {
            final Path path = configured.toAbsolutePath().normalize();
            if (Files.isDirectory(path)) {
                files.addAll(listJsonFiles(path));
            } else if (Files.isRegularFile(path)) {
                files.add(path);
            } else {
                LOGGER.warning("Flow definition path " + configured
                        + " is not a readable file or directory; skipping it");
            }
        }
        return new ArrayList<Path>(files);
    }

    private static List<Path> listJsonFiles(final Path directory) {
        try (Stream<Path> walk = Files.walk(directory)) {
            return walk
                    .filter(Files::isRegularFile)
                    .filter(file -> file.getFileName().toString().toLowerCase(Locale.ROOT).endsWith(".json"))
                    .sorted()
                    .collect(Collectors.toList());
        } catch (IOException | RuntimeException failure) {
            LOGGER.log(Level.WARNING, "cannot list Flow definition directory " + directory
                    + "; skipping it", failure);
            return Collections.emptyList();
        }
    }

    private static FlowDefinitionDocument readFile(final Path file) {
        final byte[] content;
        final JsonNode root;
        try {
            content = Files.readAllBytes(file);
            root = JSON.readTree(content);
        } catch (IOException failure) {
            LOGGER.log(Level.WARNING, "cannot read Flow definition file " + file + "; skipping it", failure);
            return null;
        }
        final String flowName = readFlowName(root);
        if (flowName == null || flowName.trim().isEmpty()) {
            LOGGER.info("file " + file + " has no flow.name, so it is not a Flow definition; skipping it");
            return null;
        }
        final Instant generatedAt = readGeneratedAt(file, root);
        if (generatedAt == null) {
            return null;
        }
        return new FlowDefinitionDocument(flowName, content, generatedAt);
    }

    private static String readFlowName(final JsonNode root) {
        final JsonNode flowName = root == null ? null : root.path("flow").get("name");
        return flowName != null && flowName.isTextual() ? flowName.asText() : null;
    }

    private static Instant readGeneratedAt(final Path file, final JsonNode root) {
        final JsonNode generatedAt = root.get("generatedAt");
        if (generatedAt != null && generatedAt.isTextual()) {
            try {
                return OffsetDateTime.parse(generatedAt.asText()).toInstant();
            } catch (DateTimeParseException invalid) {
                LOGGER.warning("Flow definition file " + file + " has an invalid generatedAt "
                        + generatedAt.asText() + "; using the file modification time");
            }
        }
        try {
            return Files.getLastModifiedTime(file).toInstant();
        } catch (IOException failure) {
            LOGGER.log(Level.WARNING, "cannot read the modification time of Flow definition file "
                    + file + "; skipping it", failure);
            return null;
        }
    }
}
