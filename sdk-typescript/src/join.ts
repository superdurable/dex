// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { jsonCodec, type Codec } from "./codec.js";
import type { Context } from "./context.js";
import { deadEnd, type StepDecision, type StepMovement } from "./step.js";
import { requireMapInstance } from "./validation.js";
import { ChannelMap, type Channel, type Condition } from "./wait.js";

/**
 * Identifies one round of a {@link Join} and the number of branches that round started.
 *
 * {@link Join.fork} and {@link Join.forkWithRoundKey} create a JoinRound and pass the same value to
 * every branch builder and to the join builder. Put it in the input of each branch Step, of the
 * join Step, and of the next round's split Step. Treat it as an opaque token: pass it back to the
 * Join methods and do not build or change one yourself.
 *
 * Dex stores a JoinRound inside Step inputs, so its JSON shape is a persistent format. The shape is
 * `{"roundKey": string, "branchCount": integer}` in every Dex SDK. Use {@link joinRoundCodec} when a
 * Step input is exactly a JoinRound. Inside a larger JSON input, the default JSON encoding of this
 * object produces the same shape.
 *
 * @example
 * ```ts
 * class ReviewDocument implements Step<JoinRound> {
 *   public readonly inputCodec = joinRoundCodec;
 *
 *   public getStepType(): string {
 *     return "ReviewDocument";
 *   }
 *
 *   public execute(context: Context, round: JoinRound): StepDecision {
 *     return reviewCompleted.endBranch(context, round, "approved");
 *   }
 * }
 * ```
 * @since 1.43.0
 */
export interface JoinRound {
  /**
   * ChannelMap instance key that holds this round's branch results.
   *
   * {@link Join.fork} uses the split Step's execution ID, such as `RequestQuotes-2`. Dex Web shows
   * this key as the instance of the ChannelMap named after the Join.
   */
  readonly roundKey: string;
  /**
   * Number of branches that the fork started for this round.
   *
   * It equals the number of branch builders passed to the fork and can be 0.
   */
  readonly branchCount: number;
}

/**
 * Encodes a {@link JoinRound} as the JSON object `{"roundKey": string, "branchCount": integer}`.
 *
 * Use it as the `inputCodec` of a branch Step whose input is exactly a JoinRound. Encoding writes
 * only the two fields. Decoding ignores unknown fields, because later releases may add fields.
 *
 * @example
 * ```ts
 * class QuoteFromSupplierA implements Step<JoinRound> {
 *   public readonly inputCodec = joinRoundCodec;
 *
 *   public getStepType(): string {
 *     return "QuoteFromSupplierA";
 *   }
 *
 *   public execute(context: Context, round: JoinRound): StepDecision {
 *     return quoteReceived.endBranch(context, round, "A");
 *   }
 * }
 * ```
 * Decoding throws a TypeError when `roundKey` is not a non-empty string without `/`, or when
 * `branchCount` is not a non-negative safe integer.
 * @since 1.43.0
 */
export const joinRoundCodec: Codec<JoinRound> = jsonCodec<JoinRound>({
  typeName: "JoinRound",
  encode: (round) => ({ roundKey: round.roundKey, branchCount: round.branchCount }),
  decode: decodeJoinRound,
});

const forkedJoinNamesByContext = new WeakMap<Context, Set<string>>();

