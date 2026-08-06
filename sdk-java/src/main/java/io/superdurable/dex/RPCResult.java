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

public final class RPCResult<O> {
    private final O output;
    private final List<StepMovement<?>> nextSteps;

    private RPCResult(final O output, final List<StepMovement<?>> nextSteps) {
        this.output = output;
        this.nextSteps = Collections.unmodifiableList(nextSteps);
    }

    public static <O> RPCResult<O> of(final O output) {
        return new RPCResult<O>(output, Collections.<StepMovement<?>>emptyList());
    }

    public static <O> RPCResult<O> of(
            final O output,
            final StepMovement<?>... nextSteps) {
        return new RPCResult<O>(
                output,
                Arrays.<StepMovement<?>>asList(nextSteps.clone()));
    }

    O getOutput() {
        return output;
    }

    List<StepMovement<?>> getNextSteps() {
        return nextSteps;
    }
}
