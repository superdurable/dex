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

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/**
 * Carries a typed RPC output, optional next-Step movements, and cancellation selectors.
 *
 * <p>Return this value from a function-style {@link RPC} method. The output is encoded according to
 * the RPC method's declared generic return type, while movements let an RPC start or advance Flow
 * execution atomically with its state changes.
 *
 * <pre>{@code
 * @RPC
 * public RPCResult<OrderStatus> approve(Context context, Approval input) {
 *     return RPCResult.of(OrderStatus.APPROVED, StepMovement.of(shipOrder, input.order))
 *             .withCancelingSiblingSteps(quoteCarrier)
 *             .withCancelingSteps(approvalTimeout);
 * }
 * }</pre>
 *
 * @param <O> the concrete Java RPC output type
 */
public final class RPCResult<O> {
    private final O output;
    private final List<StepMovement<?>> nextSteps;
    private final List<Step<?>> cancelingSteps;
    private final List<Step<?>> cancelingSiblingSteps;

    private RPCResult(
            final O output,
            final List<StepMovement<?>> nextSteps,
            final List<Step<?>> cancelingSteps,
            final List<Step<?>> cancelingSiblingSteps) {
        this.output = output;
        this.nextSteps = Collections.unmodifiableList(nextSteps);
        this.cancelingSteps = CancellationSteps.immutable(cancelingSteps);
        this.cancelingSiblingSteps = CancellationSteps.immutable(cancelingSiblingSteps);
    }

    /**
     * Creates an RPC result without next Steps.
     *
     * @param output the typed RPC output, which may be {@code null}
     * @param <O> the output type
     * @return the RPC result
     */
    public static <O> RPCResult<O> of(final O output) {
        return new RPCResult<O>(
                output,
                Collections.<StepMovement<?>>emptyList(),
                Collections.<Step<?>>emptyList(),
                Collections.<Step<?>>emptyList());
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
                Arrays.<StepMovement<?>>asList(nextSteps.clone()),
                Collections.<Step<?>>emptyList(),
                Collections.<Step<?>>emptyList());
    }

    /**
     * Cancels queued or active executions of the selected Step types in the current Flow.
     *
     * <p>Dex resolves the selection after this RPC's Attribute and Channel writes commit and before
     * this result's next-Step movements are enqueued. Existing executions that already finished,
     * were canceled, or do not exist are ignored. Cancellation is cooperative, and Dex continues
     * with this result's next Steps without waiting for selected handlers to return.
     *
     * <p>Repeated calls take the union of their arguments. A Flow-wide selection supersedes a
     * sibling-only selection for the same registered Step. Each argument must be the exact Step
     * instance registered with the current Flow; a {@code null} or external Step causes an invalid
     * RPC result.
     *
     * @param steps registered Steps whose queued or active executions should be canceled
     * @return a new RPC result containing the combined cancellation selection
     */
    public RPCResult<O> withCancelingSteps(final Step<?>... steps) {
        final List<Step<?>> global = CancellationSteps.add(cancelingSteps, steps);
        final List<Step<?>> siblings = new ArrayList<Step<?>>(cancelingSiblingSteps);
        CancellationSteps.remove(siblings, global);
        return copy(global, siblings);
    }

    /**
     * Cancels selected executions previously scheduled by the same RPC name.
     *
     * <p>RPC-scheduled Steps use {@code __rpc/&lt;rpcName&gt;} as their scheduling source. This method
     * selects only executions of the requested Step types whose source belongs to the RPC returning
     * this result. Dex applies the snapshot, no-op, validation, and cooperative behavior described
     * by {@link #withCancelingSteps(Step[])}.
     *
     * <p>Repeated calls take the union of their arguments, while a Flow-wide selection for a Step
     * type supersedes its sibling-only selection.
     *
     * @param steps registered Steps whose same-RPC executions should be canceled
     * @return a new RPC result containing the combined sibling cancellation selection
     */
    public RPCResult<O> withCancelingSiblingSteps(final Step<?>... steps) {
        final List<Step<?>> siblings = CancellationSteps.add(cancelingSiblingSteps, steps);
        CancellationSteps.remove(siblings, cancelingSteps);
        return copy(cancelingSteps, siblings);
    }

    private RPCResult<O> copy(
            final List<Step<?>> global,
            final List<Step<?>> siblings) {
        return new RPCResult<O>(output, nextSteps, global, siblings);
    }

    O getOutput() {
        return output;
    }

    List<StepMovement<?>> getNextSteps() {
        return nextSteps;
    }

    List<Step<?>> getCancelingSteps() {
        return cancelingSteps;
    }

    List<Step<?>> getCancelingSiblingSteps() {
        return cancelingSiblingSteps;
    }
}
