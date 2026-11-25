# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from datetime import datetime
from typing import TYPE_CHECKING, Any, Sequence, TypeVar, assert_type

from dex import (
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
    Context,
    RecoveryErrorInfo,
    StepOutput,
    Stream,
    is_graceful_completion_condition_completed,
    supports_graceful_completion_condition,
)

if TYPE_CHECKING:
    from dex.context import _StepOutputFinalizer

ValueT = TypeVar("ValueT")


# CI type-checks this file: an application Context built from the members published
# before the graceful completion condition must still satisfy Context.
class ApplicationFakeContext:
    @property
    def flow_id(self) -> str:
        return "fake-flow"

    @property
    def run_id(self) -> str:
        return "fake-run"

    @property
    def flow_started_at(self) -> datetime:
        return datetime(2026, 1, 1)

    @property
    def step_execution_id(self) -> str:
        return "fake-step"

    @property
    def from_step_execution_id(self) -> str:
        return ""

    @property
    def recovery_error(self) -> RecoveryErrorInfo | None:
        return None

    @property
    def first_attempt_at(self) -> datetime:
        return datetime(2026, 1, 1)

    @property
    def deadline(self) -> datetime | None:
        return None

    @property
    def attempt(self) -> int:
        return 1

    def has_last_heartbeat_value(self) -> bool:
        return False

    def get_last_heartbeat_value(self, value_type: type[ValueT]) -> ValueT | None:
        return None

    def has_timer_fired(self, index: int | None = None) -> bool:
        return False

    def wait_for_method_failed(self) -> bool:
        return False

    def is_cancellation_requested(self) -> bool:
        return False

    def set_step_execution_local(self, key: str, value: object) -> None:
        pass

    def get_step_execution_local(
        self, key: str, value_type: type[ValueT]
    ) -> ValueT | None:
        return None

    def record_event(self, name: str, value: object) -> None:
        pass

    def _get_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
    ) -> ValueT:
        raise NotImplementedError

    def _set_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None:
        pass

    def _delete_attribute(
        self,
        definition: Attribute[object] | AttributeMap[object],
        instance: str | None,
    ) -> None:
        pass

    def _attribute_map_keys(self, definition: AttributeMap[object]) -> tuple[str, ...]:
        return ()

    def _publish_channel(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None:
        pass

    def _delete_channel_message(
        self,
        definition: Channel[Any] | ChannelMap[Any],
        instance: str | None,
        message_id: str,
    ) -> None:
        pass

    def _channel_size(
        self,
        definition: Channel[object] | ChannelMap[object],
        instance: str | None,
    ) -> int:
        return 0

    def _pending_channel_messages(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[object]:
        return ()

    def _channel_results(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[ValueT]:
        return ()

    def _channel_map_keys(self, definition: ChannelMap[object]) -> tuple[str, ...]:
        return ()

    def _begin_join_fork(self, definition: ChannelMap[Any]) -> str:
        return ""

    def _write_stream(
        self, definition: Stream[ValueT], value: ValueT
    ) -> StepOutput | None:
        return None

    def _prepare_buffered_stream(self, definition: Stream[object]) -> bool:
        return False

    def _register_step_output_finalizer(self, finalizer: _StepOutputFinalizer) -> None:
        pass


def test_graceful_completion_helpers_return_false_for_an_application_context() -> None:
    context: Context = ApplicationFakeContext()

    assert assert_type(supports_graceful_completion_condition(context), bool) is False
    assert (
        assert_type(is_graceful_completion_condition_completed(context), bool) is False
    )