/**
 * Synchronizes parallel branches: a join Step waits until branches of the same round report results.
 *
 * Use a Join when one Step starts several branches and a later Step must continue after all of
 * them, the first of them, or any `n` of them report a result. A Join is a ChannelMap named
 * {@link Join.name}. Each round uses its own ChannelMap instance. Each branch publishes one
 * message: the result value `T` itself.
 *
 * Declare a Join once, at module level, and register it in the Flow's
 * `getPersistenceSchema().channels`. Its name must not match another Channel, ChannelMap, Join,
 * Attribute, or Stream of the Flow. Dex Web shows it as a ChannelMap.
 *
 * Use it in four places:
 * - Split: the Step that starts the branches returns `goToMany(...join.fork(...))`. {@link Join.fork}
 *   creates one {@link JoinRound} and passes it to every branch builder and to the join builder.
 * - Branch: the last Step of each branch calls {@link Join.endBranch}, or {@link Join.reportBranchResult}
 *   when the branch continues afterwards.
 * - Join: the join Step's `waitFor` returns {@link Join.forAllBranches}, {@link Join.forFirstBranch},
 *   or {@link Join.forBranches}. Its `execute` reads the results with {@link Join.branchResults}.
 * - Reset: when the Flow forks the same Join again in a loop, pass the previous JoinRound to the
 *   next split Step. Its `waitFor` returns {@link Join.forRemainingBranches}. The next round then
 *   starts only after every branch of the previous round has reported.
 *
 * Rules and traps:
 * - Each branch must report exactly one result per round, including on its recovery path. A branch
 *   that never reports keeps an all-branches join waiting forever. A branch that reports twice is
 *   counted twice. Add a Timer, such as `Wait.anyOf(join.forAllBranches(round), Timer.byDuration(...))`,
 *   when a branch may never report.
 * - A branch passes its JoinRound only to `endBranch` or `reportBranchResult`. It must not wait on a
 *   join Condition. Only the join Step that the fork started waits on a round; two waiting Steps
 *   would split the results between them.
 * - A Step or retry attempt forks one Join at most once. A second {@link Join.fork} call in the same
 *   attempt throws. Use two Joins, or {@link Join.forkWithRoundKey} with distinct keys, for two
 *   independent joins.
 * - An RPC has no Step execution ID, so `fork` throws there. Use `forkWithRoundKey` in an RPC.
 * - The `receivedCount` passed to `forRemainingBranches` must equal the number of results the join
 *   consumed: the `count` of `forBranches`, 1 for `forFirstBranch`, or `branchCount` for
 *   `forAllBranches`.
 * - Results that arrive after the join fired stay in the round's ChannelMap instance until a reset
 *   consumes them or the Flow ends. They never satisfy another round. In a loop, always reset.
 * - To cancel the remaining branches when the join fires, return the join decision with
 *   `withCancelingSiblingSteps(decision, BranchA, BranchB)`. `fork` puts every branch and the join
 *   Step in one decision, so they are siblings. Sibling cancellation does not reach later Steps of
 *   a multi-Step branch; use `withCancelingSteps` there when only one round runs at a time. Steps
 *   started by an RPC are siblings of every Step started by any call of that RPC, so for a round
 *   forked by {@link Join.forkWithRoundKey} in an RPC, cancel the branches with `withCancelingSteps`.
 * - A canceled branch reports nothing, so an all-branches join keeps waiting. Cancel the join Step
 *   together with the branches, add a Timer, or report a substitute result for the canceled branch
 *   from the canceling RPC with {@link Join.reportBranchResult}.
 * - The same holds for a reset: `forRemainingBranches` waits for results that canceled branches
 *   never report. A cancelling join's round therefore resets only when an RPC reports a substitute
 *   result for each canceled branch. Without that, do not reset the round; its late results stay
 *   until the Flow ends.
 * - When Dex cancels a join Step after its wait consumed results but before its `execute` commits,
 *   what happens to those results depends on the server version. A server that requeues consumed
 *   messages on cancellation returns them to the front of the round's instance, unless the
 *   canceling decision closes the Flow unconditionally. An older server drops them, and the round
 *   can no longer fire. See the
 *   [server change log](https://docs.superdurable.io/reference/server/changelog).
 *
 * @example
 * ```ts
 * import {
 *   Join,
 *   StepList,
 *   StepMovement,
 *   Wait,
 *   goToMany,
 *   gracefulComplete,
 *   joinRoundCodec,
 *   jsonCodec,
 *   stringCodec,
 *   type Context,
 *   type Flow,
 *   type JoinRound,
 *   type PersistenceSchema,
 *   type Step,
 *   type StepDecision,
 * } from "@superdurable/dex";
 *
 * const quoteReceived = new Join("quoteReceived", stringCodec);
 *
 * interface QuoteRequest {
 *   readonly requiredQuotes: number;
 *   readonly roundNumber: number;
 *   readonly roundCount: number;
 *   readonly previousRound?: JoinRound;
 * }
 *
 * interface QuoteJoinInput {
 *   readonly request: QuoteRequest;
 *   readonly round: JoinRound;
 * }
 *
 * // Split: waits for the previous round's reset, then starts two branches and the join Step.
 * class RequestQuotes implements Step<QuoteRequest> {
 *   public getStepType(): string {
 *     return "RequestQuotes";
 *   }
 *
 *   public waitFor(_context: Context, request: QuoteRequest): Wait {
 *     if (request.previousRound === undefined) {
 *       return Wait.skipImmediately();
 *     }
 *     // The join consumed requiredQuotes results; consume the rest of that round.
 *     return Wait.until(
 *       quoteReceived.forRemainingBranches(request.previousRound, request.requiredQuotes),
 *     );
 *   }
 *
 *   public execute(context: Context, request: QuoteRequest): StepDecision {
 *     if (request.previousRound !== undefined) {
 *       const lateQuotes = quoteReceived.remainingBranchResults(context, request.previousRound);
 *       console.log(`ignored ${lateQuotes.length} late quotes`);
 *     }
 *     return goToMany(...quoteReceived.fork(
 *       context,
 *       [
 *         (round) => StepMovement.of(QuoteFromSupplierA, round),
 *         (round) => StepMovement.of(QuoteFromSupplierB, round),
 *       ],
 *       (round) => StepMovement.of(AcceptQuotes, { request, round }),
 *     ));
 *   }
 * }
 *
 * // Branches: each reports exactly one result and ends.
 * class QuoteFromSupplierA implements Step<JoinRound> {
 *   public readonly inputCodec = joinRoundCodec;
 *
 *   public getStepType(): string {
 *     return "QuoteFromSupplierA";
 *   }
 *
 *   public execute(context: Context, round: JoinRound): StepDecision {
 *     return quoteReceived.endBranch(context, round, "A");
 *   }
 * }
 *
 * class QuoteFromSupplierB implements Step<JoinRound> {
 *   public readonly inputCodec = joinRoundCodec;
 *
 *   public getStepType(): string {
 *     return "QuoteFromSupplierB";
 *   }
 *
 *   public execute(context: Context, round: JoinRound): StepDecision {
 *     return quoteReceived.endBranch(context, round, "B");
 *   }
 * }
 *
 * // Join: continues after the first requiredQuotes results, then starts the next round.
 * class AcceptQuotes implements Step<QuoteJoinInput> {
 *   public getStepType(): string {
 *     return "AcceptQuotes";
 *   }
 *
 *   public waitFor(_context: Context, input: QuoteJoinInput): Wait {
 *     return Wait.until(quoteReceived.forBranches(input.round, input.request.requiredQuotes));
 *   }
 *
 *   public execute(context: Context, input: QuoteJoinInput): StepDecision {
 *     const acceptedQuotes = quoteReceived.branchResults(context, input.round);
 *     const placeOrder = StepMovement.of(PlaceOrder, acceptedQuotes);
 *     if (input.request.roundNumber >= input.request.roundCount) {
 *       return goToMany(placeOrder);
 *     }
 *     const nextRequest: QuoteRequest = {
 *       ...input.request,
 *       roundNumber: input.request.roundNumber + 1,
 *       previousRound: input.round,
 *     };
 *     return goToMany(placeOrder, StepMovement.of(RequestQuotes, nextRequest));
 *   }
 * }
 *
 * class PlaceOrder implements Step<readonly string[]> {
 *   public readonly inputCodec = jsonCodec<readonly string[]>();
 *
 *   public getStepType(): string {
 *     return "PlaceOrder";
 *   }
 *
 *   public execute(_context: Context, quotes: readonly string[]): StepDecision {
 *     return gracefulComplete(quotes);
 *   }
 * }
 *
 * export class QuoteFlow implements Flow<QuoteRequest> {
 *   public getFlowType(): string {
 *     return "QuoteFlow";
 *   }
 *
 *   public getSteps() {
 *     return StepList.startStep(new RequestQuotes()).otherSteps(
 *       new QuoteFromSupplierA(),
 *       new QuoteFromSupplierB(),
 *       new AcceptQuotes(),
 *       new PlaceOrder(),
 *     );
 *   }
 *
 *   public getPersistenceSchema(): PersistenceSchema {
 *     return { channels: [quoteReceived] };
 *   }
 * }
 * ```
 * For a runtime number of branches, map the inputs to branch builders, as the
 * {@link Join.forkWithRoundKey} example does.
 * @typeParam T - Result type that each branch reports.
 * @since 1.43.0
 */
