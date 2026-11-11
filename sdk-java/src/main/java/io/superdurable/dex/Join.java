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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;
import java.util.function.Function;

/**
 * Synchronizes parallel branches of one Flow: a split Step starts the branches and one join Step
 * waits for their results.
 *
 * <p>Use a Join whenever a Step must continue only after all, the first, or a number of parallel
 * branches finish. Declare it like a Channel and register it in the Flow's
 * {@link PersistenceSchema}. A Join is built on a {@link ChannelMap} with the same name: each
 * round of the Join uses one ChannelMap instance, and each branch publishes its result there.
 * Dex Web and pending-message reads show the Join as that ChannelMap. Its name must not equal the
 * name of another Attribute, Channel, ChannelMap, or Stream of the Flow.
 *
 * <p>A round has four parts:
 *
 * <ol>
 *   <li><b>Split.</b> The split Step's Execute calls {@link #fork}. It creates one
 *       {@link JoinRound} whose key is the split Step's execution ID and whose branch count is the
 *       number of branch builders. It calls each builder and the join builder with that round and
 *       returns the movements. Return them with {@link StepDecision#goToMany(List)}.</li>
 *   <li><b>Branch.</b> Each branch reports exactly one result per round. Its last Step returns
 *       {@link #endBranch}, or calls {@link #reportBranchResult} when it must continue.</li>
 *   <li><b>Join.</b> The join Step's WaitFor waits with {@link #forAllBranches},
 *       {@link #forFirstBranch}, or {@link #forBranches}. Its Execute reads the consumed results
 *       with {@link #getBranchResults}.</li>
 *   <li><b>Reset.</b> When the join consumed fewer results than branches and the Flow loops, the
 *       next round's split Step waits with {@link #forRemainingBranches} before it forks again,
 *       and may read the late results with {@link #getRemainingBranchResults}. This blocks the next
 *       round until every branch of the previous round has reported.</li>
 * </ol>
 *
 * <p>Example: request three quotes per round, accept the first two, and repeat for three rounds.
 * The example compiles as written.
 *
 * <pre>{@code
 * import io.superdurable.dex.Channel;
 * import io.superdurable.dex.Context;
 * import io.superdurable.dex.Flow;
 * import io.superdurable.dex.Join;
 * import io.superdurable.dex.JoinRound;
 * import io.superdurable.dex.PersistenceSchema;
 * import io.superdurable.dex.Step;
 * import io.superdurable.dex.StepDecision;
 * import io.superdurable.dex.StepList;
 * import io.superdurable.dex.StepMovement;
 * import io.superdurable.dex.Wait;
 * import java.util.ArrayList;
 * import java.util.Arrays;
 * import java.util.List;
 * import java.util.function.Function;
 *
 * public final class QuoteFlow implements Flow<QuoteFlow.QuoteRequest> {
 *     public static final class QuoteRequest {
 *         public int requiredQuotes = 2;
 *         public int roundNumber = 1;
 *         public int roundCount = 3;
 *         public JoinRound previousRound; // null in the first round
 *     }
 *
 *     public static final class SupplierInput {
 *         public String supplier;
 *         public JoinRound round;
 *     }
 *
 *     public static final class AcceptInput {
 *         public QuoteRequest request;
 *         public JoinRound round;
 *     }
 *
 *     private final Join<String> quoteReceived = Join.define("quoteReceived", String.class);
 *     private final Channel<Void> supplierReleased = Channel.define("supplierReleased", Void.class);
 *     private final RequestQuotes requestQuotes = new RequestQuotes();
 *     private final QuoteFromSupplier quoteFromSupplier = new QuoteFromSupplier();
 *     private final AcceptQuotes acceptQuotes = new AcceptQuotes();
 *
 *     public StepList<QuoteRequest> getSteps() {
 *         return StepList.startStep(requestQuotes).otherSteps(quoteFromSupplier, acceptQuotes);
 *     }
 *
 *     public PersistenceSchema getPersistenceSchema() {
 *         return PersistenceSchema.of(quoteReceived, supplierReleased);
 *     }
 *
 *     // Split: reset the previous round, then fork three branches and one join.
 *     final class RequestQuotes implements Step<QuoteRequest> {
 *         public Class<QuoteRequest> getInputType() {
 *             return QuoteRequest.class;
 *         }
 *
 *         public Wait waitFor(Context context, QuoteRequest request) {
 *             if (request.previousRound == null) {
 *                 return Wait.skipImmediately();
 *             }
 *             return Wait.until(quoteReceived.forRemainingBranches(
 *                     request.previousRound, request.requiredQuotes));
 *         }
 *
 *         public StepDecision execute(Context context, QuoteRequest request) {
 *             List<Function<JoinRound, StepMovement<?>>> branches = new ArrayList<>();
 *             for (String supplier : Arrays.asList("A", "B", "C")) {
 *                 branches.add(round -> {
 *                     SupplierInput branchInput = new SupplierInput();
 *                     branchInput.supplier = supplier;
 *                     branchInput.round = round;
 *                     return StepMovement.of(QuoteFromSupplier.class, branchInput);
 *                 });
 *             }
 *             List<StepMovement<?>> movements = quoteReceived.fork(context, branches, round -> {
 *                 AcceptInput joinInput = new AcceptInput();
 *                 joinInput.request = request;
 *                 joinInput.round = round;
 *                 return StepMovement.of(AcceptQuotes.class, joinInput);
 *             });
 *             return StepDecision.goToMany(movements);
 *         }
 *     }
 *
 *     // Branch: report exactly one result, then end.
 *     final class QuoteFromSupplier implements Step<SupplierInput> {
 *         public Class<SupplierInput> getInputType() {
 *             return SupplierInput.class;
 *         }
 *
 *         public Wait waitFor(Context context, SupplierInput input) {
 *             return Wait.until(supplierReleased.forOne());
 *         }
 *
 *         public StepDecision execute(Context context, SupplierInput input) {
 *             return quoteReceived.endBranch(context, input.round, input.supplier);
 *         }
 *     }
 *
 *     // Join: accept the first requiredQuotes results, then start the next round.
 *     final class AcceptQuotes implements Step<AcceptInput> {
 *         public Class<AcceptInput> getInputType() {
 *             return AcceptInput.class;
 *         }
 *
 *         public Wait waitFor(Context context, AcceptInput input) {
 *             return Wait.until(quoteReceived.forBranches(
 *                     input.round, input.request.requiredQuotes));
 *         }
 *
 *         public StepDecision execute(Context context, AcceptInput input) {
 *             List<String> accepted = quoteReceived.getBranchResults(context, input.round);
 *             QuoteRequest request = input.request;
 *             if (request.roundNumber == request.roundCount) {
 *                 return StepDecision.forceComplete(accepted);
 *             }
 *             QuoteRequest next = new QuoteRequest();
 *             next.requiredQuotes = request.requiredQuotes;
 *             next.roundNumber = request.roundNumber + 1;
 *             next.roundCount = request.roundCount;
 *             next.previousRound = input.round;
 *             return StepDecision.goTo(RequestQuotes.class, next);
 *         }
 *     }
 * }
 * }</pre>
 *
 * <p>For a run-time number of branches, map the input list to one builder per element; the branch
 * count is the list length. A join with a deadline combines a Join condition with a Timer, such as
 * {@code Wait.anyOf(Timer.byDuration(timeout, "deadline"), join.forAllBranches(round, "all"))}.
 *
 * <p>Rules and traps:
 *
 * <ul>
 *   <li>Each branch reports exactly one result per round. A result is committed together with the
 *       Step method that reports it, so a retried method never reports twice, and a failed attempt
 *       reports nothing. A branch that reports twice is counted twice. A branch that never reports,
 *       for example because its failure route goes to a recovery Step that does not report, blocks
 *       an all-branches join forever. Report a failure result from the recovery Step, or give the
 *       join a Timer deadline.</li>
 *   <li>A branch passes its round only to {@link #endBranch} or {@link #reportBranchResult}. It
 *       never waits on a join condition. A multi-Step branch passes the round down to its last
 *       Step.</li>
 *   <li>Exactly one join Step waits on a round. Two Steps waiting on the same round split its
 *       results between them.</li>
 *   <li>Fork a Join at most once per Step invocation. A second {@link #fork} of the same Join in
 *       the same invocation throws. Use another Join, or {@link #forkWithRoundKey} with distinct
 *       keys, for two independent joins.</li>
 *   <li>An RPC has no Step execution ID, so it cannot call {@link #fork}. Use
 *       {@link #forkWithRoundKey} with an application-chosen key instead.</li>
 *   <li>The {@code receivedCount} passed to {@link #forRemainingBranches} must equal the number
 *       of results the join consumed: the {@code count} of {@link #forBranches}, or 1 for
 *       {@link #forFirstBranch}.</li>
 *   <li>Without a reset, late results stay in the round's ChannelMap instance until the Flow
 *       closes. They do not delay graceful completion. A Flow that loops must reset every round,
 *       except the round of a cancelling join.</li>
 *   <li>To cancel the remaining branches when the join fires, return the join's decision with
 *       {@link StepDecision#withCancelingSiblingSteps} and the branch Step classes. Fork puts the
 *       branches and the join in one decision, so the selection covers exactly this round's
 *       branches. Later Steps of a multi-Step branch are not siblings; select them with
 *       {@link StepDecision#withCancelingSteps}, which is correct only when one round is active at
 *       a time. Rounds forked by an RPC through {@link #forkWithRoundKey} share their parent with
 *       every other Step that RPC name started, so prefer {@code withCancelingSteps} there.</li>
 *   <li>A canceled branch never reports, and no other code can safely report for it. Dex decides
 *       at commit time whether a cancellation takes effect, so the code that cancels a branch
 *       cannot know whether the branch already reported. If it did, the cancellation does nothing,
 *       and an extra report counts that branch twice, so an all-branches join can fire while
 *       another branch has not reported. To keep an all-branches join from waiting forever for a
 *       canceled branch, cancel the join Step together with its branches, or wait on
 *       {@link Wait#anyOf} with the join condition and a Timer and handle the timeout in
 *       Execute.</li>
 *   <li>Never reset a cancelling join's round with {@link #forRemainingBranches}: it waits for
 *       results that canceled branches never report. The next round forks a new round without the
 *       reset wait, as the first round does. The canceled round's late results stay until the Flow
 *       closes.</li>
 *   <li>Canceling a join Step after its wait matched but before its Execute committed makes the
 *       round unable to fire again, unless the Dex Server returns a canceled Step's consumed
 *       messages to their Channel. Check the server change log for that behavior before
 *       recreating a join for the same round.</li>
 * </ul>
 *
 * @param <T> the branch result type; each result is stored exactly as a {@code ChannelMap<T>}
 *     message
 * @since 1.44.0
 */
