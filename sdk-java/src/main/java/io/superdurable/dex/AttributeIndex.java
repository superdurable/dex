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
 * Describes how an Attribute is indexed for Flow search.
 *
 * <p>Attach an index when defining an {@link Attribute} or {@link AttributeMap}. The optional
 * index key lets multiple logical Attributes share a server-side search field when the server
 * configuration permits it. A {@code null} index key uses the Attribute name.
 *
 * <pre>{@code
 * Attribute<String> customer = Attribute.define(
 *         "customer",
 *         String.class,
 *         new AttributeIndex(AttributeIndex.Type.KEYWORD));
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
     * @param type the search representation; must not be {@code null}
     * @throws IllegalArgumentException if {@code type} is {@code null}
     */
    public AttributeIndex(final Type type) {
        this(type, null);
    }

    /**
     * Creates an index with an explicit server-side search key.
     *
     * @param type the search representation; must not be {@code null}
     * @param indexKey the server-side search key, or {@code null} to use the Attribute name
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
