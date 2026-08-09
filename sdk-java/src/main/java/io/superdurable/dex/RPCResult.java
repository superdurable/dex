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

/**
 * Carries a typed RPC output and optional next-Step movements.
 *
 * <p>Return this value from a function-style {@link RPC} method. The output is encoded according to
 * the RPC method's declared generic return type, while movements let an RPC start or advance Flow
 * execution atomically with its state changes.
 *
 * <pre>{@code
 * @RPC
 * public RPCResult<OrderStatus> approve(Context context, Approval input) {
 *     return RPCResult.of(OrderStatus.APPROVED, StepMovement.of(shipOrder, input.order));
 * }
 * }</pre>
 *
 * @param <O> the concrete Java RPC output type
 */
public final class RPCResult<O> {
    private final O output;
    private final List<StepMovement<?>> nextSteps;

    private RPCResult(final O output, final List<StepMovement<?>> nextSteps) {
        this.output = output;
        this.nextSteps = Collections.unmodifiableList(nextSteps);
    }

    /**
     * Creates an RPC result without next Steps.
     *
     * @param output the typed RPC output, which may be {@code null}
     * @param <O> the output type
     * @return the RPC result
     */
    public static <O> RPCResult<O> of(final O output) {
        return new RPCResult<O>(output, Collections.<StepMovement<?>>emptyList());
    }

    /**
     * Creates an RPC result that also schedules next Steps.
     *
     * @param output the typed RPC output, which may be {@code null}
     * @param nextSteps the movements to schedule
     * @param <O> the output type
     * @return the RPC result
     */
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