public final class Join<T> extends PersistenceDefinition {
    private final ChannelMap<T> channelMap;

    private Join(final ChannelMap<T> channelMap) {
        this.channelMap = channelMap;
    }

    /**
     * Defines a typed Join.
     *
     * <p>Declare the Join once, typically as a final field of the Flow, and register it in the
     * Flow's {@link PersistenceSchema}. The name is durable: renaming it strands the branch results
     * of open Flows.
     *
     * @param name the stable Join name, which is also the name of the underlying ChannelMap; must
     *     not be blank or contain {@code /}
     * @param resultType the concrete Java class of each branch result
     * @param <T> the branch result type
     * @return the Join definition
     * @throws IllegalArgumentException if {@code name} is {@code null}, blank, or contains {@code /}
     * @throws NullPointerException if {@code resultType} is {@code null}
     */
    public static <T> Join<T> define(final String name, final Class<T> resultType) {
        return new Join<T>(ChannelMap.define(name, resultType));
    }

    /**
     * Returns the durable name of this Join, which is also the name of its ChannelMap.
     *
     * @return the Join name
     */
    @Override
    public String getName() {
        return channelMap.getName();
    }

    @Override
    Class<T> getValueType() {
        return channelMap.getValueType();
    }

    /**
     * Returns the ChannelMap that stores this Join's branch results.
     *
     * <p>Use it to inspect a round's pending results with the existing ChannelMap API, passing the
     * round's {@link JoinRound#getRoundKey()} as the instance key. For example, an RPC can load the
     * round's instance with {@link RPCInvokeOptions.Builder#addLoadChannelMapInstance}, then read
     * {@link ChannelMap#pendingMessages} and {@link ChannelMap#size}. Late results that no reset
     * consumed stay there until the Flow closes.
     *
     * <p>The returned object is the definition registered for this Join, so every API that checks
     * the registered ChannelMap accepts it. Never publish to it or delete from it directly. Report
     * branch results only with {@link #endBranch} or {@link #reportBranchResult}; a direct
     * publication or deletion breaks the exactly-once result count of the round.
     *
     * <pre>{@code
     * List<ChannelMessage<String>> pending =
     *         quoteReceived.getChannelMap().pendingMessages(context, round.getRoundKey());
     * }</pre>
     *
     * @return the registered ChannelMap with the same name as this Join
     * @since 1.44.0
     */
    public ChannelMap<T> getChannelMap() {
        return channelMap;
    }

