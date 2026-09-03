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

/** Indicates that an RPC did not load the requested pending Channel messages. */
public final class ChannelMessagesNotLoadedException extends IllegalStateException {
    /** Creates an exception with an actionable missing-load message. */
    public ChannelMessagesNotLoadedException(final String message) {
        super(message);
    }
}
