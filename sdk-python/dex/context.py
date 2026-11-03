# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Sustainable Use License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from enum import Enum
from typing import TYPE_CHECKING, Any, Protocol, Sequence, TypeVar

if TYPE_CHECKING:
    from dex.attribute import Attribute, AttributeMap
    from dex.channel import Channel, ChannelMap
    from dex.step import StepOutput
    from dex.stream import Stream

    class _StepOutputFinalizer(Protocol):
        def _finalize_step_output(self) -> None: ...

        def _cancel_step_output(self) -> None: ...


ValueT = TypeVar("ValueT")


class StepAttemptProgress(Enum):
    """Describe how far the attempts of a failed Step method got.

    Dex reports the furthest attempt of the failed method in
    ``RecoveryErrorInfo.attempt_progress``. A recovery Step uses it to tell work
    that never ran from work that may have run. ``ASSIGNED`` means the work did
    not start only for a Step that calls ``AsyncContext.mark_attempt_started``
    before every external effect. For any other Step, treat ``ASSIGNED`` as
    possibly run.

    Attributes:
        UNSPECIFIED: The Dex Server does not report progress. Treat the work as
            possibly run.
        NOT_ASSIGNED: No attempt was sent to a Worker, so the work never ran.
        ASSIGNED: An attempt was sent to a Worker, and no attempt marked
            itself started.
        STARTED: An attempt marked itself started, so its external effect may
            have run.
    """

    UNSPECIFIED = 0
    NOT_ASSIGNED = 1
    ASSIGNED = 2
    STARTED = 3


@dataclass(frozen=True)
class RecoveryErrorInfo:
    """Describe the final failure that selected a recovery path.

    Attributes:
        detail: Failure message supplied by the Worker or backend.
        error_type: Original Worker error type or backend failure type. A
            failed attempt that had marked itself started and lost its Worker
            result to a Dex Server restart reports ``StartedAttemptInterrupted``.
        attempt_progress: How far the failed method's attempts got.
            ``StepAttemptProgress.UNSPECIFIED`` from a Dex Server that does not
            report it.
    """

    detail: str
    error_type: str
    attempt_progress: StepAttemptProgress = StepAttemptProgress.UNSPECIFIED


