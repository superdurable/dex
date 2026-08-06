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

import java.util.Arrays;
import java.util.Collections;
import java.util.List;

public final class StepDecision {
    enum Kind {
        NEXT,
        GRACEFUL_COMPLETE,
        FORCE_COMPLETE,
        FORCE_COMPLETE_IF_CHANNELS_EMPTY,
        FORCE_FAIL,
        DEAD_END
    }

    private final Kind kind;
    private final List<StepMovement<?>> movements;
    private final Object output;
    private final String reason;
    private final List<Object> emptyChannels;
    private final StepMovement<?> fallback;

    private StepDecision(
            final Kind kind,
            final List<StepMovement<?>> movements,
            final Object output,
            final String reason,
            final List<Object> emptyChannels,
            final StepMovement<?> fallback) {
        this.kind = kind;
        this.movements = Collections.unmodifiableList(movements);
        this.output = output;
        this.reason = reason;
        this.emptyChannels = Collections.unmodifiableList(emptyChannels);
        this.fallback = fallback;
    }

    public static <I> StepDecision goTo(final Step<I> step, final I input) {
        return goToMulti(StepMovement.of(step, input));
    }

    public static StepDecision goToMulti(final StepMovement<?>... movements) {
        return new StepDecision(
                Kind.NEXT,
                Arrays.<StepMovement<?>>asList(movements.clone()),
                null,
                null,
                Collections.<Object>emptyList(),
                null);
    }

    public static StepDecision gracefulComplete() {
        return gracefulComplete(null);
    }

    public static StepDecision gracefulComplete(final Object output) {
        return close(Kind.GRACEFUL_COMPLETE, output, null);
    }

    public static StepDecision forceComplete(final Object output) {
        return close(Kind.FORCE_COMPLETE, output, null);
    }

    public static StepDecision forceComplete() {
        return forceComplete(null);
    }

    public static StepDecision forceCompleteWhenChannelsEmpty(
            final Object output,
            final StepMovement<?> fallback,
            final Object... channels) {
        return new StepDecision(
                Kind.FORCE_COMPLETE_IF_CHANNELS_EMPTY,
                Collections.<StepMovement<?>>emptyList(),
                output,
                null,
                Arrays.<Object>asList(channels.clone()),
                fallback);
    }

    public static StepDecision forceFail(final String reason) {
        return close(Kind.FORCE_FAIL, null, reason);
    }

    public static StepDecision deadEnd() {
        return close(Kind.DEAD_END, null, null);
    }

    private static StepDecision close(final Kind kind, final Object output, final String reason) {
        return new StepDecision(
                kind,
                Collections.<StepMovement<?>>emptyList(),
                output,
                reason,
                Collections.<Object>emptyList(),
                null);
    }

    Kind getKind() {
        return kind;
    }

    List<StepMovement<?>> getMovements() {
        return movements;
    }

    Object getOutput() {
        return output;
    }

    String getReason() {
        return reason;
    }

    List<Object> getEmptyChannels() {
        return emptyChannels;
    }

    StepMovement<?> getFallback() {
        return fallback;
    }
}