export class Join<T> {
  /**
   * The ChannelMap that carries this Join's branch results, named {@link Join.name}.
   *
   * Use it to read a round's pending results with the existing ChannelMap API. The instance key
   * of a round is its {@link JoinRound.roundKey}. For example, an RPC selects the round with
   * `join.channelMap.loadMessages(round.roundKey)` in its load options, then reads
   * `join.channelMap.pendingMessages(context, round.roundKey)` or
   * `join.channelMap.size(context, round.roundKey)`. Pending results are the results that no join
   * or reset has consumed, such as late results after a first-branch join.
   *
   * This is the exact definition the Flow registers when its PersistenceSchema lists the Join, so
   * every API that checks for a registered ChannelMap accepts it.
   *
   * Never publish to it or delete its messages directly. Report results only with
   * {@link Join.endBranch} or {@link Join.reportBranchResult}. A direct publication or deletion
   * breaks the rule that each branch adds exactly one result per round, so joins and resets
   * count wrong.
   *
   * @example
   * ```ts
   * class QuoteFlow implements Flow<never> {
   *   // getFlowType, getSteps, and getPersistenceSchema register the Steps and quoteReceived.
   *
   *   @rpc()
   *   public getLateQuotes(context: Context, round: JoinRound): RPCResult<readonly string[]> {
   *     const messages = quoteReceived.channelMap.pendingMessages(context, round.roundKey);
   *     return { output: messages.map((message) => message.value) };
   *   }
   * }
   *
   * const lateQuotes = await client.invokeRPCWithOptions(quoteFlow.getLateQuotes, "quote-42", round, {
   *   loadChannelMapInstances: [quoteReceived.channelMap.loadMessages(round.roundKey)],
   * });
   * ```
   * @since 1.43.0
   */
  public readonly channelMap: ChannelMap<T>;

