# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Step context a connector operation sees inside an agent tool Step."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from datetime import datetime
from typing import Any, TypeVar, cast

from dex import AsyncContext, CodecRegistry, Value, WireKind

ValueT = TypeVar("ValueT")

_CODECS = CodecRegistry()
_NO_HEARTBEAT_VALUE: Any = object()


class ConnectorStepContext:
    """Give a connector operation the tool Step's context, with heartbeats kept as text.

    The single-dispatch fence records markers as heartbeat values and reads the
    previous attempt's marker back. The agent session's tool Step keeps one text
    checkpoint per attempt and repeats it on every background heartbeat. This
    context stores each heartbeat value as that text checkpoint and decodes it
    on the next attempt, so a marker survives the session's own heartbeats and
    a retry can read it. It exposes only the members the fence uses; the agent
    core's conformance tests pin that list.

    ``ConnectorToolSource`` builds one per tool Step attempt. Applications do
    not construct it.

    Attributes:
        flow_id: The Flow ID.
        run_id: The Flow run ID.
        step_execution_id: The tool Step's execution ID, the same on every attempt.
        attempt: The one-based attempt.
        deadline: The attempt's deadline, or ``None``.
    """

    def __init__(
        self,
        context: AsyncContext,
        *,
        previous_checkpoint: str | None,
        heartbeat: Callable[[], Awaitable[None]],
        save_checkpoint: Callable[[str], Awaitable[None]] | None,
    ) -> None:
        self._context = context
        self._previous_checkpoint = previous_checkpoint
        self._heartbeat = heartbeat
        self._save_checkpoint = save_checkpoint
        # The source reads it on a Retry; a failed save leaves the earlier value.
        self._saved_heartbeat_value: object | None = None

    @property
    def flow_id(self) -> str:
        return self._context.flow_id

    @property
    def run_id(self) -> str:
        return self._context.run_id

    @property
    def step_execution_id(self) -> str:
        return self._context.step_execution_id

    @property
    def attempt(self) -> int:
        return self._context.attempt

    @property
    def deadline(self) -> datetime | None:
        return self._context.deadline

    def has_last_heartbeat_value(self) -> bool:
        """Report whether an earlier attempt of this tool Step saved a heartbeat value.

        Returns:
            ``True`` when the session read a checkpoint for this attempt.
        """
        return self._previous_checkpoint is not None

    def get_last_heartbeat_value(self, value_type: type[ValueT]) -> ValueT | None:
        """Decode the earlier attempt's heartbeat value.

        Args:
            value_type: The type the value was saved as, such as the fence's ``Marker``.

        Returns:
            The decoded value, or ``None`` when there is none.

        Raises:
            Exception: If the checkpoint does not decode as ``value_type``.
        """
        if self._previous_checkpoint is None:
            return None
        if value_type is str:
            return cast(ValueT, self._previous_checkpoint)
        codec = _CODECS.resolve(value_type)
        return cast(ValueT, codec.decode(Value(WireKind.JSON, self._previous_checkpoint)))

    async def heartbeat(self, value: object = _NO_HEARTBEAT_VALUE) -> None:
        """Record a heartbeat, saving a value as the attempt's text checkpoint.

        Args:
            value: The heartbeat value, such as a fence marker. Omit it for a
                plain heartbeat, which repeats the saved checkpoint.

        Raises:
            RuntimeError: If a value is given and the tool Step cannot save checkpoints.
        """
        if value is _NO_HEARTBEAT_VALUE:
            await self._heartbeat()
            return
        if self._save_checkpoint is None:
            raise RuntimeError("this tool Step cannot save heartbeat checkpoints")
        await self._save_checkpoint(encode_heartbeat_value(value))
        self._saved_heartbeat_value = value


def encode_heartbeat_value(value: object) -> str:
    """Return the JSON text that stores one heartbeat value as a checkpoint."""
    encoded = _CODECS.resolve(type(value)).encode(value)
    if encoded.kind is not WireKind.JSON or not isinstance(encoded.data, str):
        raise TypeError(f"heartbeat value {type(value).__name__} does not encode as JSON")
    return encoded.data
