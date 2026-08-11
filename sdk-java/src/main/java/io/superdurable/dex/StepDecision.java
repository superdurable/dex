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
 * return StepDecision.goTo(chargeOrder, order);
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
    private final Object output;
    private final String reason;
    private final List<Object> emptyChannels;
    private final StepMovement<?> fallback;

    private StepDecision(
            final Kind kind,
            final List<StepMovement<?>> movements,
            final Object output,
            final String reason,
            final List<Object> emptyChannels,
            final StepMovement<?> fallback) {
        this.kind = kind;
        this.movements = Collections.unmodifiableList(movements);
        this.output = output;
        this.reason = reason;
        this.emptyChannels = Collections.unmodifiableList(emptyChannels);
        this.fallback = fallback;
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
                null,
                null,
                Collections.<Object>emptyList(),
                null);
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
        return gracefulComplete(null);
    }

    /**
     * Requests successful Flow completion after all execution branches stop and records an output.
     *
     * <p>This decision has the branch-draining behavior described by {@link #gracefulComplete()}.
     * The output is serialized as this branch's completion output. When the Flow completes,
     * {@link Client#waitForFlow(String, Class)} returns the latest recorded Step output; an output
     * recorded later by another parallel branch takes precedence.
     *
     * @param output the serializable output for this execution branch, or {@code null}
     * @return a graceful-completion decision
     */
    public static StepDecision gracefulComplete(final Object output) {
        return close(Kind.GRACEFUL_COMPLETE, output, null);
    }

    /**
     * Immediately completes the Flow with an output value.
     *
     * @param output the serializable Flow output, or {@code null}
     * @return a force-completion decision
     */
    public static StepDecision forceComplete(final Object output) {
        return close(Kind.FORCE_COMPLETE, output, null);
    }

    /**
     * Immediately completes the Flow without an output value.
     *
     * @return a force-completion decision
     */
    public static StepDecision forceComplete() {
        return forceComplete(null);
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
                output,
                null,
                Arrays.<Object>asList(channels.clone()),
                fallback);
    }

    /**
     * Fails the Flow with an application-provided reason.
     *
     * @param reason the user-visible failure reason
     * @return a force-failure decision
     */
    public static StepDecision forceFail(final String reason) {
        return close(Kind.FORCE_FAIL, null, reason);
    }

    /**
     * Leaves this execution with no next Step and without completing the Flow.
     *
     * @return a dead-end decision
     */
    public static StepDecision deadEnd() {
        return close(Kind.DEAD_END, null, null);
    }

    private static StepDecision close(final Kind kind, final Object output, final String reason) {
        return new StepDecision(
                kind,
                Collections.<StepMovement<?>>emptyList(),
                output,
                reason,
                Collections.<Object>emptyList(),
                null);
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

    String getReason() {
        return reason;
    }

    List<Object> getEmptyChannels() {
        return emptyChannels;
    }

    StepMovement<?> getFallback() {
        return fallback;
    }
}