  /**
   * Creates a Join definition for a PersistenceSchema.
   * @param name - Durable name of the Join and of its ChannelMap. It must be non-empty, must not
   * contain `/`, and must be unique among the Flow's persistence definitions. Changing it while
   * Flows are running loses their pending results.
   * @param codec - Codec of the branch result type. Changing the encoding while Flows are running
   * can make pending results undecodable.
   * @throws {TypeError} When `name` is empty, contains only whitespace, or contains `/`.
   */
  public constructor(
    public readonly name: string,
    public readonly codec: Codec<T>,
  ) {
    this.channelMap = new ChannelMap(name, codec);
  }

  /**
   * Starts one round from a Step: creates its {@link JoinRound} and the branch and join movements.
   *
   * Call it in the `execute` of the split Step and return the movements, for example
   * `goToMany(...join.fork(context, branchBuilders, joinBuilder))`. Add more movements to the same
   * decision when needed. The round key is the current Step execution ID, which stays the same
   * across retries. The branch count is `branchBuilders.length`, so the application never counts
   * branches. An empty list creates a round with 0 branches, whose {@link Join.forAllBranches}
   * Condition is ready immediately.
   *
   * The fork calls each branch builder in order, then the join builder, and returns their
   * movements in that order. It does not contact Dex. See {@link Join} for a complete example.
   * @param context - Context of the current Step `waitFor` or `execute` call.
   * @param branchBuilders - One function per branch. Each receives the round and returns the
   * movement that starts that branch.
   * @param joinBuilder - Receives the round and returns the movement of the join Step.
   * @returns The branch movements followed by the join movement.
   * @throws {TypeError} When `context` belongs to an RPC, which has no Step execution ID. Use
   * {@link Join.forkWithRoundKey} there.
   * @throws {Error} When the same attempt already forked this Join.
   */
  public fork(
    context: Context,
    branchBuilders: readonly ((round: JoinRound) => StepMovement<any>)[],
    joinBuilder: (round: JoinRound) => StepMovement<any>,
  ): readonly StepMovement<any>[] {
    if (context.stepExecutionId === "") {
      throw new TypeError(`Join ${this.name} fork requires a Step context; use forkWithRoundKey in an RPC`);
    }
    let forkedJoinNames = forkedJoinNamesByContext.get(context);
    if (forkedJoinNames === undefined) {
      forkedJoinNames = new Set();
      forkedJoinNamesByContext.set(context, forkedJoinNames);
    }
    if (forkedJoinNames.has(this.name)) {
      throw new Error(
        `Join ${this.name} was already forked by Step execution ${context.stepExecutionId}; ` +
          "use another Join or forkWithRoundKey with a distinct key",
      );
    }
    forkedJoinNames.add(this.name);
    return this.buildRoundMovements(context.stepExecutionId, branchBuilders, joinBuilder);
  }

