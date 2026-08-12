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
 * Describes the durable action Dex takes after a Step executes.
 *
 * <p>Every {@link Step#execute} call returns one decision. A decision may schedule next Steps,
 * complete or fail the Flow, leave the execution idle, or complete only after specified Channels
 * become empty. Output values are serialized by the worker and are available to waiting clients.
 *
 * <pre>{@code
 * if (order.cancelled) {
 *     return StepDecision.forceComplete("cancelled");
 * }
 * return StepDecision.goTo(chargeOrder, order)
 *         .withCancelingSiblingSteps(reserveInventory, quoteShipping);
 * }</pre>
 */
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
    private final boolean hasOutput;
    private final Object output;
    private final String reason;
    private final List<Object> emptyChannels;
    private final StepMovement<?> fallback;
    private final List<Step<?>> cancelingSteps;
    private final List<Step<?>> cancelingSiblingSteps;

    private StepDecision(
            final Kind kind,
            final List<StepMovement<?>> movements,
            final boolean hasOutput,
            final Object output,
            final String reason,
            final List<Object> emptyChannels,
            final StepMovement<?> fallback,
            final List<Step<?>> cancelingSteps,
            final List<Step<?>> cancelingSiblingSteps) {
        this.kind = kind;
        this.movements = Collections.unmodifiableList(movements);
        this.hasOutput = hasOutput;
        this.output = output;
        this.reason = reason;
        this.emptyChannels = Collections.unmodifiableList(emptyChannels);
        this.fallback = fallback;
        this.cancelingSteps = CancellationSteps.immutable(cancelingSteps);
        this.cancelingSiblingSteps = CancellationSteps.immutable(cancelingSiblingSteps);
    }

    /**
     * Schedules one typed next Step.
     *
     * @param step the target Step
     * @param input the typed target input
     * @param <I> the target Step input type
     * @return a next-Step decision
     */
    public static <I> StepDecision goTo(final Step<I> step, final I input) {
        return goToMulti(StepMovement.of(step, input));
    }

    /**
     * Schedules zero or more next-Step movements together.
     *
     * @param movements the movements to schedule
     * @return a multi-movement decision
     */
    public static StepDecision goToMulti(final StepMovement<?>... movements) {
        return new StepDecision(
                Kind.NEXT,
                Arrays.<StepMovement<?>>asList(movements.clone()),
                false,
                null,
                null,
                Collections.<Object>emptyList(),
                null,
                Collections.<Step<?>>emptyList(),
                Collections.<Step<?>>emptyList());
    }

    /**
     * Requests successful Flow completion after all execution branches stop, without an output.
     *
     * <p>This decision stops the current branch without scheduling another Step, but it does not
     * interrupt Steps that are already running in parallel. Those branches may continue and
     * schedule their next Steps. Dex changes the Flow status to {@link FlowStatus#COMPLETED} only
     * after no active or queued Steps remain. Use {@link #forceComplete()} to stop every branch
     * immediately, or {@link #deadEnd()} to stop only the current branch without requesting Flow
     * completion.
     *
     * @return a graceful-completion decision
     */
    public static StepDecision gracefulComplete() {
        return close(Kind.GRACEFUL_COMPLETE, false, null, null);
    }

    /**
     * Requests successful Flow completion after all execution branches stop and records an output.
     *
     * <p>This decision has the branch-draining behavior described by {@link #gracefulComplete()}.
     * The output is serialized as this branch's completion output. When the Flow completes,
     * {@link Client#waitForFlow(String)} returns every recorded Step output with its Step identity.
     * Parallel completion order is not a business ordering contract.
     *
     * @param output the serializable output for this execution branch, or {@code null}
     * @return a graceful-completion decision
     */
    public static StepDecision gracefulComplete(final Object output) {
        return close(Kind.GRACEFUL_COMPLETE, true, output, null);
    }

    /**
     * Immediately completes the Flow with an output value.
     *
     * @param output the serializable Flow output, or {@code null}
     * @return a force-completion decision
     */
    public static StepDecision forceComplete(final Object output) {
        return close(Kind.FORCE_COMPLETE, true, output, null);
    }

    /**
     * Immediately completes the Flow without an output value.
     *
     * @return a force-completion decision
     */
    public static StepDecision forceComplete() {
        return close(Kind.FORCE_COMPLETE, false, null, null);
    }

    /**
     * Completes when all selected Channels are empty, otherwise schedules a fallback movement.
     *
     * @param output the serializable Flow output used when Channels are empty
     * @param fallback the movement scheduled when at least one Channel is not empty
     * @param channels the {@link Channel} or {@link ChannelMap} definitions that must be empty
     * @return a conditional force-completion decision
     */
    public static StepDecision forceCompleteIfChannelsEmpty(
            final Object output,
            final StepMovement<?> fallback,
            final Object... channels) {
        return new StepDecision(
                Kind.FORCE_COMPLETE_IF_CHANNELS_EMPTY,
                Collections.<StepMovement<?>>emptyList(),
                true,
                output,
                null,
                Arrays.<Object>asList(channels.clone()),
                fallback,
                Collections.<Step<?>>emptyList(),
                Collections.<Step<?>>emptyList());
    }

    /**
     * Fails the Flow with an application-provided reason.
     *
     * @param reason the user-visible failure reason
     * @return a force-failure decision
     */
    public static StepDecision forceFail(final String reason) {
        return close(Kind.FORCE_FAIL, false, null, reason);
    }

    /**
     * Leaves this execution with no next Step and without completing the Flow.
     *
     * @return a dead-end decision
     */
    public static StepDecision deadEnd() {
        return close(Kind.DEAD_END, false, null, null);
    }

    private static StepDecision close(
            final Kind kind,
            final boolean hasOutput,
            final Object output,
            final String reason) {
        return new StepDecision(
                kind,
                Collections.<StepMovement<?>>emptyList(),
                hasOutput,
                output,
                reason,
                Collections.<Object>emptyList(),
                null,
                Collections.<Step<?>>emptyList(),
                Collections.<Step<?>>emptyList());
    }

    /**
     * Cancels queued or active executions of the selected Step types in the current Flow.
     *
     * <p>Dex resolves the selection as a snapshot after the current execution succeeds. Existing
     * executions that already finished, were canceled, or do not exist are ignored. Newly scheduled
     * movements in this decision are not part of the snapshot. Cancellation is cooperative and Dex
     * continues with this decision without waiting for selected handlers to return.
     *
     * <p>Repeated calls take the union of their arguments. A Flow-wide selection supersedes a
     * sibling-only selection for the same registered Step. Each argument must be the exact Step
     * instance registered with the current Flow; a {@code null} or external Step causes an invalid
     * Step result.
     *
     * @param steps registered Steps whose queued or active executions should be canceled
     * @return a new decision containing the combined cancellation selection
     */
    public StepDecision withCancelingSteps(final Step<?>... steps) {
        final List<Step<?>> global = CancellationSteps.add(cancelingSteps, steps);
        final List<Step<?>> siblings = new ArrayList<Step<?>>(cancelingSiblingSteps);
        CancellationSteps.remove(siblings, global);
        return copy(global, siblings);
    }

    /**
     * Cancels selected sibling Step executions that share the current execution's scheduling source.
     *
     * <p>A sibling has the same {@link Context#getFromStepExecutionId()} as the execution returning
     * this decision. Dex applies the same snapshot, no-op, and cooperative cancellation semantics as
     * {@link #withCancelingSteps(Step[])}. Repeated calls take the union of their arguments, while a
     * Flow-wide selection for a Step type supersedes its sibling-only selection.
     *
     * @param steps registered Steps whose matching sibling executions should be canceled
     * @return a new decision containing the combined sibling cancellation selection
     */
    public StepDecision withCancelingSiblingSteps(final Step<?>... steps) {
        final List<Step<?>> siblings = CancellationSteps.add(cancelingSiblingSteps, steps);
        CancellationSteps.remove(siblings, cancelingSteps);
        return copy(cancelingSteps, siblings);
    }

    private StepDecision copy(
            final List<Step<?>> global,
            final List<Step<?>> siblings) {
        return new StepDecision(
                kind,
                movements,
                hasOutput,
                output,
                reason,
                emptyChannels,
                fallback,
                global,
                siblings);
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

    boolean hasOutput() {
        return hasOutput;
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

    List<Step<?>> getCancelingSteps() {
        return cancelingSteps;
    }

    List<Step<?>> getCancelingSiblingSteps() {
        return cancelingSiblingSteps;
    }
}
