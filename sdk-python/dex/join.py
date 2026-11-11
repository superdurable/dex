# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable, Generic, Sequence, TypeVar

from dex.channel import ChannelMap
from dex.condition import Condition
from dex.context import Context
from dex.step import StepDecision, StepMovement, dead_end

ResultT = TypeVar("ResultT")


@dataclass(frozen=True)
class JoinRound:
    """Identify one round of a :class:`Join` and the number of its branches.

    :meth:`Join.fork` and :meth:`Join.fork_with_round_key` create a JoinRound and
    pass the same value to every branch builder and to the join builder. Put it in
    the input of each branch Step, of the join Step, and of the next round's split
    Step. Treat it as an opaque token: do not build one by hand, and do not change
    its fields.

    A JoinRound is part of persisted Step input. Its JSON form is
    ``{"roundKey": string, "branchCount": integer}`` in every Dex SDK, so a Step
    written in one SDK can hand it to a Step in another SDK.

    .. versionadded:: 1.44.0

    Attributes:
        round_key: The ChannelMap instance key that holds this round's branch
            results. :meth:`Join.fork` uses the split Step's execution ID, such as
            ``"RequestQuotes-2"``. It is non-empty and contains no ``/``.
        branch_count: The number of branches the fork started. It is zero or
            greater.

    Examples:
        A branch Step whose whole input is the JoinRound::

            class QuoteFromSupplierA(Step[JoinRound]):
                def execute(self, context: Context, input: JoinRound) -> StepDecision:
                    return QUOTE_RECEIVED.end_branch(context, input, "A")
    """

    round_key: str = field(metadata={"dex_json_name": "roundKey"})
    branch_count: int = field(metadata={"dex_json_name": "branchCount"})


