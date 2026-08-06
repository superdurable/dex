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

import java.util.List;
import java.util.Objects;

public final class ChannelMap<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;

    private ChannelMap(final String name, final Class<T> valueType) {
        this.name = Attribute.requireName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
    }

    public static <T> ChannelMap<T> define(final String name, final Class<T> valueType) {
        return new ChannelMap<T>(name, valueType);
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    public void publish(final Context context, final String instance, final T value) {
        context.publish(this, instance, value);
    }

    public int size(final Context context, final String instance) {
        return context.channelSize(this, instance);
    }

    public List<T> getConditionResults(final Context context, final String instance) {
        return context.channelResults(this, instance);
    }

    public Condition forOne(final String instance) {
        return forOne(instance, null);
    }

    public Condition forOne(final String instance, final String conditionId) {
        return range(instance, 1, 1, conditionId);
    }

    public Condition forN(final String instance, final int count) {
        return forN(instance, count, null);
    }

    public Condition forN(
            final String instance,
            final int count,
            final String conditionId) {
        return range(instance, count, count, conditionId);
    }

    public Condition atLeast(final String instance, final int count) {
        return atLeast(instance, count, null);
    }

    public Condition atLeast(
            final String instance,
            final int count,
            final String conditionId) {
        return range(instance, count, null, conditionId);
    }

    public Condition atMost(final String instance, final int count) {
        return atMost(instance, count, null);
    }

    public Condition atMost(
            final String instance,
            final int count,
            final String conditionId) {
        return range(instance, null, count, conditionId);
    }

    public Condition range(
            final String instance,
            final Integer atLeast,
            final Integer atMost) {
        return range(instance, atLeast, atMost, null);
    }

    public Condition range(
            final String instance,
            final Integer atLeast,
            final Integer atMost,
            final String conditionId) {
        return Condition.channel(name, instance, atLeast, atMost, conditionId);
    }
}