  /**
   * Starts one round with an application-supplied round key.
   *
   * Use it in an RPC, which has no Step execution ID, or when the application already has a unique
   * key per round. It behaves like {@link Join.fork} otherwise. Return the movements as the RPC's
   * `nextSteps` or in a Step decision.
   *
   * The key must be unique per round of this Join within the Flow run. Reusing a key mixes results
   * of different rounds. Steps started by an RPC are siblings of every Step started by any call of
   * the same RPC, so prefer `withCancelingSteps` over `withCancelingSiblingSteps` for these rounds.
   * When you migrate a hand-written ChannelMap join to a Join with the same name, pass the old
   * instance key to keep running Flows working.
   *
   * @example
   * ```ts
   * import {
   *   Join,
   *   StepList,
   *   StepMovement,
   *   Wait,
   *   gracefulComplete,
   *   joinRoundCodec,
   *   rpc,
   *   stringCodec,
   *   type Context,
   *   type Flow,
   *   type JoinRound,
   *   type PersistenceSchema,
   *   type RPCResult,
   *   type Step,
   *   type StepDecision,
   * } from "@superdurable/dex";
   *
   * const reviewCompleted = new Join("reviewCompleted", stringCodec);
   *
   * interface ReviewRequest {
   *   readonly requestId: string;
   *   readonly reviewers: readonly string[];
   * }
   *
   * interface ReviewerInput {
   *   readonly reviewer: string;
   *   readonly round: JoinRound;
   * }
   *
   * class ReviewByReviewer implements Step<ReviewerInput> {
   *   public getStepType(): string {
   *     return "ReviewByReviewer";
   *   }
   *
   *   public execute(context: Context, input: ReviewerInput): StepDecision {
   *     return reviewCompleted.endBranch(context, input.round, `${input.reviewer}: approved`);
   *   }
   * }
   *
   * class CollectReviews implements Step<JoinRound> {
   *   public readonly inputCodec = joinRoundCodec;
   *
   *   public getStepType(): string {
   *     return "CollectReviews";
   *   }
   *
   *   public waitFor(_context: Context, round: JoinRound): Wait {
   *     return Wait.until(reviewCompleted.forAllBranches(round));
   *   }
   *
   *   public execute(context: Context, round: JoinRound): StepDecision {
   *     return gracefulComplete(reviewCompleted.branchResults(context, round));
   *   }
   * }
   *
   * export class ReviewFlow implements Flow<never> {
   *   public getFlowType(): string {
   *     return "ReviewFlow";
   *   }
   *
   *   public getSteps() {
   *     return StepList.withoutStartStep().otherSteps(new ReviewByReviewer(), new CollectReviews());
   *   }
   *
   *   public getPersistenceSchema(): PersistenceSchema {
   *     return { channels: [reviewCompleted] };
   *   }
   *
   *   @rpc()
   *   public requestReviews(_context: Context, request: ReviewRequest): RPCResult<void> {
   *     return {
   *       output: undefined,
   *       nextSteps: reviewCompleted.forkWithRoundKey(
   *         `review-${request.requestId}`,
   *         request.reviewers.map((reviewer) => (round: JoinRound) =>
   *           StepMovement.of(ReviewByReviewer, { reviewer, round })),
   *         (round) => StepMovement.of(CollectReviews, round),
   *       ),
   *     };
   *   }
   * }
   * ```
   * @param roundKey - ChannelMap instance key of the round. It must be non-empty and must not
   * contain `/`.
   * @param branchBuilders - One function per branch. Each receives the round and returns the
   * movement that starts that branch.
   * @param joinBuilder - Receives the round and returns the movement of the join Step.
   * @returns The branch movements followed by the join movement.
   * @throws {TypeError} When `roundKey` is empty, contains only whitespace, or contains `/`.
   */
  public forkWithRoundKey(
    roundKey: string,
    branchBuilders: readonly ((round: JoinRound) => StepMovement<any>)[],
    joinBuilder: (round: JoinRound) => StepMovement<any>,
  ): readonly StepMovement<any>[] {
    return this.buildRoundMovements(roundKey, branchBuilders, joinBuilder);
  }