class Join(Generic[ResultT]):
    """Synchronize parallel branches of a Flow and collect one result from each branch.

    Use a Join when one Step starts several branches and a later Step must wait for
    all of them, the first of them, or ``n`` of them. The Join counts the branches
    and chooses a separate round key for every fork, so the application keeps no
    branch counter and no round number.

    A Join is a :class:`~dex.ChannelMap` with the same name. Each branch result is
    one ordinary ChannelMap message of type ``result_type``, stored in the instance
    named by the round key. Dex Web and Client tools show the Join as that
    ChannelMap. Register the Join in the Flow's ``PersistenceSchema``. Its name
    must differ from every other Attribute, Channel, ChannelMap, and Stream of the
    Flow.

    How to use it:

    1. The split Step calls :meth:`fork` in ``execute``. It passes one builder per
       branch and one builder for the join Step. Each builder receives the same
       :class:`JoinRound` and returns a :class:`~dex.StepMovement`. Return the
       movements with :func:`~dex.go_to_many`.
    2. Each branch reports exactly one result per round. Its last Step calls
       :meth:`end_branch`, or :meth:`report_branch_result` when it continues
       after reporting.
    3. The join Step waits with :meth:`for_all_branches`, :meth:`for_first_branch`,
       or :meth:`for_branches`, and reads the results with :meth:`branch_results`.
    4. When the Flow forks the same Join again in a loop, pass the previous
       JoinRound to the next split Step. Its ``wait_for`` waits with
       :meth:`for_remaining_branches` to consume the results that arrived after
       the join fired. This reset also blocks the next round until every branch of
       the previous round has reported.

    The condition methods return ordinary conditions. Combine them with other
    conditions in :meth:`~dex.Wait.all_of`, :meth:`~dex.Wait.any_of`, and
    :meth:`~dex.Wait.any_combination_of`. For example, ``Wait.any_of(
    join.for_all_branches(round), Timer.by_duration(timedelta(hours=1)))`` gives
    the join a deadline.

    Rules and traps:

    - Each branch reports exactly one result in each round. A branch that reports
      twice is counted twice. A branch that never reports, for example because its
      failure policy moves it to a recovery Step that does not report, keeps an
      all-branches join waiting forever. Make recovery Steps report a result that
      means failure, or give the join a Timer deadline.
    - Branches only pass the JoinRound to :meth:`end_branch` or
      :meth:`report_branch_result`. They must not wait on a Join condition.
    - Only the Step built by the join builder waits on the round. Two Steps that
      wait on one round split its results between them.
    - One Step invocation forks one Join at most once. A second :meth:`fork` of
      the same Join in the same invocation raises ``ValueError``. Use a second
      Join, or :meth:`fork_with_round_key` with a different key.
    - An RPC handler has no Step execution ID, so :meth:`fork` raises
      ``ValueError`` there. Use :meth:`fork_with_round_key`.
    - The ``received_count`` of :meth:`for_remaining_branches` must equal the
      number of results the join consumed: ``1`` after :meth:`for_first_branch`,
      ``count`` after :meth:`for_branches`, and ``branch_count`` after
      :meth:`for_all_branches`.
    - Without a reset, results that arrive after the join stay pending in the
      round's instance until the Flow ends. They never count toward another round.
      A Flow that forks in a loop should reset every round, except the round of
      a canceling join.
    - A canceling join returns its decision with
      :meth:`~dex.StepDecision.with_canceling_sibling_steps` and the branch Step
      types. A canceled branch does not report. Sibling selection matches only the
      first Step of each branch. When :meth:`fork_with_round_key` runs in an RPC,
      every Step started by any call of that RPC is a sibling, so cancel those
      branches explicitly with :meth:`~dex.StepDecision.with_canceling_steps`
      while only one round is active.
    - A canceled branch never reports, and no other code can safely report for
      it. Dex decides at commit time whether a cancellation takes effect, so the
      code that cancels a branch cannot know whether the branch already
      reported. If it did, the cancellation does nothing, and an extra report
      counts that branch twice, so an all-branches join can fire while another
      branch has not reported. To keep an all-branches join from waiting
      forever for a canceled branch, cancel the join Step together with its
      branches, or wait on :meth:`~dex.Wait.any_of` with the join condition and a
      Timer and handle the timeout in ``execute``.
    - Never reset a canceling join's round with :meth:`for_remaining_branches`:
      it waits for results that canceled branches never report. The next round
      forks a new round without the reset wait, as the first round does. The
      canceled round's late results stay pending until the Flow ends.
    - When a join Step is canceled after its wait matched but before its
      ``execute`` committed, what happens to the results that wait consumed
      depends on the server version. A server that requeues consumed messages
      on cancellation returns them to the round, so a new join for the round
      sees them. An older server drops them, and a new join waits forever. See
      the server change log at https://docs.superdurable.io/reference/server/changelog.

    .. versionadded:: 1.44.0

    Examples:
        A two-round partial join. Each round accepts the first two of three
        quotes, and the next round resets the previous one::

            from dataclasses import dataclass

            from dex import (
                Channel, Context, Flow, Join, JoinRound, PersistenceSchema, Step,
                StepDecision, StepList, StepMovement, Wait, go_to_many,
                graceful_complete, rpc,
            )

            QUOTE_RECEIVED = Join("quoteReceived", str)
            SUPPLIER_RELEASED = Channel("supplierReleased", str)
            REQUIRED_QUOTES = 2

            @dataclass
            class QuoteRequest:
                round_number: int
                previous_round: JoinRound | None = None

            @dataclass
            class QuoteJoinInput:
                request: QuoteRequest
                join_round: JoinRound

            class RequestQuotes(Step[QuoteRequest]):
                def wait_for(self, context: Context, input: QuoteRequest) -> Wait:
                    if input.previous_round is None:
                        return Wait.skip_immediately()
                    return Wait.until(
                        QUOTE_RECEIVED.for_remaining_branches(
                            input.previous_round, REQUIRED_QUOTES
                        )
                    )

                def execute(self, context: Context, input: QuoteRequest) -> StepDecision:
                    if input.previous_round is not None:
                        late_quotes = QUOTE_RECEIVED.remaining_branch_results(
                            context, input.previous_round
                        )
                        context.record_event("lateQuotes", list(late_quotes))
                    movements = QUOTE_RECEIVED.fork(
                        context,
                        [
                            lambda join_round: StepMovement.of(QuoteFromSupplier, join_round),
                            lambda join_round: StepMovement.of(QuoteFromSupplier, join_round),
                            lambda join_round: StepMovement.of(QuoteFromSupplier, join_round),
                        ],
                        lambda join_round: StepMovement.of(
                            AcceptQuotes, QuoteJoinInput(input, join_round)
                        ),
                    )
                    return go_to_many(*movements)

            class QuoteFromSupplier(Step[JoinRound]):
                def wait_for(self, context: Context, input: JoinRound) -> Wait:
                    return Wait.until(SUPPLIER_RELEASED.for_one())

                def execute(self, context: Context, input: JoinRound) -> StepDecision:
                    supplier = SUPPLIER_RELEASED.results(context)[0]
                    return QUOTE_RECEIVED.end_branch(context, input, supplier)

            class AcceptQuotes(Step[QuoteJoinInput]):
                def wait_for(self, context: Context, input: QuoteJoinInput) -> Wait:
                    return Wait.until(
                        QUOTE_RECEIVED.for_branches(input.join_round, REQUIRED_QUOTES)
                    )

                def execute(self, context: Context, input: QuoteJoinInput) -> StepDecision:
                    accepted = QUOTE_RECEIVED.branch_results(context, input.join_round)
                    if input.request.round_number == 2:
                        return graceful_complete(list(accepted))
                    next_request = QuoteRequest(
                        input.request.round_number + 1, input.join_round
                    )
                    return go_to_many(StepMovement.of(RequestQuotes, next_request))

            class QuoteFlow(Flow[QuoteRequest]):
                @rpc
                def release_supplier(self, context: Context, supplier: str) -> None:
                    SUPPLIER_RELEASED.publish(context, supplier)

                def get_steps(self) -> StepList[QuoteRequest]:
                    return StepList.start_step(RequestQuotes()).other_steps(
                        QuoteFromSupplier(), AcceptQuotes()
                    )

                def get_persistence_schema(self) -> PersistenceSchema:
                    return PersistenceSchema.of(QUOTE_RECEIVED, SUPPLIER_RELEASED)

        A run-time number of branches. Map the inputs to builders with a helper
        function, which binds each input; a lambda written directly in the
        comprehension would see only the last input::

            from dataclasses import dataclass
            from typing import Callable

            from dex import (
                Context, Flow, Join, JoinRound, PersistenceSchema, Step,
                StepDecision, StepList, StepMovement, Wait, go_to_many,
                graceful_complete,
            )

            REVIEW_DONE = Join("reviewDone", str)

            @dataclass
            class ReviewInput:
                reviewer: str
                join_round: JoinRound

            def build_review(reviewer: str) -> Callable[[JoinRound], StepMovement[ReviewInput]]:
                return lambda join_round: StepMovement.of(
                    ReviewDocument, ReviewInput(reviewer, join_round)
                )

            class RequestReviews(Step[list[str]]):
                def execute(self, context: Context, input: list[str]) -> StepDecision:
                    movements = REVIEW_DONE.fork(
                        context,
                        [build_review(reviewer) for reviewer in input],
                        lambda join_round: StepMovement.of(CollectReviews, join_round),
                    )
                    return go_to_many(*movements)

            class ReviewDocument(Step[ReviewInput]):
                def execute(self, context: Context, input: ReviewInput) -> StepDecision:
                    return REVIEW_DONE.end_branch(
                        context, input.join_round, f"approved by {input.reviewer}"
                    )

            class CollectReviews(Step[JoinRound]):
                def wait_for(self, context: Context, input: JoinRound) -> Wait:
                    return Wait.until(REVIEW_DONE.for_all_branches(input))

                def execute(self, context: Context, input: JoinRound) -> StepDecision:
                    return graceful_complete(list(REVIEW_DONE.branch_results(context, input)))

            class ReviewFlow(Flow[list[str]]):
                def get_steps(self) -> StepList[list[str]]:
                    return StepList.start_step(RequestReviews()).other_steps(
                        ReviewDocument(), CollectReviews()
                    )

                def get_persistence_schema(self) -> PersistenceSchema:
                    return PersistenceSchema.of(REVIEW_DONE)
    """

    def __init__(self, name: str, result_type: type[ResultT]) -> None:
        """Define a Join whose branch results use the ChannelMap named ``name``.

        Define each Join once, for example as a module constant, and register that
        same object in the Flow's ``PersistenceSchema``.

        Args:
            name: The durable Join name. It is also the ChannelMap name, so it is
                non-empty, contains no ``/``, and is unique within the Flow.
                Renaming it orphans the results of running Flows.
            result_type: The Python type of every branch result. Changing it
                must keep already published results decodable.

        Raises:
            ValueError: If ``name`` is empty or contains ``/``.
        """
        self._channel_map: ChannelMap[ResultT] = ChannelMap(name, result_type)

    @property
    def name(self) -> str:
        """Return the durable Join name.

        Returns:
            The name passed to the constructor, which is also the name of the
            underlying ChannelMap.
        """
        return self._channel_map.name

    @property
    def result_type(self) -> type[ResultT]:
        """Return the Python type of every branch result.

        Returns:
            The result type passed to the constructor.
        """
        return self._channel_map.value_type

    @property
    def channel_map(self) -> ChannelMap[ResultT]:
        """Return the ChannelMap that holds this Join's branch results.

        Use it to inspect a round's pending results with the existing ChannelMap
        API. The instance key of a round is its :attr:`JoinRound.round_key`. For
        example, an RPC declares ``@rpc(load_channel_maps=(join.channel_map,))``
        and then reads ``join.channel_map.pending_messages(context,
        join_round.round_key)``, or reads the count with ``size``. The returned
        object is the definition that ``PersistenceSchema.of`` registers for this
        Join, so every API that checks registration accepts it.

        Never publish to it, delete from it, or wait on it directly. Report
        results with :meth:`end_branch` or :meth:`report_branch_result`, and wait
        with the Join condition methods. A direct publication counts as a branch
        result and breaks the exactly-once count of the round.

        .. versionadded:: 1.44.0

        Returns:
            The registered ChannelMap with the Join's name and result type.
        """
        return self._channel_map

    def fork(
        self,
        context: Context,
        branch_builders: Sequence[Callable[[JoinRound], StepMovement[Any]]],
        join_builder: Callable[[JoinRound], StepMovement[Any]],
    ) -> tuple[StepMovement[Any], ...]:
        """Start a new round and build the movements of its branches and its join Step.

        Call it in the split Step's ``execute``. The round key is the current Step
        execution ID, which stays the same across retries and is unique within the
        run. The branch count is ``len(branch_builders)``. The method builds the
        JoinRound first, then calls each branch builder in order, then the join
        builder. It makes no server call.

        An empty ``branch_builders`` creates a round with zero branches.
        :meth:`for_all_branches` on that round completes immediately.

        Args:
            context: The current Step ``execute`` Context.
            branch_builders: One builder per branch. Each receives the JoinRound
                and returns the movement that starts its branch.
            join_builder: Receives the JoinRound and returns the movement of the
                Step that waits for the branches.

        Returns:
            The branch movements in builder order, followed by the join movement.
            Pass them to :func:`~dex.go_to_many`, optionally with other movements.

        Raises:
            ValueError: If the Context is not a Step ``execute`` Context, for
                example in an RPC handler, if this invocation already forked this
                Join, or if the Join is not registered with the current Flow.
        """
        round_key = context._begin_join_fork(self._channel_map)
        return self._build_round_movements(round_key, branch_builders, join_builder)

    def fork_with_round_key(
        self,
        round_key: str,
        branch_builders: Sequence[Callable[[JoinRound], StepMovement[Any]]],
        join_builder: Callable[[JoinRound], StepMovement[Any]],
    ) -> tuple[StepMovement[Any], ...]:
        """Start a round with an application round key and build its movements.

        Use it in an RPC handler, which has no Step execution ID, or to keep the
        instance keys of an existing hand-written ChannelMap join. Every round of
        this Join that can be active at the same time needs a distinct key. Reusing
        a key mixes the results of two rounds. Otherwise it behaves like
        :meth:`fork`.

        Args:
            round_key: The ChannelMap instance key for this round. It must be
                non-empty and must not contain ``/``.
            branch_builders: One builder per branch. Each receives the JoinRound
                and returns the movement that starts its branch.
            join_builder: Receives the JoinRound and returns the movement of the
                Step that waits for the branches.

        Returns:
            The branch movements in builder order, followed by the join movement.
            Return them with ``go_to_many`` or as RPC next Steps.

        Raises:
            ValueError: If ``round_key`` is empty or contains ``/``.
        """
        if not round_key.strip() or "/" in round_key:
            raise ValueError(
                f"Join {self.name} round key must be non-empty and must not contain '/'"
            )
        return self._build_round_movements(round_key, branch_builders, join_builder)

    def end_branch(
        self, context: Context, join_round: JoinRound, result: ResultT
    ) -> StepDecision:
        """Report this branch's result and end the branch.

        Return its value from the last Step of a branch. The result commits with
        the Step's decision. A failed attempt reports nothing, and a retry does
        not report twice.

        Args:
            context: The current Step Context.
            join_round: The JoinRound this branch received.
            result: This branch's result.

        Returns:
            A dead-end decision. Other paths of the Flow continue.
        """
        self.report_branch_result(context, join_round, result)
        return dead_end()

    def report_branch_result(
        self, context: Context, join_round: JoinRound, result: ResultT
    ) -> None:
        """Report a branch result without ending the current Step path.

        Use it when a branch continues after it reports, for example when a
        multi-Step branch reports from a Step other than its last. An RPC can
        report a branch's result when that result arrives through the RPC and no
        Step of that branch reports it, for example for a handoff branch in a
        round the RPC forked with :meth:`fork_with_round_key`. The result
        commits only when the handler's result commits. Never report for a
        canceled branch; see the :class:`Join` rules.

        Args:
            context: The current Step or RPC Context.
            join_round: The JoinRound of the branch being reported.
            result: The branch result.

        Raises:
            ValueError: If the JoinRound is invalid or the Join is not registered
                with the current Flow.
        """
        self._require_valid_round(join_round)
        self._channel_map.publish(context, join_round.round_key, result)

    def for_all_branches(
        self, join_round: JoinRound, *, condition_id: str | None = None
    ) -> Condition:
        """Create a condition that waits for a result from every branch of a round.

        It consumes ``branch_count`` results. A round with zero branches completes
        immediately.

        Args:
            join_round: The round to wait for.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition for the join Step's ``wait_for``.

        Raises:
            ValueError: If the JoinRound is invalid.
        """
        self._require_valid_round(join_round)
        return self._channel_map.for_n(
            join_round.round_key, join_round.branch_count, condition_id=condition_id
        )

    def for_first_branch(
        self, join_round: JoinRound, *, condition_id: str | None = None
    ) -> Condition:
        """Create a condition that waits for the first result of a round.

        It consumes exactly one result, the earliest committed one. Results that
        were already pending stay for :meth:`for_remaining_branches`. Put the
        winner's identity in the result when the join needs it. A round with
        zero branches raises ``ValueError``, because the wait would never
        complete. It behaves like ``for_branches(join_round, 1)``.

        Args:
            join_round: The round to wait for. It needs at least one branch.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition for the join Step's ``wait_for``.

        Raises:
            ValueError: If the JoinRound is invalid or has no branches.
        """
        return self.for_branches(join_round, 1, condition_id=condition_id)

    def for_branches(
        self, join_round: JoinRound, count: int, *, condition_id: str | None = None
    ) -> Condition:
        """Create a condition that waits for ``count`` results of a round.

        It consumes exactly ``count`` results in commit order. Use it for a
        partial join that continues after ``count`` of the branches.

        Args:
            join_round: The round to wait for.
            count: The number of results to wait for, from 1 to ``branch_count``.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition for the join Step's ``wait_for``.

        Raises:
            ValueError: If the JoinRound is invalid or ``count`` is outside
                ``[1, branch_count]``.
        """
        self._require_valid_round(join_round)
        if not 1 <= count <= join_round.branch_count:
            raise ValueError(
                f"Join {self.name} branch count {count} must be between 1 and "
                f"{join_round.branch_count}"
            )
        return self._channel_map.for_n(
            join_round.round_key, count, condition_id=condition_id
        )

    def branch_results(
        self, context: Context, join_round: JoinRound
    ) -> Sequence[ResultT]:
        """Return the results that the join Step's wait consumed for a round.

        Args:
            context: The join Step's ``execute`` Context.
            join_round: The round the Step waited for.

        Returns:
            The consumed results in commit order. The sequence is empty when the
            Step did not wait on this round or its condition did not win.
        """
        return self._channel_map.results(context, join_round.round_key)

    def for_remaining_branches(
        self,
        join_round: JoinRound,
        received_count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create a condition that waits for and consumes the rest of a round's results.

        This is the reset of a round. Use it in the next round's split Step
        ``wait_for``. It waits for ``branch_count - received_count`` results, so
        the next round starts only after every branch of the previous round has
        reported. When nothing remains, it completes immediately.

        Args:
            join_round: The previous round.
            received_count: The number of results the previous join consumed,
                from 0 to ``branch_count``. Pass the same count the join waited
                for, not a counter.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition for the next split Step's ``wait_for``.

        Raises:
            ValueError: If the JoinRound is invalid or ``received_count`` is
                outside ``[0, branch_count]``.
        """
        self._require_valid_round(join_round)
        if not 0 <= received_count <= join_round.branch_count:
            raise ValueError(
                f"Join {self.name} received count {received_count} must be between "
                f"0 and {join_round.branch_count}"
            )
        return self._channel_map.for_n(
            join_round.round_key,
            join_round.branch_count - received_count,
            condition_id=condition_id,
        )

    def remaining_branch_results(
        self, context: Context, join_round: JoinRound
    ) -> Sequence[ResultT]:
        """Return the late results that the reset of a round consumed.

        Args:
            context: The ``execute`` Context of the Step that waited with
                :meth:`for_remaining_branches`.
            join_round: The previous round.

        Returns:
            The consumed late results in commit order. The sequence is empty when
            nothing remained or the Step did not wait on this round.
        """
        return self._channel_map.results(context, join_round.round_key)

    def _build_round_movements(
        self,
        round_key: str,
        branch_builders: Sequence[Callable[[JoinRound], StepMovement[Any]]],
        join_builder: Callable[[JoinRound], StepMovement[Any]],
    ) -> tuple[StepMovement[Any], ...]:
        join_round = JoinRound(round_key, len(branch_builders))
        branch_movements = tuple(builder(join_round) for builder in branch_builders)
        return (*branch_movements, join_builder(join_round))

    def _require_valid_round(self, join_round: JoinRound) -> None:
        if not join_round.round_key.strip() or "/" in join_round.round_key:
            raise ValueError(
                f"Join {self.name} round key must be non-empty and must not contain '/'"
            )
        if join_round.branch_count < 0:
            raise ValueError(
                f"Join {self.name} branch count {join_round.branch_count} must not be negative"
            )
