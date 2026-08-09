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

public final class ValueMappingException extends RuntimeException {
    public ValueMappingException(final String message) {
        super(message);
    }

    public ValueMappingException(final String message, final Throwable cause) {
        super(message, cause);
    }
}
