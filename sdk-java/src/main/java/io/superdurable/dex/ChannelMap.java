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

/**
 * Defines a durable map of named, typed Channel instances.
 *
 * <p>Use a Channel map when the instance key is selected at runtime, such as one Channel per order
 * or tenant. Register the definition once in the Flow's {@link PersistenceSchema}, then pass the
 * same instance key when publishing, waiting, and reading condition results.
 *
 * <pre>{@code
 * private final ChannelMap<String> commands =
 *         ChannelMap.define("commands", String.class);
 *
 * return Wait.until(commands.forOne(orderId, "next-command"));
 * }</pre>
 *
 * @param <T> the Java message type carried by every Channel instance
 */
public final class ChannelMap<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;

    private ChannelMap(final String name, final Class<T> valueType) {
        this.name = Attribute.requirePersistenceDefinitionName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
    }

    /**
     * Defines a typed Channel map.
     *
     * @param name the stable Channel-map name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used to serialize messages
     * @param <T> the Java message type
     * @return the typed Channel-map definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> ChannelMap<T> define(final String name, final Class<T> valueType) {
        return new ChannelMap<T>(name, valueType);
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    /**
     * Publishes one message to a map instance from the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param instance the Channel-map instance
     * @param value the typed message to publish
     */
    public void publish(final Context context, final String instance, final T value) {
        context.publish(this, instance, value);
    }

    /**
     * Returns the number of messages visible in one map instance.
     *
     * @param context the Step or RPC invocation context
     * @param instance the Channel-map instance
     * @return the visible message count
     */
    public int size(final Context context, final String instance) {
        return context.channelSize(this, instance);
    }

    /**
     * Returns the number of non-empty instances visible to the current RPC.
     *
     * @param context the RPC invocation context
     * @return the current number of non-empty instances
     */
    public int getMapSize(final Context context) {
        return getAllInstanceKeys(context).size();
    }

    /**
     * Returns non-empty instance keys in ascending order for the current RPC.
     *
     * @param context the RPC invocation context
     * @return an immutable sorted list of decoded instance keys
     */
    public List<String> getAllInstanceKeys(final Context context) {
        if (!(context instanceof InvocationContext)) {
            throw new IllegalArgumentException("Dex invocation Context is required");
        }
        return ((InvocationContext) context).channelMapKeys(this);
    }

    /**
     * Returns messages consumed by a satisfied condition for one map instance.
     *
     * @param context the Step invocation context
     * @param instance the Channel-map instance used by the condition
     * @return the typed condition results in server-provided order
     */
    public List<T> getConditionResults(final Context context, final String instance) {
        return context.channelResults(this, instance);
    }

    /**
     * Creates a condition requiring exactly one message from an instance.
     *
     * @param instance the Channel-map instance
     * @return the Channel condition
     */
    public Condition forOne(final String instance) {
        return forOne(instance, null);
    }

    /**
     * Creates a named condition requiring exactly one message from an instance.
     *
     * @param instance the Channel-map instance
     * @param conditionId the condition ID used to identify its result
     * @return the Channel condition
     */
    public Condition forOne(final String instance, final String conditionId) {
        return range(instance, 1, 1, conditionId);
    }

    /**
     * Creates a condition requiring exactly {@code count} messages from an instance.
     *
     * @param instance the Channel-map instance
     * @param count the required message count
     * @return the Channel condition
     */
    public Condition forN(final String instance, final int count) {
        return forN(instance, count, null);
    }

    /**
     * Creates a named condition requiring exactly {@code count} messages from an instance.
     *
     * @param instance the Channel-map instance
     * @param count the required message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition forN(
            final String instance,
            final int count,
            final String conditionId) {
        return range(instance, count, count, conditionId);
    }

    /**
     * Creates a condition requiring at least {@code count} messages from an instance.
     *
     * @param instance the Channel-map instance
     * @param count the minimum message count
     * @return the Channel condition
     */
    public Condition atLeast(final String instance, final int count) {
        return atLeast(instance, count, null);
    }

    /**
     * Creates a named condition requiring at least {@code count} messages from an instance.
     *
     * @param instance the Channel-map instance
     * @param count the minimum message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition atLeast(
            final String instance,
            final int count,
            final String conditionId) {
        return range(instance, count, null, conditionId);
    }

    /**
     * Creates a condition accepting at most {@code count} messages from an instance.
     *
     * @param instance the Channel-map instance
     * @param count the maximum message count
     * @return the Channel condition
     */
    public Condition atMost(final String instance, final int count) {
        return atMost(instance, count, null);
    }

    /**
     * Creates a named condition accepting at most {@code count} messages from an instance.
     *
     * @param instance the Channel-map instance
     * @param count the maximum message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition atMost(
            final String instance,
            final int count,
            final String conditionId) {
        return range(instance, null, count, conditionId);
    }

    /**
     * Creates a condition with optional message-count bounds for one instance.
     *
     * @param instance the Channel-map instance
     * @param atLeast the minimum count, or {@code null} for no minimum
     * @param atMost the maximum count, or {@code null} for no maximum
     * @return the Channel condition
     * @throws IllegalArgumentException if both bounds are {@code null}
     */
    public Condition range(
            final String instance,
            final Integer atLeast,
            final Integer atMost) {
        return range(instance, atLeast, atMost, null);
    }

    /**
     * Creates a named condition with optional message-count bounds for one instance.
     *
     * @param instance the Channel-map instance
     * @param atLeast the minimum count, or {@code null} for no minimum
     * @param atMost the maximum count, or {@code null} for no maximum
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     * @throws IllegalArgumentException if both bounds are {@code null}
     */
    public Condition range(
            final String instance,
            final Integer atLeast,
            final Integer atMost,
            final String conditionId) {
        return Condition.channel(name, instance, atLeast, atMost, conditionId);
    }
}
