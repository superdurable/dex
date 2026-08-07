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

ValueT = TypeVar("ValueT")


class Context(Protocol):
    @property
    def flow_id(self) -> str: ...

    @property
    def run_id(self) -> str: ...

    @property
    def step_execution_id(self) -> str: ...

    @property
    def from_step_execution_id(self) -> str: ...

    @property
    def attempt(self) -> int: ...

    def has_timer_fired(self, index: int | None = None) -> bool: ...

    def wait_for_method_failed(self) -> bool: ...

    def set_step_execution_local(self, key: str, value: object) -> None: ...

    def get_step_execution_local(
        self, key: str, value_type: type[ValueT]
    ) -> ValueT | None: ...

    def record_event(self, name: str, value: object) -> None: ...

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
