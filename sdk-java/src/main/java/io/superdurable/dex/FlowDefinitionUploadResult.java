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

/**
 * Describes the outcome of uploading one Flow definition and the document stored afterwards.
 *
 * <p>{@link Client#uploadFlowDefinitions(java.util.List, boolean)} returns one result for each
 * uploaded document, in request order. The stored digest and generation time describe the
 * document that the Server holds after the call. They differ from the uploaded document only for
 * {@link FlowDefinitionUploadOutcome#KEPT_EXISTING}.
 *
 * @since 1.45.0
 */
public final class FlowDefinitionUploadResult {
    private final String flowType;
    private final FlowDefinitionUploadOutcome outcome;
    private final String storedDigest;
    private final Instant storedGeneratedAt;

    FlowDefinitionUploadResult(
            final String flowType,
            final FlowDefinitionUploadOutcome outcome,
            final String storedDigest,
            final Instant storedGeneratedAt) {
        this.flowType = flowType;
        this.outcome = outcome;
        this.storedDigest = storedDigest;
        this.storedGeneratedAt = storedGeneratedAt;
    }

    /**
     * Returns the Flow type of the uploaded document.
     *
     * @return the Flow type
     * @since 1.45.0
     */
    public String getFlowType() {
        return flowType;
    }

    /**
     * Returns what the Server did with the uploaded document.
     *
     * @return the nonnull outcome
     * @since 1.45.0
     */
    public FlowDefinitionUploadOutcome getOutcome() {
        return outcome;
    }

    /**
     * Returns the digest of the document stored after the call.
     *
     * @return the lowercase hexadecimal SHA-256 digest of the stored content
     * @since 1.45.0
     */
    public String getStoredDigest() {
        return storedDigest;
    }

    /**
     * Returns the generation time of the document stored after the call.
     *
     * @return the stored generation time at microsecond precision, or {@code null} if the Server
     *     did not report one
     * @since 1.45.0
     */
    public Instant getStoredGeneratedAt() {
        return storedGeneratedAt;
    }

    /**
     * Returns a diagnostic description of this result.
     *
     * @return the Flow type, outcome, stored digest, and stored generation time
     */
    @Override
    public String toString() {
        return "FlowDefinitionUploadResult{flowType=" + flowType + ", outcome=" + outcome
                + ", storedDigest=" + storedDigest + ", storedGeneratedAt=" + storedGeneratedAt + "}";
    }
}