  /**
   * Reports this branch's result and ends the branch.
   *
   * Return it from the `execute` of a branch's last Step. It stages the result like
   * {@link Join.reportBranchResult} and returns a dead-end decision. Dex commits the result only
   * when this `execute` call succeeds, so a retried attempt does not report twice.
   * @param context - Context of the current Step `execute` call.
   * @param round - Round that the fork passed to this branch.
   * @param result - Result of this branch.
   * @returns A dead-end decision.
   * @throws {TypeError} When `round` has an invalid key or the Join is not registered by this Flow.
   * @throws {RangeError} When `round.branchCount` is not a non-negative safe integer.
   */
  public endBranch(context: Context, round: JoinRound, result: T): StepDecision {
    this.reportBranchResult(context, round, result);
    return deadEnd();
  }

  /**
   * Reports this branch's result without ending the branch.
   *
   * Use it when the branch continues after it reports, or from an RPC that reports a substitute
   * result for a branch it canceled. The result is staged with the current handler result and
   * committed only when that handler succeeds. Each branch must report exactly once per round.
   * See {@link Join} for an example.
   * @param context - Context of the current Step or RPC call.
   * @param round - Round that the fork passed to this branch.
   * @param result - Result of this branch.
   * @throws {TypeError} When `round` has an invalid key or the Join is not registered by this Flow.
   * @throws {RangeError} When `round.branchCount` is not a non-negative safe integer.
   */
  public reportBranchResult(context: Context, round: JoinRound, result: T): void {
    requireJoinRound(round);
    this.channelMap.publish(context, round.roundKey, result);
  }

