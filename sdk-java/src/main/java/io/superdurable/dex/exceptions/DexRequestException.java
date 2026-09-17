/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.exceptions;

import io.grpc.Status;

/**
 * Reports a Dex request failure without a more specific public exception type.
 *
 * <p>Catch a concrete domain exception when the outcome has application meaning. This fallback
 * preserves transport and Dex-specific status metadata for diagnostics when Dex cannot classify
 * the response more precisely.
 */
public final class DexRequestException extends DexServiceException {
    /**
     * Creates an unclassified request failure from Dex service status metadata.
     *
     * @param code the nonnull gRPC status code
     * @param subStatus the Dex-specific category, or {@code null} when unavailable
     * @param detail the server-provided error detail, which may be {@code null}
     * @param cause the original transport failure, which may be {@code null}
     */
    public DexRequestException(
            final Status.Code code,
            final ErrorSubStatus subStatus,
            final String detail,
            final Throwable cause) {
        super(code, subStatus, detail, cause);
    }
}
