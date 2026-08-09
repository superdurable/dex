/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

/**
 * Reports that the SDK cannot encode, hydrate, or decode an application value.
 *
 * <p>This local exception identifies unsupported wire values, non-finite numbers, unhydrated blobs,
 * concrete-class mismatches, and Jackson serialization failures. Verify the registered value class
 * and application {@code ObjectMapper} configuration before retrying.
 */
public final class ValueMappingException extends RuntimeException {
    /**
     * Creates a value-mapping failure without an underlying cause.
     *
     * @param message the user-actionable mapping failure
     */
    public ValueMappingException(final String message) {
        super(message);
    }

    /**
     * Creates a value-mapping failure with its underlying serializer error.
     *
     * @param message the user-actionable mapping failure
     * @param cause the underlying mapping or serialization error
     */
    public ValueMappingException(final String message, final Throwable cause) {
        super(message, cause);
    }
}