    /**
     * Starts a new round from a split Step and builds its branch and join movements.
     *
     * <p>The round key is the current Step execution ID, which is unique in the Flow run and stays
     * the same across retries. The branch count is {@code branchBuilders.size()}. Fork calls every
     * branch builder in order and then the join builder, each with the same {@link JoinRound}, and
     * returns the movements in that order. Pass them to {@link StepDecision#goToMany(List)},
     * optionally together with other movements. Fork itself makes no server call.
     *
     * <p>An empty builder list is allowed. Its round has zero branches, and
     * {@link #forAllBranches} on it is satisfied immediately.
     *
     * <p>See {@link Join} for a complete example.
     *
     * @param context the Step invocation context
     * @param branchBuilders one builder per branch; each receives the round and returns the
     *     branch's first movement, whose input should carry the round
     * @param joinBuilder receives the round and returns the join Step's movement, whose input
     *     should carry the round
     * @return the branch movements in builder order, followed by the join movement
     * @throws NullPointerException if an argument, a builder, or a built movement is {@code null}
     * @throws IllegalStateException if called outside a Step invocation, such as from an RPC, or
     *     if this Join was already forked in the same invocation
     * @throws IllegalArgumentException if {@code context} is not a Dex invocation context
     */
    public List<StepMovement<?>> fork(
            final Context context,
            final List<? extends Function<JoinRound, ? extends StepMovement<?>>> branchBuilders,
            final Function<JoinRound, ? extends StepMovement<?>> joinBuilder) {
        Objects.requireNonNull(context, "context");
        if (!(context instanceof InvocationContext)) {
            throw new IllegalArgumentException("Dex invocation Context is required");
        }
        final String stepExecutionId = context.getStepExecutionId();
        if (stepExecutionId == null || stepExecutionId.isEmpty()) {
            throw new IllegalStateException("Join " + getName()
                    + " fork requires a Step execution ID; use forkWithRoundKey in an RPC");
        }
        if (!((InvocationContext) context).recordJoinFork(getName())) {
            throw new IllegalStateException("Join " + getName()
                    + " was already forked in this invocation; use another Join or"
                    + " forkWithRoundKey with distinct keys");
        }
        return buildMovements(stepExecutionId, branchBuilders, joinBuilder);
    }

