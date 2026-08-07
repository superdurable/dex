/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
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
