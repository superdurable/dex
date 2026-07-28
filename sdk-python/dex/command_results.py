# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import typing
from dataclasses import dataclass
from typing import Any, Union, Optional

from dex.errors import WorkflowDefinitionError, NotRegisteredError
from dex.dex_api.models import (
    ChannelRequestStatus,
    CommandResults as IdlCommandResults,
    TimerStatus,
)
from dex.dex_api.types import Unset
from dex.object_encoder import ObjectEncoder
from dex.type_store import TypeStore


@dataclass
class TimerCommandResult:
    status: TimerStatus
    command_id: str


@dataclass
class InternalChannelCommandResult:
    channel_name: str
    value: Any
    status: ChannelRequestStatus
    command_id: str


@dataclass
class SignalChannelCommandResult:
    channel_name: str
    value: Any
    status: ChannelRequestStatus
    command_id: str


@dataclass
class CommandResults:
    timer_commands: list[TimerCommandResult]
    internal_channel_commands: list[InternalChannelCommandResult]
    signal_channel_commands: list[SignalChannelCommandResult]
    wait_until_api_succeeded: Optional[bool] = None


def from_idl_command_results(
    idl_results: Union[Unset, IdlCommandResults],
    internal_channel_types: TypeStore,
    signal_channel_types: dict[str, typing.Optional[type]],
    object_encoder: ObjectEncoder,
) -> CommandResults:
    results = CommandResults(list(), list(), list(), None)
    if isinstance(idl_results, Unset):
        return results

    if not isinstance(idl_results.timer_results, Unset):
        for timer in idl_results.timer_results:
            results.timer_commands.append(
                TimerCommandResult(timer.timer_status, timer.command_id)
            )

    if not isinstance(idl_results.inter_state_channel_results, Unset):
        for inter in idl_results.inter_state_channel_results:

            try:
                val_type = internal_channel_types.get_type(inter.channel_name)
            except NotRegisteredError as exception:
                raise WorkflowDefinitionError(
                    "internal channel is not registered: " + inter.channel_name
                ) from exception

            encoded = object_encoder.decode(inter.value, val_type)

            results.internal_channel_commands.append(
                InternalChannelCommandResult(
                    inter.channel_name,
                    encoded,
                    inter.request_status,
                    inter.command_id,
                )
            )

    if not isinstance(idl_results.signal_results, Unset):
        for sig in idl_results.signal_results:
            results.signal_channel_commands.append(
                SignalChannelCommandResult(
                    sig.signal_channel_name,
                    object_encoder.decode(
                        sig.signal_value,
                        signal_channel_types.get(sig.signal_channel_name),
                    ),
                    sig.signal_request_status,
                    sig.command_id,
                )
            )

    if not isinstance(idl_results.state_wait_until_failed, Unset):
        # The server will set state_wait_until_failed to true if the waitUntil API failed.
        # Hence, flag inversion is needed here to indicate that the waitUntil API succeeded.
        results.wait_until_api_succeeded = not idl_results.state_wait_until_failed

    return results