    /**
     * Starts a new round with an application-chosen key and builds its branch and join movements.
     *
     * <p>Use it in an RPC, which has no Step execution ID, or when a migrated Flow must keep the
     * instance keys of a hand-written join, such as {@code round-3}. The key must be unique among
     * the rounds of this Join that can be active at the same time; two active rounds with the same
     * key share their results. Otherwise it behaves like {@link #fork}. In an RPC, return the
     * movements with {@link RPCResult#of(Object, List)}.
     *
     * @param roundKey the round key, used as the ChannelMap instance key; must not be blank or
     *     contain {@code /}
     * @param branchBuilders one builder per branch; each receives the round and returns the
     *     branch's first movement
     * @param joinBuilder receives the round and returns the join Step's movement
     * @return the branch movements in builder order, followed by the join movement
     * @throws IllegalArgumentException if {@code roundKey} is {@code null}, blank, or contains
     *     {@code /}
     * @throws NullPointerException if a builder list, a builder, or a built movement is
     *     {@code null}
     */
    public List<StepMovement<?>> forkWithRoundKey(
            final String roundKey,
            final List<? extends Function<JoinRound, ? extends StepMovement<?>>> branchBuilders,
            final Function<JoinRound, ? extends StepMovement<?>> joinBuilder) {
        return buildMovements(requireRoundKey(roundKey), branchBuilders, joinBuilder);
    }

