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

public final class AttributeIndex {
    public enum Type {
        KEYWORD,
        FULL_TEXT,
        KEYWORD_ARRAY,
        INT,
        DOUBLE,
        BOOL,
        DATETIME
    }

    private final Type type;
    private final String indexKey;

    public AttributeIndex(final Type type) {
        this(type, null);
    }

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