  /**
   * Creates a Condition that waits for the results of every branch of a round.
   *
   * Return it from the join Step's `waitFor`. It consumes exactly `round.branchCount` results.
   * A round with 0 branches is ready immediately. Combine it with a Timer through `Wait.anyOf`
   * when a branch may never report. See {@link Join} for an example.
   * @param round - Round that the fork passed to the join Step.
   * @param conditionId - Optional stable Condition ID, required inside `Wait.anyCombinationOf`.
   * @returns A Channel Condition on the round's ChannelMap instance.
   * @throws {TypeError} When `round` has an invalid key or `conditionId` is empty.
   * @throws {RangeError} When `round.branchCount` is not a non-negative safe integer.
   */
  public forAllBranches(round: JoinRound, conditionId?: string): Condition {
    requireJoinRound(round);
    return this.channelMap.forN(round.roundKey, round.branchCount, conditionId);
  }

  /**
   * Creates a Condition that waits for the first result of a round.
   *
   * Return it from the join Step's `waitFor` to continue with the first branch that reports.
   * It consumes exactly one result, the earliest one Dex committed. Put the branch's identity in the
   * result when the join must know which branch won. The reset of this round uses a
   * `receivedCount` of 1. See {@link Join} for an example.
   *
   * It behaves like `forBranches(round, 1)`. A round with 0 branches has no first result and would
   * wait forever, so this method throws for it, which fails the `waitFor` attempt.
   * @param round - Round that the fork passed to the join Step.
   * @param conditionId - Optional stable Condition ID, required inside `Wait.anyCombinationOf`.
   * @returns A Channel Condition on the round's ChannelMap instance.
   * @throws {TypeError} When `round` has an invalid key or `conditionId` is empty.
   * @throws {RangeError} When `round.branchCount` is 0 or not a non-negative safe integer.
   */
  public forFirstBranch(round: JoinRound, conditionId?: string): Condition {
    return this.forBranches(round, 1, conditionId);
  }

  /**
   * Creates a Condition that waits for `count` results of a round.
   *
   * Return it from the join Step's `waitFor` for a partial join. It consumes exactly `count`
   * results in the order Dex committed them. The reset of this round uses `count` as its
   * `receivedCount`. See {@link Join} for an example.
   * @param round - Round that the fork passed to the join Step.
   * @param count - Number of results to wait for, from 1 to `round.branchCount`.
   * @param conditionId - Optional stable Condition ID, required inside `Wait.anyCombinationOf`.
   * @returns A Channel Condition on the round's ChannelMap instance.
   * @throws {TypeError} When `round` has an invalid key or `conditionId` is empty.
   * @throws {RangeError} When `count` is not an integer from 1 to `round.branchCount`.
   */
  public forBranches(round: JoinRound, count: number, conditionId?: string): Condition {
    requireJoinRound(round);
    if (!Number.isSafeInteger(count) || count < 1 || count > round.branchCount) {
      throw new RangeError(
        `Join ${this.name} branch count ${count} must be an integer from 1 to ${round.branchCount}`,
      );
    }
    return this.channelMap.forN(round.roundKey, count, conditionId);
  }

  /**
   * Returns the results that the join Step's satisfied Condition consumed, in commit order.
   *
   * Call it in the join Step's `execute`. It returns an empty array when the round's Condition
   * was not the one that made the Wait ready, such as when a Timer won a `Wait.anyOf`.
   * @param context - Context of the join Step's `execute` call.
   * @param round - Round that the join Step waited on.
   * @returns The consumed results in the order Dex committed them.
   * @throws {TypeError} When `round` has an invalid key or the Join is not registered by this Flow.
   * @throws {RangeError} When `round.branchCount` is not a non-negative safe integer.
   */
  public branchResults(context: Context, round: JoinRound): readonly T[] {
    requireJoinRound(round);
    return this.channelMap.results(context, round.roundKey);
  }

