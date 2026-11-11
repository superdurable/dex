// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use serde::{Deserialize, Serialize};

use crate::registry::validate_map_instance;
use crate::{
    ChannelMap, Condition, Context, HandlerError, HandlerResult, StepDecision, StepMovement, Value,
};

/// Identifies one round of a [`Join`] and the number of branches in that round.
///
/// [`Join::fork`] and [`Join::fork_with_round_key`] create the round. Pass the same value, unchanged,
/// to every branch Step, to the join Step, and to the next round's split Step when a loop resets the
/// Join. Treat it as an opaque token: the application never computes the key or the count itself.
///
/// A `JoinRound` is persisted inside Step inputs as the JSON object
/// `{"roundKey": string, "branchCount": integer}`. Every Dex SDK uses these field names, so a
/// round created by one SDK decodes in another. Deserializing a negative `branchCount` fails.
///
/// See [`Join`] for a complete example.
///
/// Since 1.41.0.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JoinRound {
    /// The Channel-map instance that holds this round's branch results.
    ///
    /// [`Join::fork`] uses the split Step's execution ID, such as `RequestQuotes-2`.
    /// [`Join::fork_with_round_key`] uses the caller's key. Dex Web shows it as the instance key of
    /// the Join's Channel map. It is never empty and never contains `/` when the SDK creates it.
    pub round_key: String,
    /// The number of branches the round started, which is the number of branch builders passed to
    /// the fork.
    ///
    /// [`Join::for_all_branches`] waits for this many results. It may be zero, in which case that
    /// condition completes immediately.
    pub branch_count: usize,
}