    /**
     * Reports this branch's result and ends the branch.
     *
     * <p>Return the decision from the last Step of a branch. It publishes {@code result} to the
     * round and returns {@link StepDecision#deadEnd()}. The publication commits together with this
     * Execute, so it counts exactly once. Report from the recovery Step too when the branch's
     * failure route skips this call, or the join may wait forever.
     *
     * @param context the Step invocation context
     * @param round the round received in the branch Step's input
     * @param result the branch result; it may carry the branch identity when the join needs to
     *     know which branch finished
     * @return a dead-end decision
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if this Join is not registered in the Flow
     * @throws io.superdurable.dex.exceptions.ValueMappingException if the result cannot be encoded
     */
    public StepDecision endBranch(final Context context, final JoinRound round, final T result) {
        reportBranchResult(context, round, result);
        return StepDecision.deadEnd();
    }

    /**
     * Reports this branch's result without ending the branch.
     *
     * <p>Use it when a branch continues after reporting, for example when a multi-Step branch
     * reports from a Step other than its last. An RPC that forked a round with
     * {@link #forkWithRoundKey} can also report results for that round. The result is published to
     * the round's ChannelMap instance and commits together with the current method. Each branch
     * reports exactly once per round. Never report for a canceled branch; see the {@link Join}
     * rules.
     *
     * @param context the Step or RPC invocation context
     * @param round the round of the branch
     * @param result the branch result
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if this Join is not registered in the Flow
     * @throws io.superdurable.dex.exceptions.ValueMappingException if the result cannot be encoded
     */
    public void reportBranchResult(final Context context, final JoinRound round, final T result) {
        channelMap.publish(context, requireValidRound(round).getRoundKey(), result);
    }

    /**
     * Creates a condition satisfied when every branch of the round has reported.
     *
     * <p>Return it from the join Step's WaitFor, alone or inside {@link Wait#allOf},
     * {@link Wait#anyOf}, or {@link Wait#anyCombinationOf}. It consumes exactly
     * {@code round.getBranchCount()} results. A round with zero branches is satisfied
     * immediately.
     *
     * @param round the round received in the join Step's input
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count
     */
    public Condition forAllBranches(final JoinRound round) {
        return forAllBranches(round, null);
    }

    /**
     * Creates a named condition satisfied when every branch of the round has reported.
     *
     * @param round the round received in the join Step's input
     * @param conditionId the condition ID used to identify the condition in a combined wait, or
     *     {@code null}
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count
     * @see #forAllBranches(JoinRound)
     */
    public Condition forAllBranches(final JoinRound round, final String conditionId) {
        final JoinRound validRound = requireValidRound(round);
        return channelMap.forN(validRound.getRoundKey(), validRound.getBranchCount(), conditionId);
    }

