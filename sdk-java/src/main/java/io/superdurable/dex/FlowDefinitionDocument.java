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

import java.time.Instant;
import java.util.Arrays;

/**
 * Holds one Flow Definition Graph JSON document in memory for upload to the Dex Server.
 *
 * <p>Dex Web draws a Flow type's graph from the document stored for it. Pass documents to
 * {@link WorkerOptions.Builder#flowDefinitions(java.util.List)} to upload them when a Worker
 * starts, or to {@link Client#uploadFlowDefinitions(java.util.List, boolean)} from a deploy
 * script. Use {@link WorkerOptions.Builder#flowDefinitionPaths(java.util.List)} instead when the
 * documents are files, such as the output of {@code dexcli visualize}.
 *
 * <p>The Flow type must equal {@code flow.name} in the content. The content must be a UTF-8 JSON
 * object whose {@code schemaVersion} starts with {@code 2.}. The Dex Server compares
 * {@code generatedAt} with the stored document: a different document replaces the stored one
 * only when its generation time is strictly later. Pass a fixed time for a document that is
 * generated from a template, such as the release time of the template, so every Worker sends
 * the same time.
 *
 * <pre>{@code
 * byte[] content = Files.readAllBytes(Paths.get("flow-definitions/OrderFlow.json"));
 * FlowDefinitionDocument document = new FlowDefinitionDocument(
 *         "OrderFlow", content, Instant.parse("2026-11-01T00:00:00Z"));
 * WorkerOptions options = WorkerOptions.newBuilder()
 *         .flowDefinitions(Collections.singletonList(document))
 *         .build();
 * }</pre>
 *
 * <p>Instances are immutable. The constructor and {@link #getContent()} copy the content array.
 *
 * @since 1.45.0
 */
public final class FlowDefinitionDocument {
    private final String flowType;
    private final byte[] content;
    private final Instant generatedAt;

    /**
     * Creates a document from its Flow type, JSON content, and generation time.
     *
     * @param flowType the nonblank Flow type that the document describes; it must equal
     *     {@code flow.name} in the content
     * @param content the nonnull UTF-8 JSON bytes of the Flow Definition Graph; the array is copied
     * @param generatedAt the nonnull time when the document was generated
     * @throws IllegalArgumentException if flowType is null or blank, or content or generatedAt is
     *     null
     * @since 1.45.0
     */
    public FlowDefinitionDocument(
            final String flowType,
            final byte[] content,
            final Instant generatedAt) {
        if (flowType == null || flowType.trim().isEmpty()) {
            throw new IllegalArgumentException("Flow definition flowType must be nonblank");
        }
        if (content == null) {
            throw new IllegalArgumentException("Flow definition content is required for " + flowType);
        }
        if (generatedAt == null) {
            throw new IllegalArgumentException("Flow definition generatedAt is required for " + flowType);
        }
        this.flowType = flowType;
        this.content = Arrays.copyOf(content, content.length);
        this.generatedAt = generatedAt;
    }

    /**
     * Returns the Flow type that the document describes.
     *
     * @return the nonblank Flow type
     * @since 1.45.0
     */
    public String getFlowType() {
        return flowType;
    }

    /**
     * Returns a copy of the JSON content.
     *
     * @return a new array holding the UTF-8 JSON bytes
     * @since 1.45.0
     */
    public byte[] getContent() {
        return Arrays.copyOf(content, content.length);
    }

    /**
     * Returns the time when the document was generated.
     *
     * @return the nonnull generation time
     * @since 1.45.0
     */
    public Instant getGeneratedAt() {
        return generatedAt;
    }

    byte[] contentBytes() {
        return content;
    }

    /**
     * Returns a diagnostic description with the content length instead of the content.
     *
     * @return the Flow type, content length in bytes, and generation time
     */
    @Override
    public String toString() {
        return "FlowDefinitionDocument{flowType=" + flowType + ", contentBytes=" + content.length
                + ", generatedAt=" + generatedAt + "}";
    }
}
