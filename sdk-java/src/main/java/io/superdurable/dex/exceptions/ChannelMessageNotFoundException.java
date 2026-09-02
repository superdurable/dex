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

import io.grpc.Status;

/** Thrown when a requested Channel message is no longer pending. */
public final class ChannelMessageNotFoundException extends DexServiceException {
    /**
     * Creates the exception from Dex service status metadata.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided failure detail
     * @param cause the original transport failure
     */
    public ChannelMessageNotFoundException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.CHANNEL_MESSAGE_NOT_FOUND, detail, cause);
    }
}