    /**
     * Creates a condition satisfied by the first branch result of the round.
     *
     * <p>It consumes exactly one result: the earliest committed one, even when several are already
     * waiting. Use it for a discriminator. Put the branch identity in the result when the join
     * must know which branch won. Reset with {@code forRemainingBranches(round, 1)} when the Flow
     * loops.
     *
     * <p>A round with zero branches has no first result, so this method throws instead of
     * returning a condition that would wait forever. When a round may be empty, check
     * {@link JoinRound#getBranchCount()} first.
     *
     * @param round the round received in the join Step's input
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or fewer than one branch
     */
    public Condition forFirstBranch(final JoinRound round) {
        return forFirstBranch(round, null);
    }

    /**
     * Creates a named condition satisfied by the first branch result of the round.
     *
     * @param round the round received in the join Step's input
     * @param conditionId the condition ID used to identify the condition in a combined wait, or
     *     {@code null}
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or fewer than one branch
     * @see #forFirstBranch(JoinRound)
     */
    public Condition forFirstBranch(final JoinRound round, final String conditionId) {
        return forBranches(round, 1, conditionId);
    }

    /**
     * Creates a condition satisfied when {@code count} branches of the round have reported.
     *
     * <p>Use it for a partial join (n of m). It consumes exactly {@code count} results in commit
     * order. Reset with {@code forRemainingBranches(round, count)} when the Flow loops.
     *
     * @param round the round received in the join Step's input
     * @param count the number of results to wait for and consume; from 1 to the branch count
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if {@code count} is outside 1 to the branch count
     */
    public Condition forBranches(final JoinRound round, final int count) {
        return forBranches(round, count, null);
    }

    /**
     * Creates a named condition satisfied when {@code count} branches of the round have reported.
     *
     * @param round the round received in the join Step's input
     * @param count the number of results to wait for and consume; from 1 to the branch count
     * @param conditionId the condition ID used to identify the condition in a combined wait, or
     *     {@code null}
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if {@code count} is outside 1 to the branch count
     * @see #forBranches(JoinRound, int)
     */
    public Condition forBranches(
            final JoinRound round,
            final int count,
            final String conditionId) {
        final JoinRound validRound = requireValidRound(round);
        if (count < 1 || count > validRound.getBranchCount()) {
            throw new IllegalArgumentException("Join " + getName() + " branch count to wait for "
                    + count + " must be between 1 and " + validRound.getBranchCount());
        }
        return channelMap.forN(validRound.getRoundKey(), count, conditionId);
    }

    /**
     * Returns the branch results consumed by the join condition, in commit order.
     *
     * <p>Call it from the join Step's Execute after {@link #forAllBranches},
     * {@link #forFirstBranch}, or {@link #forBranches} was satisfied. When the condition was not
     * satisfied, for example because a Timer won an {@link Wait#anyOf}, the list is empty.
     *
     * @param context the Step invocation context
     * @param round the round received in the join Step's input
     * @return the immutable typed results
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if this Join is not registered in the Flow
     */
    public List<T> getBranchResults(final Context context, final JoinRound round) {
        return channelMap.getConditionResults(context, requireValidRound(round).getRoundKey());
    }

    /**
     * Creates a condition that waits for and consumes the late results of a round: the reset.
     *
     * <p>Return it from the next round's split Step's WaitFor, with the previous round and the
     * number of results its join consumed. It waits for the remaining
     * {@code round.getBranchCount() - receivedCount} results and consumes them, so the next round
     * starts only after every branch of the previous round reported, and the previous round leaves
     * no messages behind. When nothing remains, it is satisfied immediately. In the first round,
     * which has no previous round, return {@link Wait#skipImmediately()} instead.
     *
     * <p>Count only the results the join consumed. The reset waits for every other branch of the
     * round, so it never completes while a canceled branch has no reported result. Never reset the
     * round of a cancelling join; the next round forks a new round without this reset.
     *
     * @param round the previous round
     * @param receivedCount the number of results already consumed from that round, which is the
     *     join's {@code count}, or 1 for a first-branch join; from 0 to the branch count
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if {@code receivedCount} is outside 0 to the branch count
     */
    public Condition forRemainingBranches(final JoinRound round, final int receivedCount) {
        return forRemainingBranches(round, receivedCount, null);
    }

