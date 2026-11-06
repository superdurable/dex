/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import java.time.Duration;

/**
 * Creates server-side durable timer conditions for Step waits.
 *
 * <p>This is not a JVM timer and does not require the Worker process to remain running. Dex stores
 * the timer as part of the durable Flow execution on the server, so the deadline remains scheduled
 * across Worker disconnections, process restarts, and machine failures. No Java thread is blocked
 * while the timer is pending. When the server-side timer fires and the surrounding {@link Wait} is
 * satisfied, Dex invokes the Step through an available Worker. Add a stable condition ID when
 * application code needs to inspect or skip a specific timer later.
 *
 * <p>The delay starts when Dex applies the successful WaitFor result, not when this factory
 * is called. This SDK sends whole seconds and discards fractional seconds. A zero duration is
 * immediately eligible. Inspect the winning timer with {@link Context#hasTimerFired(int)} or
 * make it eligible early with {@link Client#skipTimer}. Handler scheduling can occur later when
 * a Worker becomes available; the timer does not promise an exact Java invocation time.
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
     * Creates a server-side durable timer condition without a condition ID.
     *
     * @param duration the nonnegative delay before the timer fires
     * @return the timer condition
     * @throws IllegalArgumentException if {@code duration} is {@code null} or negative
     */
    public static Condition byDuration(final Duration duration) {
        return Condition.timer(duration);
    }

    /**
     * Creates a server-side durable timer condition with a stable condition ID.
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
