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

/**
 * Reports what the Dex Server did with one uploaded Flow definition.
 *
 * <p>The Server decides each Flow type separately. A stored document is replaced only by a
 * different document with a strictly later generation time, unless the upload uses force.
 * See {@link Client#uploadFlowDefinitions(java.util.List, boolean)} for an example.
 *
 * @since 1.45.0
 */
public enum FlowDefinitionUploadOutcome {
    /** No document was stored for the Flow type; the uploaded one is now stored. */
    CREATED,
    /** The uploaded document replaced a different, older one. */
    REPLACED,
    /** The stored document is identical; nothing was written. */
    UNCHANGED,
    /**
     * The stored document differs and is at least as new, so it stays.
     *
     * <p>Upload with force to replace it anyway.
     */
    KEPT_EXISTING,
    /** The Server reported an outcome that this SDK version does not know. */
    UNKNOWN
}
