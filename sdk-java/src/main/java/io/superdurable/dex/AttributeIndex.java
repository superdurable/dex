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

/**
 * Describes how an Attribute is indexed for Flow search.
 *
 * <p>Attach an index when defining an {@link Attribute} or {@link AttributeMap}. The optional
 * index key lets multiple logical Attributes share one physical Dex index when their types match.
 * A {@code null} index key uses the Attribute name.
 *
 * <p>Use explicit generic numbered keys for application indexes: keyword1, keywordList1,
 * text1, int1, double1, bool1, and datetime1, then subsequent numbers. Share a typed slot pool
 * across Flow types. Two distinct indexed fields in one Flow need different slots. Reuse across
 * Flow types requires the same index type; their business meanings may differ.
 *
 * <p>With server-zd's PostgreSQL search backend: the server enforces index capacity and Keyword
 * length limits. PostgreSQL search provides 10 KEYWORD slots and three each for INT, DATETIME,
 * DOUBLE, BOOL, KEYWORD_ARRAY, and FULL_TEXT per tenant. System indexes do not consume these
 * slots. Index registration beyond capacity fails with FAILED_PRECONDITION and can prevent Worker
 * startup.
 * Registrations cannot be deleted; plan the shared pool before registering application indexes.
 * PostgreSQL limits a KEYWORD value or each KEYWORD_ARRAY element to 1024 UTF-8 bytes.
 * An oversized new write fails with INVALID_ARGUMENT; a rejected Step result fails the Step.
 * Previously committed oversized indexed documents can be omitted from search while their
 * Flow state remains committed. Other search backends can have different limits.
 *
 * <pre>{@code
 * Attribute<String> customer = Attribute.define(
 *         "customer",
 *         String.class,
 *         new AttributeIndex(AttributeIndex.Type.KEYWORD, "keyword1"));
 * Attribute<String> region = Attribute.define(
 *         "region",
 *         String.class,
 *         new AttributeIndex(AttributeIndex.Type.KEYWORD, "keyword2"));
 * }</pre>
 */
public final class AttributeIndex {
    /** Defines the value representation used by the search backend. */
    public enum Type {
        /** Indexes one exact string value for equality and aggregation queries. */
        KEYWORD,

        /** Indexes analyzed text for full-text search queries. */
        FULL_TEXT,

        /** Indexes an array of exact string values. */
        KEYWORD_ARRAY,

        /** Indexes a signed integer value. */
        INT,

        /** Indexes a floating-point value. */
        DOUBLE,

        /** Indexes a boolean value. */
        BOOL,

        /** Indexes a date-and-time value. */
        DATETIME
    }

    private final Type type;
    private final String indexKey;

    /**
     * Creates an index using the Attribute name as its search key.
     *
     * @param type the index representation; must not be {@code null}
     * @throws IllegalArgumentException if {@code type} is {@code null}
     */
    public AttributeIndex(final Type type) {
        this(type, null);
    }

    /**
     * Creates an index with an explicit physical index key.
     *
     * @param type the index representation; must not be {@code null}
     * @param indexKey the physical index key, or {@code null} to use the Attribute name
     * @throws IllegalArgumentException if {@code type} is {@code null}
     */
    public AttributeIndex(final Type type, final String indexKey) {
        if (type == null) {
            throw new IllegalArgumentException("index type is required");
        }
        this.type = type;
        this.indexKey = indexKey;
    }

    Type getType() {
        return type;
    }

    String getIndexKey() {
        return indexKey;
    }
}
