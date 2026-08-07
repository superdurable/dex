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

public final class AttributeLock {
    private final String attribute;
    private final String instance;

    private AttributeLock(final String attribute, final String instance) {
        this.attribute = attribute;
        this.instance = instance;
    }

    public static AttributeLock of(final Attribute<?> attribute) {
        return new AttributeLock(attribute.getName(), null);
    }

    public static AttributeLock of(final AttributeMap<?> attribute, final String instance) {
        return new AttributeLock(attribute.getName(), Attribute.requireName(instance));
    }

    String getAttribute() {
        return attribute;
    }

    String getInstance() {
        return instance;
    }
}
