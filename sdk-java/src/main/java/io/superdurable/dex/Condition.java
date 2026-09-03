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

import java.time.Duration;

/**
 * Represents one Timer, Channel, or SubFlow condition used by a {@link Wait}.
 *
 * <p>Applications create conditions through fluent domain factories such as
 * {@link Timer#byDuration}, {@link Channel#forOne}, and {@link SubFlow#run}; a condition is immutable
 * and is interpreted by Dex when a Step returns its wait definition.
 *
 * <pre>{@code
 * Condition deadline = Timer.byDuration(Duration.ofMinutes(5), "deadline");
 * Condition command = commands.forOne("command");
 * return Wait.anyOf(deadline, command);
 * }</pre>
 */
public final class Condition {
    enum Kind {
        TIMER,
        CHANNEL,
        SUB_FLOW
    }

    private final Kind kind;
    private final String conditionId;
    private final String channelName;
    private final String instance;
    private final Integer atLeast;
    private final Integer atMost;
    private final Duration duration;
    private final Class<? extends Flow<?>> subFlowClass;
    private final Object subFlowInput;
    private final SubFlowOptions subFlowOptions;

    private Condition(
            final Kind kind,
            final String conditionId,
            final String channelName,
            final String instance,
            final Integer atLeast,
            final Integer atMost,
            final Duration duration,
            final Class<? extends Flow<?>> subFlowClass,
            final Object subFlowInput,
            final SubFlowOptions subFlowOptions) {
        this.kind = kind;
        this.conditionId = conditionId;
        this.channelName = channelName;
        this.instance = instance;
        this.atLeast = atLeast;
        this.atMost = atMost;
        this.duration = duration;
        this.subFlowClass = subFlowClass;
        this.subFlowInput = subFlowInput;
        this.subFlowOptions = subFlowOptions;
    }

    static Condition timer(final Duration duration) {
        return timer(duration, null);
    }

    static Condition timer(final Duration duration, final String conditionId) {
        if (duration == null || duration.isNegative()) {
            throw new IllegalArgumentException("non-negative duration is required");
        }
        return new Condition(
                Kind.TIMER, conditionId, null, null, null, null, duration, null, null, null);
    }

    static Condition channel(
            final String channelName,
            final String instance,
            final Integer atLeast,
            final Integer atMost,
            final String conditionId) {
        if (atLeast == null && atMost == null) {
            throw new IllegalArgumentException("channel condition requires a bound");
        }
        final String validatedInstance = instance == null
                ? null
                : Attribute.requireMapInstance(instance);
        return new Condition(
                Kind.CHANNEL,
                conditionId,
                channelName,
                validatedInstance,
                atLeast,
                atMost,
                null,
                null,
                null,
                null);
    }

    static Condition subFlow(
            final Class<? extends Flow<?>> flowClass,
            final Object input,
            final SubFlowOptions options) {
        if (flowClass == null) {
            throw new IllegalArgumentException("SubFlow class is required");
        }
        final SubFlowOptions effective = options == null ? SubFlowOptions.newBuilder().build() : options;
        return new Condition(
                Kind.SUB_FLOW,
                effective.getConditionId(),
                null,
                null,
                null,
                null,
                null,
                flowClass,
                input,
                effective);
    }

    Kind getKind() {
        return kind;
    }

    String getConditionId() {
        return conditionId;
    }

    String getChannelName() {
        return channelName;
    }

    String getInstance() {
        return instance;
    }

    Integer getAtLeast() {
        return atLeast;
    }

    Integer getAtMost() {
        return atMost;
    }

    Duration getDuration() {
        return duration;
    }

    Class<? extends Flow<?>> getSubFlowClass() {
        return subFlowClass;
    }

    Object getSubFlowInput() {
        return subFlowInput;
    }

    SubFlowOptions getSubFlowOptions() {
        return subFlowOptions;
    }
}