/// Synchronizes the parallel branches of one Flow by counting the results they report.
///
/// Use a Join when one Step starts several branches and a later Step must continue after all of
/// them, the first of them, or any `n` of them have finished. The Join produces the branch count and
/// the round key, so the application keeps no counter and no round number.
///
/// A round has three roles:
///
/// 1. The split Step calls [`Self::fork`] in `execute`. It passes one builder per branch and one
///    builder for the join Step. Each builder receives the round's [`JoinRound`] and returns a
///    [`StepMovement`]. Return the movements with [`StepDecision::go_to_many`].
/// 2. Each branch reports exactly one result. A single-Step branch returns [`Self::end_branch`]. A
///    branch that continues after reporting calls [`Self::report_branch_result`].
/// 3. The join Step waits in `wait_for` with [`Self::for_all_branches`], [`Self::for_first_branch`],
///    or [`Self::for_branches`]. Its `execute` reads the results with [`Self::branch_results`].
///
/// A Join is a [`ChannelMap`] of `T` with the same name. Each round uses its own instance, keyed by
/// [`JoinRound::round_key`]. A branch result is stored as the plain `T` value, so Dex Web,
/// pending-message reads, and Channel-map loads show and read the Join like any Channel map.
/// Results of another round never satisfy this round's condition. The conditions are ordinary
/// [`Condition`] values. Combine them with [`crate::Wait::all_of`], [`crate::Wait::any_of`], or
/// [`crate::Wait::any_combination_of`], for example with a [`crate::Timer`] as a deadline.
///
/// After a first-of-m or n-of-m join, the other branches still report. To reuse the Join in a loop,
/// reset the finished round. Pass its `JoinRound` to the next round's split Step. That Step waits in
/// `wait_for` with [`Self::for_remaining_branches`] and the count the join consumed. Its `execute`
/// can read the late results with [`Self::remaining_branch_results`]. The next round therefore
/// cannot start before the previous round's branches have all reported. Without a reset, late
/// results stay pending in the round's instance until the Flow closes. They never count toward
/// another round.
///
/// Add the Join to the Flow with [`crate::PersistenceSchema::join`]. Declare it as a module-level
/// [`std::sync::LazyLock`] static. The name follows the Channel-map rules: it is stable, it contains
/// no `/`, and it differs from every other persistence definition of the Flow.
///
/// # Rules and traps
///
/// - Each branch reports exactly one result per round. A branch that never reports keeps an all-of
///   join waiting forever. A branch that reports twice counts twice. When an execute failure routes
///   a branch to a recovery Step, that Step must report a result that represents the failure.
/// - A multi-Step branch passes its `JoinRound` down to the Step that reports. A branch passes the
///   round only to [`Self::end_branch`] or [`Self::report_branch_result`]. It never waits on a join
///   condition.
/// - Only the join Step that the fork started waits for a round. Two Steps that wait for the same
///   round split its results between them.
/// - The `received_count` of [`Self::for_remaining_branches`] equals the number of results the
///   join consumed: `count` after [`Self::for_branches`], and `1` after [`Self::for_first_branch`].
/// - A reset waits for every branch that has not reported yet, so it never completes while a
///   canceled branch has not reported. Do not reset the round of a cancelling join, unless an RPC
///   reports a result for each canceled branch with [`Self::report_branch_result`]. Without a
///   reset, the round's late results stay pending until the Flow closes.
/// - One invocation forks one Join at most once with [`Self::fork`]. Use a second Join, or
///   [`Self::fork_with_round_key`] with distinct keys, for two independent joins.
/// - An RPC has no Step execution ID, so it must use [`Self::fork_with_round_key`].
/// - A cancelling join adds [`StepDecision::cancel_sibling_step`] for each branch Step type to the
///   join Step's decision. The fork starts the branches and the join Step from one decision, so they
///   are siblings, and only this round's branches are selected. A later Step of a multi-Step branch
///   is not a sibling. Select it with [`StepDecision::cancel_step`], which is correct only while one
///   round is active. Steps started by an RPC count as siblings of every Step started by any call of
///   that RPC. For rounds that an RPC forks, also use [`StepDecision::cancel_step`] with one active
///   round.
/// - A canceled branch never reports, so an all-of join that waits for it waits forever. Cancel the
///   join Step together with its branches, add a [`crate::Timer`] deadline with
///   [`crate::Wait::any_of`], or let the canceling RPC report a result for the canceled branch with
///   [`Self::report_branch_result`]. That RPC's input then carries the `JoinRound`.
/// - When a join Step is canceled after its wait matched, what happens to the results it consumed
///   depends on the server version. A server that requeues them puts them back at the front of
///   the round's instance, unless the canceling decision closes the Flow. On a server that drops
///   them, a new join Step for that round never completes. See the server-zd change log at
///   <https://docs.superdurable.io/reference/server/changelog>.
///
/// # Examples
///
/// A structured partial join that loops: each round asks three suppliers, continues after the
/// first `required_quotes` quotes, and resets before the next round.
///
/// ```
/// use std::sync::LazyLock;
///
/// use dex_sdk::{
///     Context, Flow, HandlerResult, Join, JoinRound, PersistenceSchema, Step, StepDecision,
///     StepList, StepMovement, Wait,
/// };
/// use serde::{Deserialize, Serialize};
///
/// static QUOTE_RECEIVED: LazyLock<Join<String>> = LazyLock::new(|| Join::new("quoteReceived"));
///
/// #[derive(Serialize, Deserialize)]
/// struct QuoteRequest {
///     required_quotes: usize,
///     round_number: u32,
///     round_count: u32,
///     previous_round: Option<JoinRound>,
/// }
///
/// #[derive(Serialize, Deserialize)]
/// struct SupplierQuote {
///     supplier: String,
///     round: JoinRound,
/// }
///
/// #[derive(Serialize, Deserialize)]
/// struct AcceptQuotesInput {
///     request: QuoteRequest,
///     round: JoinRound,
/// }
///
/// // Split: resets the previous round, then forks one branch per supplier and the join.
/// struct RequestQuotes;
///
/// impl Step for RequestQuotes {
///     type Input = QuoteRequest;
///
///     fn wait_for(&self, _context: &mut Context, request: QuoteRequest) -> HandlerResult<Wait> {
///         match &request.previous_round {
///             None => Ok(Wait::skip_immediately()),
///             Some(previous_round) => Ok(Wait::until(
///                 QUOTE_RECEIVED.for_remaining_branches(previous_round, request.required_quotes),
///             )),
///         }
///     }
///
///     fn execute(
///         &self,
///         context: &mut Context,
///         request: QuoteRequest,
///     ) -> HandlerResult<StepDecision> {
///         if let Some(previous_round) = &request.previous_round {
///             let late_quotes = QUOTE_RECEIVED.remaining_branch_results(context, previous_round)?;
///             context.record_event("lateQuotes", late_quotes)?;
///         }
///         let branch_builders = ["A", "B", "C"]
///             .into_iter()
///             .map(|supplier| {
///                 Box::new(move |round: &JoinRound| {
///                     StepMovement::to(
///                         &QuoteFromSupplier,
///                         SupplierQuote {
///                             supplier: supplier.to_string(),
///                             round: round.clone(),
///                         },
///                     )
///                 }) as Box<dyn FnOnce(&JoinRound) -> StepMovement>
///             })
///             .collect();
///         let movements = QUOTE_RECEIVED.fork(context, branch_builders, |round| {
///             StepMovement::to(&AcceptQuotes, AcceptQuotesInput { request, round })
///         })?;
///         Ok(StepDecision::go_to_many(movements))
///     }
/// }
///
/// // Branch: reports one result and ends.
/// struct QuoteFromSupplier;
///
/// impl Step for QuoteFromSupplier {
///     type Input = SupplierQuote;
///
///     fn execute(
///         &self,
///         context: &mut Context,
///         quote: SupplierQuote,
///     ) -> HandlerResult<StepDecision> {
///         QUOTE_RECEIVED.end_branch(context, &quote.round, quote.supplier)
///     }
/// }
///
/// // Join: continues after the required number of quotes, then starts the next round.
/// struct AcceptQuotes;
///
/// impl Step for AcceptQuotes {
///     type Input = AcceptQuotesInput;
///
///     fn wait_for(&self, _context: &mut Context, input: AcceptQuotesInput) -> HandlerResult<Wait> {
///         Ok(Wait::until(
///             QUOTE_RECEIVED.for_branches(&input.round, input.request.required_quotes),
///         ))
///     }
///
///     fn execute(
///         &self,
///         context: &mut Context,
///         input: AcceptQuotesInput,
///     ) -> HandlerResult<StepDecision> {
///         let accepted_quotes = QUOTE_RECEIVED.branch_results(context, &input.round)?;
///         let request = input.request;
///         if request.round_number == request.round_count {
///             return Ok(StepDecision::graceful_complete(accepted_quotes));
///         }
///         Ok(StepDecision::go_to(
///             &RequestQuotes,
///             QuoteRequest {
///                 round_number: request.round_number + 1,
///                 previous_round: Some(input.round),
///                 ..request
///             },
///         ))
///     }
/// }
///
/// struct QuoteFlow {
///     request_quotes: RequestQuotes,
///     quote_from_supplier: QuoteFromSupplier,
///     accept_quotes: AcceptQuotes,
/// }
///
/// impl Flow for QuoteFlow {
///     type StartInput = QuoteRequest;
///
///     fn steps(&self) -> StepList<'_, QuoteRequest> {
///         StepList::start(&self.request_quotes)
///             .and(&self.quote_from_supplier)
///             .and(&self.accept_quotes)
///     }
///
///     fn persistence(&self) -> PersistenceSchema {
///         PersistenceSchema::new().join(&QUOTE_RECEIVED)
///     }
/// }
/// ```
///
/// Since 1.41.0.
pub struct Join<T> {
    channel_map: ChannelMap<T>,
}

