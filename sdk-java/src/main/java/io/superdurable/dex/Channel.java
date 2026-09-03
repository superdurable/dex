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
        this.name = Attribute.requirePersistenceDefinitionName(name);
        this.valueType = Objects.requireNonNull(valueType, "valueType");
    }

    /**
     * Defines a typed Channel.
     *
     * @param name the stable Channel name; must not be blank or contain {@code /}
     * @param valueType the concrete Java class used to serialize messages
     * @param <T> the Java message type
     * @return the typed Channel definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
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
     * Stages deletion of one pending message from an RPC handler.
     *
     * @param context the RPC invocation context
     * @param messageId the nonblank server-assigned message ID
     */
    public void delete(final Context context, final String messageId) {
        context.deleteChannelMessage(this, messageId);
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
     * Returns this Channel's loaded pending-message snapshot in FIFO order.
     *
     * <p>Add this Channel name to {@link RPC#loadChannels()}. The snapshot does not change after
     * staged publications or deletions in the same handler.
     *
     * @param context the RPC invocation context
     * @return immutable pending message IDs and decoded values
     * @throws io.superdurable.dex.exceptions.ChannelMessagesNotLoadedException if the RPC did
     *     not load its messages
     */
    public List<ChannelMessage<T>> pendingMessages(final Context context) {
        return context.pendingChannelMessages(this);
    }

    /**
     * Finds one pending message in the loaded RPC snapshot.
     *
     * @param context the RPC invocation context
     * @param messageId the server-assigned message ID
     * @return the matching message, or {@code null} when it is absent from the snapshot
     * @throws io.superdurable.dex.exceptions.ChannelMessagesNotLoadedException if the RPC did
     *     not load its messages
     */
    public ChannelMessage<T> findPendingMessage(
            final Context context,
            final String messageId) {
        final String requiredId = Attribute.requireName(messageId);
        for (ChannelMessage<T> message : pendingMessages(context)) {
            if (message.getMessageId().equals(requiredId)) {
                return message;
            }
        }
        return null;
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
     * Creates a non-blocking condition consuming up to {@code count} queued messages. When its
     * surrounding Wait completes, it consumes messages queued then. An empty queue yields none.
     *
     * @param count the maximum message count
     * @return the Channel condition
     */
    public Condition atMost(final int count) {
        return atMost(count, null);
    }

    /**
     * Creates a named, non-blocking condition consuming up to {@code count} queued messages. When
     * its surrounding Wait completes, it consumes messages queued then. An empty queue yields none.
     *
     * @param count the maximum message count
     * @param conditionId the condition ID used to identify its results
     * @return the Channel condition
     */
    public Condition atMost(final int count, final String conditionId) {
        return range(null, count, conditionId);
    }

    /**
     * Creates a condition with optional minimum and maximum message counts. Dex waits only for the
     * minimum, then consumes currently queued messages up to the maximum. A {@code null} minimum
     * makes the condition complete immediately.
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
     * Creates a named condition with optional minimum and maximum message counts. Dex waits only
     * for the minimum, then consumes currently queued messages up to the maximum. A {@code null}
     * minimum makes the condition complete immediately.
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
