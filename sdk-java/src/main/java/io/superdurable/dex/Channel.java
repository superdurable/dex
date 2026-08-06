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

public final class Channel<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;

    private Channel(final String name, final Class<T> valueType) {
        this.name = Attribute.requireName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
    }

    public static <T> Channel<T> define(final String name, final Class<T> valueType) {
        return new Channel<T>(name, valueType);
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    public void publish(final Context context, final T value) {
        context.publish(this, value);
    }

    public int size(final Context context) {
        return context.channelSize(this);
    }

    public List<T> getConditionResults(final Context context) {
        return context.channelResults(this);
    }

    public Condition forOne() {
        return forOne(null);
    }

    public Condition forOne(final String conditionId) {
        return range(1, 1, conditionId);
    }

    public Condition forN(final int count) {
        return forN(count, null);
    }

    public Condition forN(final int count, final String conditionId) {
        return range(count, count, conditionId);
    }

    public Condition atLeast(final int count) {
        return atLeast(count, null);
    }

    public Condition atLeast(final int count, final String conditionId) {
        return range(count, null, conditionId);
    }

    public Condition atMost(final int count) {
        return atMost(count, null);
    }

    public Condition atMost(final int count, final String conditionId) {
        return range(null, count, conditionId);
    }

    public Condition range(final Integer atLeast, final Integer atMost) {
        return range(atLeast, atMost, null);
    }

    public Condition range(final Integer atLeast, final Integer atMost, final String conditionId) {
        return Condition.channel(name, null, atLeast, atMost, conditionId);
    }
}
