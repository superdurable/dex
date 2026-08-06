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

import java.util.Objects;

public final class StepDef {
    private final Step<?> step;
    private final boolean startStep;

    private StepDef(final Step<?> step, final boolean startStep) {
        this.step = Objects.requireNonNull(step, "step");
        this.startStep = startStep;
    }

    public static <I> StepDef startStep(final Step<I> step) {
        return new StepDef(step, true);
    }

    public static <I> StepDef nonStartStep(final Step<I> step) {
        return new StepDef(step, false);
    }

    Step<?> getStep() {
        return step;
    }

    boolean isStartStep() {
        return startStep;
    }
}
