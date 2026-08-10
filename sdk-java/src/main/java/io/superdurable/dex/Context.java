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

import java.time.Instant;
import java.util.List;

/**
 * Exposes invocation metadata and durable state operations to Step and RPC code.
 *
 * <p>A context belongs to one worker invocation and must not be retained or used from another
 * thread after the method returns. Attribute and Channel helpers are also available directly on
 * their typed definitions; the context methods are useful for framework-style code. Values passed
 * with a {@link Class} must use concrete, non-parameterized types. Wrap a parameterized value in a
 * concrete holder class, or use an array such as {@code String[]} instead of {@code List<String>}.
 *
 * <pre>{@code
 * public StepDecision execute(Context context, Order input) {
 *     int attempt = context.getAttempt();
 *     status.set(context, "processed-on-attempt-" + attempt);
 *     context.recordEvent("order-processed", input, Order.class);
 *     return StepDecision.gracefulComplete();
 * }
 * }</pre>
 */
public interface Context {
    /**
     * Returns the Flow ID for the current invocation.
     *
     * @return the Flow ID
     */
    String getFlowId();

    /**
     * Returns the current Flow run ID.
     *
     * @return the run ID
     */
    String getRunId();

    /**
     * Returns when the current Flow execution started.
     *
     * @return the Flow start timestamp
     */
    Instant getFlowStartedAt();

    /**
     * Returns the server-assigned identifier of the current Step execution.
     *
     * @return the Step execution ID
     */
    String getStepExecutionId();

    /**
     * Returns the Step execution that scheduled the current Step.
     *
     * @return the originating Step execution ID, or an empty value when none exists
     */
    String getFromStepExecutionId();

    /**
     * Returns when the first attempt of the current method began.
     *
     * @return the first-attempt timestamp
     */
    Instant getFirstAttemptAt();

    /**
     * Returns the one-based attempt number of the current method invocation.
     *
     * @return the current attempt number
     */
    int getAttempt();

    /**
     * Reports whether any timer condition in the current wait has fired.
     *
     * @return {@code true} when at least one timer fired
     */
    boolean hasTimerFired();

    /**
     * Reports whether the timer at a specific condition index fired.
     *
     * @param index the zero-based timer condition index
     * @return {@code true} when that timer fired
     */
    boolean hasTimerFired(int index);

    /**
     * Reports whether execute is running after a failed wait-for method.
     *
     * <p>This can be {@code true} only when the Step uses
     * {@link WaitForFailurePolicy#PROCEED}.
     *
     * @return {@code true} when wait-for retries were exhausted before execute
     */
    boolean waitForMethodFailed();

    /**
     * Stores a value scoped to the current Step execution.
     *
     * <p>Step-execution locals allow {@link Step#waitFor} to pass durable data to
     * {@link Step#execute} for the same execution.
     *
     * @param key the local value key
     * @param value the value to serialize
     * @param valueType the concrete Java value class
     * @param <T> the local value type
     */
    <T> void setStepExecutionLocal(String key, T value, Class<T> valueType);

    /**
     * Reads a value scoped to the current Step execution.
     *
     * @param key the local value key
     * @param valueType the concrete Java class used to decode the value
     * @param <T> the local value type
     * @return the decoded value, or {@code null} when the key is absent
     */
    <T> T getStepExecutionLocal(String key, Class<T> valueType);

    /**
     * Appends a named user event to the Flow execution history.
     *
     * @param name the event name
     * @param value the event payload
     * @param valueType the concrete Java payload class
     * @param <T> the event payload type
     */
    <T> void recordEvent(String name, T value, Class<T> valueType);

    /**
     * Reads an Attribute.
     *
     * @param attribute the registered typed Attribute
     * @param <T> the Attribute value type
     * @return the current value, or {@code null} when absent
     */
    <T> T getAttribute(Attribute<T> attribute);

    /**
     * Reads one Attribute-map instance.
     *
     * @param attribute the registered typed Attribute map
     * @param instance the map instance
     * @param <T> the Attribute value type
     * @return the current value, or {@code null} when absent
     */
    <T> T getAttribute(AttributeMap<T> attribute, String instance);

    /**
     * Writes an Attribute.
     *
     * @param attribute the registered typed Attribute
     * @param value the value to persist
     * @param <T> the Attribute value type
     */
    <T> void setAttribute(Attribute<T> attribute, T value);

    /**
     * Writes one Attribute-map instance.
     *
     * @param attribute the registered typed Attribute map
     * @param instance the map instance
     * @param value the value to persist
     * @param <T> the Attribute value type
     */
    <T> void setAttribute(AttributeMap<T> attribute, String instance, T value);

    /**
     * Deletes an Attribute.
     *
     * @param attribute the registered Attribute
     */
    void deleteAttribute(Attribute<?> attribute);

    /**
     * Deletes one Attribute-map instance.
     *
     * @param attribute the registered Attribute map
     * @param instance the map instance
     */
    void deleteAttribute(AttributeMap<?> attribute, String instance);

    /**
     * Publishes one message to a Channel.
     *
     * @param channel the registered typed Channel
     * @param value the message value
     * @param <T> the Channel message type
     */
    <T> void publish(Channel<T> channel, T value);

    /**
     * Publishes one message to a Channel-map instance.
     *
     * @param channel the registered typed Channel map
     * @param instance the map instance
     * @param value the message value
     * @param <T> the Channel message type
     */
    <T> void publish(ChannelMap<T> channel, String instance, T value);

    /**
     * Returns the visible message count for a Channel.
     *
     * @param channel the registered Channel
     * @return the visible message count
     */
    int channelSize(Channel<?> channel);

    /**
     * Returns the visible message count for one Channel-map instance.
     *
     * @param channel the registered Channel map
     * @param instance the map instance
     * @return the visible message count
     */
    int channelSize(ChannelMap<?> channel, String instance);

    /**
     * Returns condition results for a Channel.
     *
     * @param channel the registered typed Channel
     * @param <T> the Channel message type
     * @return the typed messages consumed by the satisfied condition
     */
    <T> List<T> channelResults(Channel<T> channel);

    /**
     * Returns condition results for one Channel-map instance.
     *
     * @param channel the registered typed Channel map
     * @param instance the map instance used by the condition
     * @param <T> the Channel message type
     * @return the typed messages consumed by the satisfied condition
     */
    <T> List<T> channelResults(ChannelMap<T> channel, String instance);
}
