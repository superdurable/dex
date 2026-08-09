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
 * Creates timer conditions for Step waits.
 *
 * <p>Timers use {@link Duration} and do not block a Java thread. Dex durably tracks the deadline and
 * invokes the Step after the surrounding {@link Wait} is satisfied. Add a stable condition ID when
 * application code needs to inspect or skip a specific timer later.
 *
 * <pre>{@code
 * public Wait waitFor(Context context, Order input) {
 *     return Wait.until(Timer.byDuration(Duration.ofMinutes(10), "payment-timeout"));
 * }
 * }</pre>
 */
public final class Timer {
    private Timer() {
    }

    /**
     * Creates a timer condition without a condition ID.
     *
     * @param duration the nonnegative delay before the timer fires
     * @return the timer condition
     * @throws IllegalArgumentException if {@code duration} is {@code null} or negative
     */
    public static Condition byDuration(final Duration duration) {
        return Condition.timer(duration);
    }

    /**
     * Creates a timer condition with a stable condition ID.
     *
     * @param duration the nonnegative delay before the timer fires
     * @param conditionId the ID used to inspect or skip this timer
     * @return the timer condition
     * @throws IllegalArgumentException if {@code duration} is {@code null} or negative
     */
    public static Condition byDuration(final Duration duration, final String conditionId) {
        return Condition.timer(duration, conditionId);
    }
}
