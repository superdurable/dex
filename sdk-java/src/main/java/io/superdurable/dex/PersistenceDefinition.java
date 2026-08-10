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
 * Represents a typed Attribute or Channel definition in a {@link PersistenceSchema}.
 *
 * <p>This is the common, type-erased base accepted by schema factory methods. Applications create
 * concrete definitions with {@link Attribute#define}, {@link AttributeMap#define},
 * {@link Channel#define}, or {@link ChannelMap#define}; custom subclasses are intentionally not
 * supported.
 */
public abstract class PersistenceDefinition {
    PersistenceDefinition() {
    }

    abstract String getName();

    abstract Class<?> getValueType();
}