  /**
   * Creates a Condition that consumes the remaining results of a round, which resets it.
   *
   * Return it from the `waitFor` of the next round's split Step, with the previous round from
   * that Step's input. It waits for the `round.branchCount - receivedCount` results that the join
   * did not consume. The next round therefore starts only after every branch of the previous round
   * has reported, and the previous round leaves no messages behind. When nothing remains, it is
   * ready immediately. See {@link Join} for an example.
   *
   * A canceled branch never reports, so this Condition never becomes ready for a round whose
   * branches were canceled. Reset such a round only after an RPC reports a substitute result for
   * each canceled branch with {@link Join.reportBranchResult}. Never add canceled branches to
   * `receivedCount`.
   * @param round - Previous round, as the join Step received it.
   * @param receivedCount - Number of results the join consumed: the `count` of `forBranches`, 1 for
   * `forFirstBranch`, or `round.branchCount` for `forAllBranches`. It must be from 0 to
   * `round.branchCount`.
   * @param conditionId - Optional stable Condition ID, required inside `Wait.anyCombinationOf`.
   * @returns A Channel Condition on the round's ChannelMap instance.
   * @throws {TypeError} When `round` has an invalid key or `conditionId` is empty.
   * @throws {RangeError} When `receivedCount` is not an integer from 0 to `round.branchCount`.
   */
  public forRemainingBranches(round: JoinRound, receivedCount: number, conditionId?: string): Condition {
    requireJoinRound(round);
    if (!Number.isSafeInteger(receivedCount) || receivedCount < 0 || receivedCount > round.branchCount) {
      throw new RangeError(
        `Join ${this.name} received count ${receivedCount} must be an integer from 0 to ${round.branchCount}`,
      );
    }
    return this.channelMap.forN(round.roundKey, round.branchCount - receivedCount, conditionId);
  }

  /**
   * Returns the late results that {@link Join.forRemainingBranches} consumed, in commit order.
   *
   * Call it in the `execute` of the Step that waited for the reset. These results arrived after
   * the join had fired, so they did not affect the join.
   * @param context - Context of the resetting Step's `execute` call.
   * @param round - Previous round that the Step waited on.
   * @returns The consumed late results in the order Dex committed them.
   * @throws {TypeError} When `round` has an invalid key or the Join is not registered by this Flow.
   * @throws {RangeError} When `round.branchCount` is not a non-negative safe integer.
   */
  public remainingBranchResults(context: Context, round: JoinRound): readonly T[] {
    return this.branchResults(context, round);
  }

  private buildRoundMovements(
    roundKey: string,
    branchBuilders: readonly ((round: JoinRound) => StepMovement<any>)[],
    joinBuilder: (round: JoinRound) => StepMovement<any>,
  ): readonly StepMovement<any>[] {
    requireMapInstance(roundKey);
    const round: JoinRound = Object.freeze({ roundKey, branchCount: branchBuilders.length });
    const movements = branchBuilders.map((buildBranch) => buildBranch(round));
    movements.push(joinBuilder(round));
    return Object.freeze(movements);
  }

}

/** Returns the ChannelMap a Flow registers for a Join, or the definition itself otherwise. */
export function registeredChannelDefinition(
  definition: Channel<unknown> | ChannelMap<unknown> | Join<unknown>,
): Channel<unknown> | ChannelMap<unknown> {
  return definition instanceof Join ? definition.channelMap : definition;
}

function requireJoinRound(round: JoinRound): void {
  requireMapInstance(round.roundKey);
  if (!Number.isSafeInteger(round.branchCount) || round.branchCount < 0) {
    throw new RangeError(`JoinRound branchCount ${round.branchCount} must be a non-negative integer`);
  }
}

function decodeJoinRound(value: unknown): JoinRound {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError("JoinRound must be a JSON object");
  }
  const { roundKey, branchCount } = value as { readonly roundKey?: unknown; readonly branchCount?: unknown };
  if (typeof roundKey !== "string") {
    throw new TypeError("JoinRound roundKey must be a string");
  }
  requireMapInstance(roundKey);
  if (typeof branchCount !== "number" || !Number.isSafeInteger(branchCount) || branchCount < 0) {
    throw new TypeError("JoinRound branchCount must be a non-negative integer");
  }
  return Object.freeze({ roundKey, branchCount });
}
