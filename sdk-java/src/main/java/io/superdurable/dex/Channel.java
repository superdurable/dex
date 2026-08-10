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
 * Defines one durable, typed message Channel for a Flow.
 *
 * <p>Register the Channel in the Flow's {@link PersistenceSchema}. Clients, Steps, and RPCs may
 * publish messages, while Step wait methods create conditions describing how many messages must be
 * available. Condition results are consumed from the current invocation context.
 *
 * <pre>{@code
 * private final Channel<String> approvals = Channel.define("approvals", String.class);
 *
 * public Wait waitFor(Context context, Order input) {
 *     return Wait.until(approvals.forOne("approval"));
 * }
 *
 * public StepDecision execute(Context context, Order input) {
 *     String approver = approvals.getConditionResults(context).get(0);
 *     return StepDecision.gracefulComplete(approver);
 * }
 * }</pre>
 *
 * @param <T> the Java message type carried by the Channel
 */
public final class Channel<T> extends PersistenceDefinition {
    private final String name;
    private final Class<T> valueType;

    private Channel(final String name, final Class<T> valueType) {
        this.name = Attribute.requireName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
    }

    /**
     * Defines a typed Channel.
     *
     * @param name the stable Channel name; must not be blank
     * @param valueType the concrete Java class used to serialize messages
     * @param <T> the Java message type
     * @return the typed Channel definition
     * @throws IllegalArgumentException if {@code name} is {@code null} or blank
     * @throws NullPointerException if {@code valueType} is {@code null}
     */
    public static <T> Channel<T> define(final String name, final Class<T> valueType) {
        return new Channel<T>(name, valueType);
    }

    String getName() {
        return name;
    }

    Class<T> getValueType() {
        return valueType;
    }

    /**
     * Publishes one message from the current invocation.
     *
     * @param context the Step or RPC invocation context
     * @param value the typed message to publish
     */
    public void publish(final Context context, final T value) {
        context.publish(this, value);
    }

    /**
     * Returns the number of messages currently visible to this invocation.
     *
     * @param context the Step or RPC invocation context
     * @return the visible message count
     */
    public int size(final Context context) {
        return context.channelSize(this);
    }

    /**
     * Returns messages consumed by this Channel's satisfied condition.
     *
     * @param context the Step invocation context
     * @return the typed condition results in server-provided order
     */
    public List<T> getConditionResults(final Context context) {
        return context.channelResults(this);
    }

    /**
     * Creates a condition requiring exactly one message.
     *
     * @return the Channel condition
     */
    public Condition forOne() {
        return forOne(null);
    }

    /**
     * Creates a named condition requiring exactly one message.
     *
     * @param conditionId the condition ID used to identify its result
     * @return the Channel condition
     */
    public Condition forOne(final String conditionId) {
        return range(1, 1, conditionId);
    }

    /**
     * Creates a condition requiring exactly {@code count} messages.
     *
     * @param count the required message count
     * @return the Channel condition
     */
    public Condition forN(final int count) {
        return forN(count, null);
    }

    /**
     * Creates a named condition requiring exactly {@code count} messages.
     *
     * @param count the required message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition forN(final int count, final String conditionId) {
        return range(count, count, conditionId);
    }

    /**
     * Creates a condition requiring at least {@code count} messages.
     *
     * @param count the minimum message count
     * @return the Channel condition
     */
    public Condition atLeast(final int count) {
        return atLeast(count, null);
    }

    /**
     * Creates a named condition requiring at least {@code count} messages.
     *
     * @param count the minimum message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition atLeast(final int count, final String conditionId) {
        return range(count, null, conditionId);
    }

    /**
     * Creates a condition accepting at most {@code count} messages.
     *
     * @param count the maximum message count
     * @return the Channel condition
     */
    public Condition atMost(final int count) {
        return atMost(count, null);
    }

    /**
     * Creates a named condition accepting at most {@code count} messages.
     *
     * @param count the maximum message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition atMost(final int count, final String conditionId) {
        return range(null, count, conditionId);
    }

    /**
     * Creates a condition with optional minimum and maximum message counts.
     *
     * @param atLeast the minimum count, or {@code null} for no minimum
     * @param atMost the maximum count, or {@code null} for no maximum
     * @return the Channel condition
     * @throws IllegalArgumentException if both bounds are {@code null}
     */
    public Condition range(final Integer atLeast, final Integer atMost) {
        return range(atLeast, atMost, null);
    }

    /**
     * Creates a named condition with optional minimum and maximum message counts.
     *
     * @param atLeast the minimum count, or {@code null} for no minimum
     * @param atMost the maximum count, or {@code null} for no maximum
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     * @throws IllegalArgumentException if both bounds are {@code null}
     */
    public Condition range(final Integer atLeast, final Integer atMost, final String conditionId) {
        return Condition.channel(name, null, atLeast, atMost, conditionId);
    }
}