impl<T> Join<T> {
    /// Defines a Join whose branch results use the Channel map named `name`.
    ///
    /// The name is the durable identity of the Join. Keep it stable while any Flow that uses it is
    /// running. Slash is prohibited because it is a reserved character. A Join that replaces a
    /// hand-written Channel-map join keeps in-flight Flows working when it reuses that map's name and
    /// value type and forks with [`Self::fork_with_round_key`] and the same instance keys.
    ///
    /// Since 1.41.0.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            channel_map: ChannelMap::new(name),
        }
    }

    /// Starts one round from a split Step: builds every branch movement and the join movement.
    ///
    /// The round key is the current Step execution ID, which stays the same across retries and
    /// time travel. The branch count is `branch_builders.len()`. The SDK calls each branch builder
    /// once, in order, with the round, and then calls `join_builder` with the same round. The
    /// returned movements are the branch movements in builder order followed by the join movement.
    /// Pass them to [`StepDecision::go_to_many`], optionally with more movements. The call stages no
    /// durable change.
    ///
    /// An empty `branch_builders` creates a round with zero branches. Its
    /// [`Self::for_all_branches`] condition completes immediately, so a multi-choice split that
    /// selects no branch needs no special case.
    ///
    /// Each closure has its own type, so box every branch builder. Map a runtime list to builders to
    /// start one branch per element; see the example on [`Join`].
    ///
    /// # Errors
    ///
    /// Returns a [`HandlerError`] when the Context has no Step execution ID, as in an RPC; use
    /// [`Self::fork_with_round_key`] there. Also returns an error when this invocation already
    /// forked this Join with `fork`. Both errors are deterministic, so a retry fails the same way.
    ///
    /// Since 1.41.0.
    #[expect(
        clippy::type_complexity,
        reason = "the boxed builder type stays inline so Join and JoinRound remain the only public types"
    )]
    pub fn fork<'builder>(
        &self,
        context: &mut Context,
        branch_builders: Vec<Box<dyn FnOnce(&JoinRound) -> StepMovement + 'builder>>,
        join_builder: impl FnOnce(JoinRound) -> StepMovement,
    ) -> HandlerResult<Vec<StepMovement>> {
        let round_key = context.step_execution_id().to_string();
        if round_key.is_empty() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!(
                    "Join {} fork requires a Step execution ID; use fork_with_round_key in an RPC",
                    self.name()
                ),
            ));
        }
        context.record_join_fork(self.name())?;
        self.fork_with_round_key(&round_key, branch_builders, join_builder)
    }

    /// Starts one round with a caller-supplied round key; otherwise behaves like [`Self::fork`].
    ///
    /// Use it in an RPC, which has no Step execution ID, and add the returned movements to the
    /// [`crate::RpcResult`] with [`crate::RpcResult::then`]. Also use it to keep the instance keys of
    /// a hand-written Channel-map join, or to fork one Join twice in one invocation with distinct
    /// keys. The key must be unique among the Join's active rounds; a reused key mixes results of two
    /// rounds. This method does not check for a repeated fork.
    ///
    /// # Errors
    ///
    /// Returns a [`HandlerError`] when `round_key` is empty or contains `/`.
    ///
    /// Since 1.41.0.
    #[expect(
        clippy::type_complexity,
        reason = "the boxed builder type stays inline so Join and JoinRound remain the only public types"
    )]
    pub fn fork_with_round_key<'builder>(
        &self,
        round_key: &str,
        branch_builders: Vec<Box<dyn FnOnce(&JoinRound) -> StepMovement + 'builder>>,
        join_builder: impl FnOnce(JoinRound) -> StepMovement,
    ) -> HandlerResult<Vec<StepMovement>> {
        validate_map_instance(round_key).map_err(|error| {
            HandlerError::new(
                "dex_sdk::HandlerError",
                format!("Join {} round key is invalid: {error}", self.name()),
            )
        })?;
        let round = JoinRound {
            round_key: round_key.to_string(),
            branch_count: branch_builders.len(),
        };
        let mut movements = Vec::with_capacity(branch_builders.len() + 1);
        for branch_builder in branch_builders {
            movements.push(branch_builder(&round));
        }
        movements.push(join_builder(round));
        Ok(movements)
    }

    /// Reports this branch's result and returns [`StepDecision::dead_end`].
    ///
    /// Call it as the return value of a branch's last Step. The result is staged like any Channel
    /// publication: it becomes durable only when the handler returns successfully, together with the
    /// decision. A failed attempt reports nothing, and a retry does not report twice.
    ///
    /// # Errors
    ///
    /// Returns the errors of [`Self::report_branch_result`].
    ///
    /// Since 1.41.0.
    pub fn end_branch(
        &self,
        context: &mut Context,
        round: &JoinRound,
        result: T,
    ) -> HandlerResult<StepDecision>
    where
        T: Value,
    {
        self.report_branch_result(context, round, result)?;
        Ok(StepDecision::dead_end())
    }

    /// Reports this branch's result without ending the branch.
    ///
    /// Use it when the branch continues after reporting, from a Step's `wait_for` or `execute`.
    /// An RPC may also report on behalf of a canceled branch; its input then carries the round. The
    /// result is staged and becomes durable only when the handler returns successfully. Results
    /// queue in the order their handlers commit.
    ///
    /// # Errors
    ///
    /// Returns a [`HandlerError`] when the Join is not in the Flow's persistence schema, when the
    /// round key is empty or contains `/`, or when `result` cannot be encoded.
    ///
    /// Since 1.41.0.
    pub fn report_branch_result(
        &self,
        context: &mut Context,
        round: &JoinRound,
        result: T,
    ) -> HandlerResult<()>
    where
        T: Value,
    {
        self.channel_map.publish(context, &round.round_key, result)
    }

    /// Creates a condition that consumes the results of every branch in the round.
    ///
    /// It consumes exactly [`JoinRound::branch_count`] results. A round with zero branches
    /// completes immediately and consumes nothing.
    ///
    /// An invalid round, with an empty key or a key containing `/`, makes the Step's `wait_for`
    /// fail when the SDK sends the Wait.
    ///
    /// Since 1.41.0.
    pub fn for_all_branches(&self, round: &JoinRound) -> Condition {
        self.counted_condition(round, round.branch_count)
    }

    /// Creates a condition that consumes the first reported result of the round.
    ///
    /// Use it for a discriminator. When several results are already queued, it consumes only the
    /// earliest one; the others remain for [`Self::for_remaining_branches`] with a
    /// `received_count` of `1`. Put the branch identity into `T` when the join needs to know which
    /// branch won. It is [`Self::for_branches`] with a count of `1`.
    ///
    /// A round with zero branches, or an invalid round, makes the Step's `wait_for` fail when the
    /// SDK sends the Wait.
    ///
    /// Since 1.41.0.
    pub fn for_first_branch(&self, round: &JoinRound) -> Condition {
        self.for_branches(round, 1)
    }

    /// Creates a condition that consumes the first `count` reported results of the round.
    ///
    /// Use it for a partial join that continues after `count` of the round's branches have
    /// reported. Pass the same `count` to [`Self::for_remaining_branches`] when a loop resets the
    /// round.
    ///
    /// A `count` outside `1..=round.branch_count`, or an invalid round, makes the Step's `wait_for`
    /// fail when the SDK sends the Wait.
    ///
    /// Since 1.41.0.
    pub fn for_branches(&self, round: &JoinRound, count: usize) -> Condition {
        if count == 0 || count > round.branch_count {
            return Condition::invalid(format!(
                "Join {} for_branches count {count} is outside 1..={}",
                self.name(),
                round.branch_count
            ));
        }
        self.counted_condition(round, count)
    }

    /// Creates a condition that consumes the results a finished round has not consumed yet.
    ///
    /// Use it in the next round's split Step `wait_for` to reset the Join. It waits for, and
    /// consumes, `round.branch_count - received_count` results. `received_count` is the number of
    /// results the join consumed: the `count` of [`Self::for_branches`], or `1` for
    /// [`Self::for_first_branch`]. When nothing remains, it completes immediately.
    ///
    /// `received_count` counts only results the join consumed. Never count a canceled branch, which
    /// does not report. The reset therefore waits for every canceled branch too; see the rules on
    /// [`Join`] for cancelling joins.
    ///
    /// A `received_count` greater than `round.branch_count`, or an invalid round, makes the Step's
    /// `wait_for` fail when the SDK sends the Wait.
    ///
    /// Since 1.41.0.
    pub fn for_remaining_branches(&self, round: &JoinRound, received_count: usize) -> Condition {
        let Some(remaining_count) = round.branch_count.checked_sub(received_count) else {
            return Condition::invalid(format!(
                "Join {} for_remaining_branches received count {received_count} is outside 0..={}",
                self.name(),
                round.branch_count
            ));
        };
        self.counted_condition(round, remaining_count)
    }

    /// Returns the results the join Step's satisfied condition consumed, in report order.
    ///
    /// Call it in the join Step's `execute`. It returns an empty list when the round's condition
    /// did not complete, for example when a Timer won a [`crate::Wait::any_of`].
    ///
    /// # Errors
    ///
    /// Returns a [`HandlerError`] when the Join is not in the Flow's persistence schema, when the
    /// round key is invalid, or when a result cannot be decoded.
    ///
    /// Since 1.41.0.
    pub fn branch_results(&self, context: &Context, round: &JoinRound) -> HandlerResult<Vec<T>>
    where
        T: Value,
    {
        self.channel_map
            .condition_results(context, &round.round_key)
    }

    /// Returns the late results that a reset with [`Self::for_remaining_branches`] consumed.
    ///
    /// Call it in the `execute` of the Step that waited for the reset, usually the next round's
    /// split Step. It reads the same consumed results as [`Self::branch_results`]; the separate name
    /// states at the call site that these results arrived after the round's join.
    ///
    /// # Errors
    ///
    /// Returns the errors of [`Self::branch_results`].
    ///
    /// Since 1.41.0.
    pub fn remaining_branch_results(
        &self,
        context: &Context,
        round: &JoinRound,
    ) -> HandlerResult<Vec<T>>
    where
        T: Value,
    {
        self.channel_map
            .condition_results(context, &round.round_key)
    }

    pub(crate) fn name(&self) -> &str {
        self.channel_map.name()
    }

    fn counted_condition(&self, round: &JoinRound, count: usize) -> Condition {
        if let Err(error) = validate_map_instance(&round.round_key) {
            return Condition::invalid(format!(
                "Join {} round key is invalid: {error}",
                self.name()
            ));
        }
        self.channel_map.for_n(&round.round_key, count)
    }
}

impl<T> Clone for Join<T> {
    fn clone(&self) -> Self {
        Self {
            channel_map: self.channel_map.clone(),
        }
    }
}

#[cfg(test)]
#[path = "join_tests.rs"]
mod tests;
