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

/**
 * Identifies one typed value that is still pending in a Channel.
 *
 * @param <T> the decoded Channel value type
 */
public final class ChannelMessage<T> {
    private final String messageId;
    private final T value;

    ChannelMessage(final String messageId, final T value) {
        this.messageId = messageId;
        this.value = value;
    }

    /**
     * Returns the UUIDv7 assigned by Dex when the message was published.
     *
     * @return the server-assigned message ID
     */
    public String getMessageId() {
        return messageId;
    }

    /**
     * Returns the decoded Channel value.
     *
     * @return the message value
     */
    public T getValue() {
        return value;
    }
}
