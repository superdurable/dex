/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.time.Instant;

/**
 * Describes one retained Stream message returned by {@link Client#readStream}.
 *
 * @param <T> the decoded message type
 */
public final class StreamMessage<T> {
    private final T value;
    private final String resumeToken;
    private final Instant createdTime;
    private final String idempotencyKey;

    StreamMessage(
            final T value,
            final String resumeToken,
            final Instant createdTime,
            final String idempotencyKey) {
        this.value = value;
        this.resumeToken = resumeToken;
        this.createdTime = createdTime;
        this.idempotencyKey = idempotencyKey;
    }

    /**
     * Returns the decoded application message.
     *
     * @return the message value
     */
    public T getValue() {
        return value;
    }

    /**
     * Returns the token for the next read.
     *
     * @return the opaque resume token
     */
    public String getResumeToken() {
        return resumeToken;
    }

    /**
     * Returns the server-assigned creation time.
     *
     * @return the UTC creation time
     */
    public Instant getCreatedTime() {
        return createdTime;
    }

    /**
     * Returns the producer idempotency key.
     *
     * @return the client or generated Step key
     */
    public String getIdempotencyKey() {
        return idempotencyKey;
    }
}
