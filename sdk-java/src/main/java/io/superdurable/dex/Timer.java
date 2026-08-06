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

public final class Timer {
    private Timer() {
    }

    public static Condition byDuration(final Duration duration) {
        return Condition.timer(duration);
    }

    public static Condition byDuration(final Duration duration, final String conditionId) {
        return Condition.timer(duration, conditionId);
    }
}
