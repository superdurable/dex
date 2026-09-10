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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Contains one newest-first page returned by {@link Client#listStreamMessages}.
 *
 * <p>Pass a nonempty next-page token unchanged to the next call. The returned message list is
 * immutable. Stream trimming may remove messages between pages.
 *
 * @param <T> the decoded Stream message type
 */
public final class StreamMessagesPage<T> {
    private final List<StreamMessage<T>> messages;
    private final String nextPageToken;

    StreamMessagesPage(
            final List<StreamMessage<T>> messages,
            final String nextPageToken) {
        this.messages = Collections.unmodifiableList(
                new ArrayList<StreamMessage<T>>(messages));
        this.nextPageToken = nextPageToken;
    }

    /**
     * Returns retained messages in newest-first order.
     *
     * @return an immutable message list
     */
    public List<StreamMessage<T>> getMessages() {
        return messages;
    }

    /**
     * Returns the token for fetching the next older page.
     *
     * @return the opaque token, or an empty string on the final page
     */
    public String getNextPageToken() {
        return nextPageToken;
    }
}
