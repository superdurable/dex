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

import java.util.Objects;

public final class Attribute<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;
    private final AttributeIndex index;

    private Attribute(final String name, final Class<T> valueType, final AttributeIndex index) {
        this.name = requireName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
        this.index = index;
    }

    public static <T> Attribute<T> define(final String name, final Class<T> valueType) {
        return new Attribute<T>(name, valueType, null);
    }

    public static <T> Attribute<T> define(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index) {
        return new Attribute<T>(name, valueType, index);
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    AttributeIndex getIndex() {
        return index;
    }

    public T get(final Context context) {
        return context.getAttribute(this);
    }

    public void set(final Context context, final T value) {
        context.setAttribute(this, value);
    }

    public void delete(final Context context) {
        context.deleteAttribute(this);
    }

    static String requireName(final String name) {
        if (name == null || name.trim().isEmpty()) {
            throw new IllegalArgumentException("durable name is required");
        }
        return name;
    }
}
