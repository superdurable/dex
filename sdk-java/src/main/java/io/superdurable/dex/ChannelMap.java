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
 * same instance key when publishing, waiting, and reading condition results. Instance keys must
 * be nonblank and must not contain {@code /}.
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
     * Stages deletion of one pending message from a Channel-map instance.
     *
     * <p>Step and timeout-handler deletions are best-effort when the message is already absent.
     *
     * @param context the handler invocation context
     * @param instance the nonblank Channel-map instance
     * @param messageId the nonblank server-assigned message ID
     */
    public void delete(final Context context, final String instance, final String messageId) {
        context.deleteChannelMessage(this, instance, messageId);
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
     * Returns one instance's loaded pending-message snapshot in FIFO order.
     *
     * <p>Select the instance in the handler's options. The snapshot does not change after staged
     * publications or deletions in the same handler.
     *
     * @param context the handler invocation context
     * @param instance the logical ChannelMap instance
     * @return immutable pending message IDs and decoded values
     * @throws io.superdurable.dex.exceptions.ChannelMessagesNotLoadedException if the invocation
     *     did not load its messages
     */
    public List<ChannelMessage<T>> pendingMessages(
            final Context context,
            final String instance) {
        return context.pendingChannelMessages(this, instance);
    }

    /**
     * Finds one instance message in the loaded handler snapshot.
     *
     * @param context the handler invocation context
     * @param instance the logical ChannelMap instance
     * @param messageId the server-assigned message ID
     * @return the matching message, or {@code null} when absent from the snapshot
     * @throws io.superdurable.dex.exceptions.ChannelMessagesNotLoadedException if the invocation
     *     did not load its messages
     */
    public ChannelMessage<T> findPendingMessage(
            final Context context,
            final String instance,
            final String messageId) {
        final String requiredId = Attribute.requireName(messageId);
        for (ChannelMessage<T> message : pendingMessages(context, instance)) {
            if (message.getMessageId().equals(requiredId)) {
                return message;
            }
        }
        return null;
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
     * Creates a non-blocking condition consuming up to {@code count} queued messages from an
     * instance. When its surrounding Wait completes, it consumes messages queued then. An empty
     * queue yields none.
     *
     * @param instance the Channel-map instance
     * @param count the maximum message count
     * @return the Channel condition
     */
    public Condition atMost(final String instance, final int count) {
        return atMost(instance, count, null);
    }

    /**
     * Creates a named, non-blocking condition consuming up to {@code count} queued messages from an
     * instance. When its surrounding Wait completes, it consumes messages queued then. An empty
     * queue yields none.
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
     * Creates a condition with optional message-count bounds for one instance. Dex waits only for
     * the minimum, then consumes currently queued messages up to the maximum. A {@code null}
     * minimum makes the condition complete immediately.
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
     * Creates a named condition with optional message-count bounds for one instance. Dex waits only
     * for the minimum, then consumes currently queued messages up to the maximum. A {@code null}
     * minimum makes the condition complete immediately.
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
