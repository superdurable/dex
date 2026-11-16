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

/**
 * Creates the wait condition that is satisfied once the Flow has a graceful completion request.
 *
 * <p>A graceful completion request comes from {@link Client#stopFlow} with
 * {@link StopType#GRACEFUL_COMPLETE}, or from a Step that returns
 * {@link StepDecision#gracefulComplete()}. The request stays in force, so a {@link Wait}
 * registered after it is satisfied at once. When the request arrives, Dex re-checks only the
 * Waits that include this condition. Other waiting Steps keep waiting and keep the Flow running.
 *
 * <p>Use it to let a long-waiting Step end when someone asks the Flow to complete. A typical Step
 * waits for the next message or this condition with {@link Wait#anyOf}. Its execute method first
 * handles a message that the Wait consumed. When there is none and
 * {@link Context#isGracefulCompletionConditionCompleted()} returns {@code true}, it returns
 * {@link StepDecision#deadEnd()}. The Flow then completes once no other Step is active.
 *
 * <p>In {@link Wait#anyOf}, this condition comes after Timers, Channels, and SubFlows, so a ready
 * message still wins over the request. The condition still reports completed when a message won
 * and a request exists. Check the message first, or it is lost. The condition also works in
 * {@link Wait#allOf} and in {@link Wait#anyCombinationOf}, where it needs a condition ID. A Wait
 * may hold at most one graceful completion condition. A second one fails the Step's waitFor with
 * {@link io.superdurable.dex.exceptions.InvalidStepResultException}.
 *
 * <p>The condition requires Server protocol version 7 or later, which server-zd 0.1.34 and later
 * releases provide. With an older Server, the Step's waitFor fails with
 * {@link io.superdurable.dex.exceptions.GracefulCompletionConditionUnsupportedException}, and Dex
 * retries it according to its retry policy.
 *
 * <pre>{@code
 * static final Channel<String> MESSAGES = Channel.define("messages", String.class);
 *
 * final class WaitForMessage implements Step<Void> {
 *     public Class<Void> getInputType() {
 *         return Void.class;
 *     }
 *
 *     public Wait waitFor(Context context, Void input) {
 *         return Wait.anyOf(MESSAGES.forOne(), GracefulCompletion.requested());
 *     }
 *
 *     public StepDecision execute(Context context, Void input) {
 *         List<String> messages = MESSAGES.getConditionResults(context);
 *         if (!messages.isEmpty()) {
 *             return StepDecision.goTo(HandleMessage.class, messages.get(0));
 *         }
 *         if (context.isGracefulCompletionConditionCompleted()) {
 *             return StepDecision.deadEnd();
 *         }
 *         return StepDecision.goTo(WaitForMessage.class, null);
 *     }
 * }
 * }</pre>
 *
 * @since 1.45.3
 */
public final class GracefulCompletion {
    private GracefulCompletion() {
    }

    /**
     * Creates a graceful completion condition without a condition ID.
     *
     * <p>Use it in {@link Wait#anyOf}, {@link Wait#allOf}, or {@link Wait#until}. Read its outcome
     * with {@link Context#isGracefulCompletionConditionCompleted()}. See {@link GracefulCompletion}
     * for an example.
     *
     * @return the graceful completion condition
     * @since 1.45.3
     */
    public static Condition requested() {
        return Condition.gracefulCompletion(null);
    }

    /**
     * Creates a graceful completion condition with a stable condition ID.
     *
     * <p>{@link Wait#anyCombinationOf} requires the ID. The ID must be unique among the conditions
     * of the same Wait. See {@link GracefulCompletion} for an example.
     *
     * @param conditionId the ID used to identify this condition and its result
     * @return the graceful completion condition
     * @since 1.45.3
     */
    public static Condition requested(final String conditionId) {
        return Condition.gracefulCompletion(conditionId);
    }
}
