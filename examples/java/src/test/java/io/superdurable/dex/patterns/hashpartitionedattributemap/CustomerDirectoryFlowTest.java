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

package io.superdurable.dex.patterns.hashpartitionedattributemap;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import org.junit.jupiter.api.Test;

class CustomerDirectoryFlowTest {
    @Test
    void usesSharedEmailPartitionGoldenVectors() {
        assertPartition(" Alice@Example.COM ", "alice@example.com", 2493822278L, "partition-278");
        assertPartition("bob@example.com", "bob@example.com", 3055529145L, "partition-145");
        assertPartition(
                "support+west@example.org",
                "support+west@example.org",
                2156001632L,
                "partition-632");
    }

    @Test
    void rejectsInvalidEmailAddresses() {
        assertThrows(
                IllegalArgumentException.class,
                () -> CustomerDirectoryFlow.canonicalEmailAddress(""));
        assertThrows(
                IllegalArgumentException.class,
                () -> CustomerDirectoryFlow.canonicalEmailAddress(" \t\r\n"));
        assertThrows(
                IllegalArgumentException.class,
                () -> CustomerDirectoryFlow.canonicalEmailAddress("josé@example.com"));
    }

    private static void assertPartition(
            final String input,
            final String canonicalEmail,
            final long hash,
            final String partitionName) {
        final CustomerDirectoryFlow.EmailPartition actual =
                CustomerDirectoryFlow.emailPartition(input);
        assertEquals(canonicalEmail, actual.canonicalEmail);
        assertEquals(hash, actual.hash);
        assertEquals(partitionName, actual.partitionName);
    }
}
