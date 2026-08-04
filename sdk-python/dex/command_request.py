# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass
from typing import Optional, Union

from dex.errors import WorkflowDefinitionError
from dex.dex_api.models import CommandWaitingType
from dex.dex_api.models.command_combination import CommandCombination
from dex.dex_api.models.command_request import (
    CommandRequest as IdlCommandRequest,
)
from dex.dex_api.models.inter_state_channel_command import (
    InterStateChannelCommand as IdlInternalChannelCommand,
)
from dex.dex_api.models.signal_command import SignalCommand as IdlSignalCommand
from dex.dex_api.models.timer_command import TimerCommand as IdlTimerCommand

@dataclass
class TimerCommand:
    command_id: str
    duration_seconds: int

    @classmethod
    def by_seconds(cls, duration_seconds: int, command_id: Optional[str] = None):
        return TimerCommand(
            command_id if command_id is not None else "", duration_seconds
        )

@dataclass
class InternalChannelCommand:
    command_id: str
    channel_name: str

    @classmethod
    def by_name(cls, channel_name: str, command_id: Optional[str] = None):
        return InternalChannelCommand(
            command_id if command_id is not None else "", channel_name
        )

@dataclass
class SignalChannelCommand:
    command_id: str
    channel_name: str

    @classmethod
    def by_name(cls, channel_name: str, command_id: Optional[str] = None):
        return SignalChannelCommand(
            command_id if command_id is not None else "",
            channel_name,
        )

BaseCommand = Union[TimerCommand, InternalChannelCommand, SignalChannelCommand]

@dataclass
class CommandRequest:
    commands: list[BaseCommand]
    command_waiting_type: CommandWaitingType
    command_combinations: list[CommandCombination]

    @classmethod
    def for_any_command_completed(cls, *commands: BaseCommand):
        bc = [c for c in commands]
        return CommandRequest(bc, CommandWaitingType.ANY_COMPLETED, [])

    @classmethod
    def for_all_command_completed(cls, *commands: BaseCommand):
        bc = [c for c in commands]
        return CommandRequest(bc, CommandWaitingType.ALL_COMPLETED, [])

    @classmethod
    def for_any_command_combination_completed(
        cls, command_combinations_list: list[list[str]], *commands: BaseCommand
    ):
        return CommandRequest(
            list(commands),
            CommandWaitingType.ANY_COMBINATION_COMPLETED,
            [CommandCombination(c) for c in command_combinations_list],
        )

    @classmethod
    def empty(cls):
        return CommandRequest(list(), CommandWaitingType.ALL_COMPLETED, [])

def _to_idl_command_request(request: CommandRequest) -> IdlCommandRequest:
    req = IdlCommandRequest(
        command_waiting_type=request.command_waiting_type,
    )

    timer_commands = []
    internal_channel_commands = []
    signal_commands = []
    for t in request.commands:
        if isinstance(t, TimerCommand):
            timer_commands.append(IdlTimerCommand(t.duration_seconds, t.command_id))
        elif isinstance(t, InternalChannelCommand):
            internal_channel_commands.append(
                IdlInternalChannelCommand(t.channel_name, t.command_id)
            )
        elif isinstance(t, SignalChannelCommand):
            signal_commands.append(IdlSignalCommand(t.channel_name, t.command_id))
        else:
            raise WorkflowDefinitionError(f"unknown command {t.__class__.__qualname__}")

    if len(timer_commands) > 0:
        req.timer_commands = timer_commands
    if len(internal_channel_commands) > 0:
        req.inter_state_channel_commands = internal_channel_commands
    if len(signal_commands) > 0:
        req.signal_commands = signal_commands
    if len(request.command_combinations) > 0:
        req.command_combinations = request.command_combinations
    return req