    /**
     * Creates a named condition that waits for and consumes the late results of a round.
     *
     * @param round the previous round
     * @param receivedCount the number of results already consumed from that round; from 0 to the
     *     branch count
     * @param conditionId the condition ID used to identify the condition in a combined wait, or
     *     {@code null}
     * @return the condition
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if {@code receivedCount} is outside 0 to the branch count
     * @see #forRemainingBranches(JoinRound, int)
     */
    public Condition forRemainingBranches(
            final JoinRound round,
            final int receivedCount,
            final String conditionId) {
        final JoinRound validRound = requireValidRound(round);
        if (receivedCount < 0 || receivedCount > validRound.getBranchCount()) {
            throw new IllegalArgumentException("Join " + getName() + " received count "
                    + receivedCount + " must be between 0 and " + validRound.getBranchCount());
        }
        return channelMap.forN(
                validRound.getRoundKey(),
                validRound.getBranchCount() - receivedCount,
                conditionId);
    }

    /**
     * Returns the late results consumed by {@link #forRemainingBranches}, in commit order.
     *
     * <p>Call it from the Execute of the Step that waited for the reset, usually the next round's
     * split Step. It reads the same ChannelMap instance as {@link #getBranchResults}; the separate
     * name states at the call site that these results arrived after the join fired.
     *
     * @param context the Step invocation context
     * @param round the previous round
     * @return the immutable typed late results
     * @throws IllegalArgumentException if the round has a blank key, a key containing {@code /},
     *     or a negative branch count, or if this Join is not registered in the Flow
     */
    public List<T> getRemainingBranchResults(final Context context, final JoinRound round) {
        return channelMap.getConditionResults(context, requireValidRound(round).getRoundKey());
    }

    private List<StepMovement<?>> buildMovements(
            final String roundKey,
            final List<? extends Function<JoinRound, ? extends StepMovement<?>>> branchBuilders,
            final Function<JoinRound, ? extends StepMovement<?>> joinBuilder) {
        Objects.requireNonNull(branchBuilders, "branchBuilders");
        Objects.requireNonNull(joinBuilder, "joinBuilder");
        final JoinRound round = new JoinRound(roundKey, branchBuilders.size());
        final List<StepMovement<?>> movements =
                new ArrayList<StepMovement<?>>(branchBuilders.size() + 1);
        for (Function<JoinRound, ? extends StepMovement<?>> branchBuilder : branchBuilders) {
            final Function<JoinRound, ? extends StepMovement<?>> builder =
                    Objects.requireNonNull(branchBuilder, "branch builder");
            movements.add(Objects.requireNonNull(builder.apply(round), "branch movement"));
        }
        movements.add(Objects.requireNonNull(joinBuilder.apply(round), "join movement"));
        return Collections.unmodifiableList(movements);
    }

    private JoinRound requireValidRound(final JoinRound round) {
        Objects.requireNonNull(round, "round");
        requireRoundKey(round.getRoundKey());
        if (round.getBranchCount() < 0) {
            throw new IllegalArgumentException("Join " + getName() + " round "
                    + round.getRoundKey() + " has negative branch count "
                    + round.getBranchCount());
        }
        return round;
    }

    private String requireRoundKey(final String roundKey) {
        if (roundKey == null || roundKey.trim().isEmpty() || roundKey.contains("/")) {
            throw new IllegalArgumentException("Join " + getName()
                    + " round key must be nonblank and must not contain '/'");
        }
        return roundKey;
    }
}
