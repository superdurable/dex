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
    """Describe the furthest progress across attempts of a failed Step method.

    Inspect ``Context.recovery_error.attempt_progress`` before deciding whether
    recovery may safely repeat a side effect. Only ``NOT_STARTED`` guarantees
    the handler did not run. Other values require idempotent recovery or an
    application check for completed side effects.

    .. versionadded:: 1.35.0

    Attributes:
        UNSPECIFIED: An older Server or a failure recorded before upgrade supplied
            no progress. Treat it like
            ``NOT_COMPLETED`` because an attempt may have run.
        NOT_STARTED: The handler did not run in any attempt.
        NOT_COMPLETED: Some attempt may have run, but none completed.

    Examples:
        >>> from dex import RecoveryErrorInfo, StepAttemptProgress
        >>> error = RecoveryErrorInfo("unavailable", "dispatch", StepAttemptProgress.NOT_STARTED)
        >>> can_repeat_side_effect = error.attempt_progress is StepAttemptProgress.NOT_STARTED
    """

    UNSPECIFIED = "unspecified"
    NOT_STARTED = "not_started"
    NOT_COMPLETED = "not_completed"


@dataclass(frozen=True)
class RecoveryErrorInfo:
    """Describe the final failure that selected a recovery path.

    A Worker disconnect or crash after hand-out can leave external effects even
    when Dex recorded no completion. Treat NOT_COMPLETED and UNSPECIFIED as
    possibly run. Only NOT_STARTED permits assuming no handler attempt ran.

    .. versionchanged:: 1.35.0
       Added attempt_progress, defaulting to UNSPECIFIED for source compatibility
       and failures recorded without progress information.

    Attributes:
        detail: Failure message supplied by the Worker or backend.
        error_type: Original Worker error type or backend failure type.
        attempt_progress: Furthest progress across all attempts of this Step
            method. Defaults to ``StepAttemptProgress.UNSPECIFIED`` for older
            records and for construction without progress. Treat that default
            like ``NOT_COMPLETED``. Only ``NOT_STARTED`` guarantees the handler
            did not run. See ``StepAttemptProgress`` for a recovery example.
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
            The current run ID. Time travel or a new run changes it; the Flow ID
            remains stable across runs.
        """
        ...

    @property
    def flow_started_at(self) -> datetime:
        """Return when the current Flow run started.

        The server supplies the same value across Steps and handler retries in
        this run. It is available in ``wait_for``, ``execute``, Flow timeout
        handlers, and RPC handlers. This is the run start time, rather than the
        start time of the current Step or attempt.

        .. versionadded:: 1.28.0

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
            The Step type and execution number encoded by Dex. Retries of the
            same Step execution share it; use attempt to distinguish retries
            and run_id to distinguish runs.
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

        RetryPolicy total duration does not shorten a running attempt. Use this
        inside ``wait_for``, ``execute``, or a Flow timeout handler to budget
        external work; each retry receives its own effective deadline.

        The deadline is hand-out time plus the method timeout. Waiting for locks
        or a Worker does not count. Do not persist it as a retry checkpoint;
        each retry must use its newly supplied deadline.

        .. versionadded:: 1.28.0

        .. versionchanged:: 1.35.0
           Retry total duration no longer shortens the attempt deadline.

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
        Heartbeat frames report progress in both durability modes.

        Args:
            value: Optional codec-supported checkpoint value.

        Raises:
            asyncio.CancelledError: If the active Worker invocation is canceled.
            ValueError: If called outside a Step handler.
            ValueMappingError: If ``value`` cannot be encoded.
        """
        ...
