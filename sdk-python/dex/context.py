# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from typing import TYPE_CHECKING, Protocol, Sequence, TypeVar

if TYPE_CHECKING:
    from dex.attribute import Attribute, AttributeMap
    from dex.channel import Channel, ChannelMap
    from dex.stream import Stream

ValueT = TypeVar("ValueT")


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
    def attempt(self) -> int:
        """Return the one-based handler retry attempt.

        Returns:
            ``1`` for the initial attempt and a larger value after retries.
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

    def _channel_size(
        self,
        definition: Channel[object] | ChannelMap[object],
        instance: str | None,
    ) -> int: ...

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
    ) -> object: ...