class Context(Protocol):
    """Expose execution metadata and decision-local Step operations.

    Dex supplies a Context to every ``wait_for`` and ``execute`` call. Do not retain
    it after the handler returns. Attribute and Channel definition methods provide
    the typed persistence API; the methods below expose execution diagnostics and
    per-attempt local state.
    """

    @property
    def flow_id(self) -> str:
        """Return the stable Flow ID shared by all runs.

        Returns:
            The application-assigned Flow ID.
        """
        ...

    @property
    def run_id(self) -> str:
        """Return the current server-assigned run ID.

        Returns:
            The run ID, which changes after time travel or continue-as-new.
        """
        ...

    @property
    def flow_started_at(self) -> datetime:
        """Return when the current Flow run started.

        The server supplies the same value across Steps and handler retries in
        this run. It is available in ``wait_for``, ``execute``, Flow timeout
        handlers, and RPC handlers. This is the run start time, rather than the
        start time of the current Step or attempt.

        Returns:
            A timezone-aware UTC datetime with one-second precision.

        Examples:
            Measure the current run's age inside a handler::

                from datetime import datetime, timezone

                elapsed = datetime.now(timezone.utc) - context.flow_started_at
                elapsed_seconds = elapsed.total_seconds()
        """
        ...

    @property
    def step_execution_id(self) -> str:
        """Return the current Step execution's protocol identifier.

        Returns:
            The Step type and execution number encoded by Dex.
        """
        ...

    @property
    def from_step_execution_id(self) -> str:
        """Return the predecessor Step execution identifier.

        Returns:
            The originating identifier, or an empty string at Flow start.
        """
        ...

    @property
    def recovery_error(self) -> RecoveryErrorInfo | None:
        """Return the final failure that selected this recovery path.

        Returns:
            Recovery information, or ``None`` outside a recovery path.
        """
        ...

    @property
    def first_attempt_at(self) -> datetime:
        """Return when the first attempt of this handler invocation started.

        The value is stable across retries of the same invocation, so a handler
        can derive a deadline that a retry does not reset.

        Returns:
            A timezone-aware UTC time with one-second precision.
        """
        ...

    @property
    def deadline(self) -> datetime | None:
        """Return the current Step method attempt's absolute deadline.

        This includes any shorter remaining RetryPolicy total duration. Use it
        inside ``wait_for``, ``execute``, or a Flow timeout handler to budget
        external work; each retry receives its own effective deadline.

        Returns:
            A timezone-aware UTC datetime with microsecond precision, or ``None``
            for RPC handlers and tasks without a server-supplied deadline.

        Examples:
            Compute the remaining attempt budget in seconds inside a handler::

                from datetime import datetime, timezone

                deadline = context.deadline
                if deadline is not None:
                    remaining_seconds = (deadline - datetime.now(timezone.utc)).total_seconds()
        """
        ...

    @property
    def attempt(self) -> int:
        """Return the one-based handler retry attempt.

        Returns:
            ``1`` for the initial attempt and a larger value after retries.
        """
        ...

    def has_last_heartbeat_value(self) -> bool:
        """Report whether the previous regular attempt persisted heartbeat details.

        Returns:
            ``True`` when a heartbeat Value is present. A persisted Python ``None``
            still counts as present.
        """
        ...

    def get_last_heartbeat_value(
        self,
        value_type: type[ValueT],
    ) -> ValueT | None:
        """Decode the previous regular attempt's heartbeat checkpoint.

        Use :meth:`has_last_heartbeat_value` to distinguish an absent checkpoint
        from a persisted Python ``None``.

        Args:
            value_type: The expected Python type and codec selection.

        Returns:
            The decoded checkpoint, or ``None`` when no Value is present.

        Raises:
            ValueMappingError: If the Value cannot be decoded as ``value_type``.
        """
        ...

    def has_timer_fired(self, index: int | None = None) -> bool:
        """Report whether a Timer made the current Wait ready.

        Args:
            index: Optional zero-based Timer index; ``None`` checks any Timer.

        Returns:
            ``True`` when the selected Timer fired for this execution.
        """
        ...

    def wait_for_method_failed(self) -> bool:
        """Report whether ``wait_for`` failed before this ``execute`` call.

        Returns:
            ``True`` when the Step's failure policy proceeded after a wait error.
        """
        ...

    def is_cancellation_requested(self) -> bool:
        """Report whether Dex canceled the active handler call.

        Async handlers are normally canceled by their task. Synchronous handlers cannot be
        forcefully interrupted by Python; long CPU-bound work may check this at natural
        boundaries. External effects still require idempotency or compensation.

        Returns:
            ``True`` after the Worker gRPC call has been canceled.
        """
        ...

    def set_step_execution_local(self, key: str, value: object) -> None:
        """Store process-local data for this Step execution.

        Local data is not persisted, replicated, or available after worker restart;
        use Attributes for durable state.

        Args:
            key: A non-empty key scoped to the Step execution.
            value: The in-memory object to retain by reference.
        """
        ...

    def get_step_execution_local(
        self, key: str, value_type: type[ValueT]
    ) -> ValueT | None:
        """Return typed process-local data for this Step execution.

        Args:
            key: The key passed to ``set_step_execution_local``.
            value_type: The required runtime type.

        Returns:
            The stored object, or ``None`` when the key is absent.

        Raises:
            TypeError: If the stored object is not an instance of ``value_type``.
        """
        ...

    def record_event(self, name: str, value: object) -> None:
        """Stage an application event in the current handler result.

        Args:
            name: The non-empty event name used for diagnostics.
            value: A codec-supported event payload.
        """
        ...

    def _get_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
    ) -> ValueT: ...

    def _set_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None: ...

    def _delete_attribute(
        self,
        definition: Attribute[object] | AttributeMap[object],
        instance: str | None,
    ) -> None: ...

    def _attribute_map_keys(
        self,
        definition: AttributeMap[object],
    ) -> tuple[str, ...]: ...

    def _publish_channel(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None: ...

    def _delete_channel_message(
        self,
        definition: Channel[Any] | ChannelMap[Any],
        instance: str | None,
        message_id: str,
    ) -> None: ...

    def _channel_size(
        self,
        definition: Channel[object] | ChannelMap[object],
        instance: str | None,
    ) -> int: ...

    def _pending_channel_messages(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[object]: ...

    def _channel_results(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[ValueT]: ...

    def _channel_map_keys(
        self,
        definition: ChannelMap[object],
    ) -> tuple[str, ...]: ...

    def _write_stream(
        self,
        definition: Stream[ValueT],
        value: ValueT,
    ) -> StepOutput | None: ...

    def _prepare_buffered_stream(self, definition: Stream[object]) -> bool: ...

    def _register_step_output_finalizer(
        self,
        finalizer: _StepOutputFinalizer,
    ) -> None: ...


class AsyncContext(Context, Protocol):
    """Expose Context operations available to asynchronous Step handlers.

    AsyncWorker supplies this protocol to coroutine ``wait_for`` and ``execute``
    methods. Stream writes enqueue synchronously; heartbeat waits only for the
    Worker output queue and never for Dex Stream Store persistence.
    """

    async def heartbeat(self, value: object = ...) -> None:
        """Emit one heartbeat from the current asynchronous Step attempt.

        Calling this method without ``value`` clears prior persisted details.
        Passing any value, including Python ``None``, persists a present Value.
        Local activity execution ignores heartbeat frames.

        Args:
            value: Optional codec-supported checkpoint value.

        Raises:
            asyncio.CancelledError: If the active Worker invocation is canceled.
            ValueError: If called outside a Step handler.
            ValueMappingError: If ``value`` cannot be encoded.
        """
        ...

    async def mark_attempt_started(self) -> None:
        """Record durably that this attempt is about to start its external effect.

        Call this method right before an external effect that must not run
        twice, such as a payment or an email. It returns only after Dex has
        committed the mark. From then on, Dex never delivers this attempt
        again at the same attempt number. When the Worker dies, or a Dex Server
        node restarts or fails over, the attempt fails instead. Its retry
        policy then applies, and after the last attempt the failure route
        receives ``RecoveryErrorInfo.attempt_progress`` set to
        ``StepAttemptProgress.STARTED``. An attempt that fails before the mark
        reports ``NOT_ASSIGNED`` or ``ASSIGNED``, so a failure route can tell
        that the effect never started.

        Time travel is the exception. Time travel to a point before the mark
        runs the attempt again in the new run attempt, like every Step after
        that point.

        The call costs one commit round trip. Call it once per attempt. A call
        made while another one waits for its acknowledgement waits for the same
        acknowledgement. A later call commits again and has no further effect.
        A heartbeat does not replace it, because Dex does not acknowledge
        heartbeats.

        Example:
            ```python
            class ChargeCard(Step[str]):
                async def execute(self, context: AsyncContext, input: str) -> StepDecision:
                    await context.mark_attempt_started()
                    await payments.charge(input, idempotency_key=context.step_execution_id)
                    return dead_end()
            ```

        Raises:
            AttemptStartedNotAcknowledgedError: If the Dex Server does not
                support the mark (``is_unsupported_server`` is ``True``), or the
                Worker task was canceled or its stream closed before Dex
                acknowledged the mark. Do not start the effect after this error.
            asyncio.CancelledError: If the handler task itself is canceled
                while it waits.
            ValueError: If called outside an asynchronous Step handler, such as
                in an RPC or in a Step run by a synchronous ``Worker``.
        """
        ...
