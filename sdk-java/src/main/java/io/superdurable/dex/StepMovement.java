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

public final class StepMovement<I> {
    private final Step<I> step;
    private final I input;
    private final StepOptions options;

    private StepMovement(final Step<I> step, final I input, final StepOptions options) {
        this.step = step;
        this.input = input;
        this.options = options;
    }

    public static <I> StepMovement<I> of(final Step<I> step, final I input) {
        return new StepMovement<I>(step, input, null);
    }

    public static <I> StepMovement<I> of(
            final Step<I> step,
            final I input,
            final StepOptions options) {
        return new StepMovement<I>(step, input, options);
    }

    Step<I> getStep() {
        return step;
    }

    I getInput() {
        return input;
    }

    StepOptions getOptions() {
        return options;
    }
}
