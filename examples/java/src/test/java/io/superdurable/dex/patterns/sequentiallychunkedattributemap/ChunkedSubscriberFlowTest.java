/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.patterns.sequentiallychunkedattributemap;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import org.junit.jupiter.api.Test;

class ChunkedSubscriberFlowTest {
    @Test
    void validatesSubscriberPageTokens() {
        assertEquals(
                ChunkedSubscriberFlow.CURRENT_CHUNK_INSTANCE,
                ChunkedSubscriberFlow.validateSubscriberPageToken(""));
        assertEquals(
                "00000000000000000101",
                ChunkedSubscriberFlow.validateSubscriberPageToken("00000000000000000101"));
        assertThrows(
                IllegalArgumentException.class,
                () -> ChunkedSubscriberFlow.validateSubscriberPageToken("1"));
        assertThrows(
                IllegalArgumentException.class,
                () -> ChunkedSubscriberFlow.validateSubscriberPageToken("archive"));
        assertThrows(
                IllegalArgumentException.class,
                () -> ChunkedSubscriberFlow.validateSubscriberPageToken("00000000000000000002"));
    }
}
