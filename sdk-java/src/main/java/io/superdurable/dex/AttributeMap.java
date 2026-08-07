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

public final class AttributeMap<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;
    private final AttributeIndex index;

    private AttributeMap(final String name, final Class<T> valueType, final AttributeIndex index) {
        this.name = Attribute.requireName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
        this.index = index;
    }

    public static <T> AttributeMap<T> define(final String name, final Class<T> valueType) {
        return new AttributeMap<T>(name, valueType, null);
    }

    public static <T> AttributeMap<T> define(
            final String name,
            final Class<T> valueType,
            final AttributeIndex index) {
        return new AttributeMap<T>(name, valueType, index);
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

    public T get(final Context context, final String instance) {
        return context.getAttribute(this, instance);
    }

    public void set(final Context context, final String instance, final T value) {
        context.setAttribute(this, instance, value);
    }

    public void delete(final Context context, final String instance) {
        context.deleteAttribute(this, instance);
    }
}
