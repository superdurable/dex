# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import TYPE_CHECKING, Generic, TypeVar, overload

from dex._utils import require_name
from dex.step import StepOutput

if TYPE_CHECKING:
    from dex.context import AsyncContext, Context

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class Stream(Generic[ValueT]):
    """Define a typed best-effort resumable Stream owned by one Flow type.

    Attributes:
        name: Logical name unique within the Flow persistence schema.
        value_type: Python type encoded for every message.
        stream_capacity_bytes: Positive approximate budget shared by all Flow instances.
    """

    name: str
    value_type: type[ValueT]
    stream_capacity_bytes: int

    def __post_init__(self) -> None:
        """Validate the Stream definition after dataclass construction.

        Raises:
            ValueError: If the name is empty or the byte budget is not positive.
            TypeError: If the byte budget is not an integer.
        """
        require_name(self.name)
        if not isinstance(self.stream_capacity_bytes, int):
            raise TypeError("Stream stream_capacity_bytes must be an integer")
        if self.stream_capacity_bytes <= 0:
            raise ValueError("Stream stream_capacity_bytes must be positive")

    @overload
    def write(  # type: ignore[overload-overlap]
        self,
        context: AsyncContext,
        value: ValueT,
    ) -> None:
        """Enqueue one message from an asynchronous Step handler."""
        ...

    @overload
    def write(self, context: Context, value: ValueT) -> StepOutput:
        """Create one message output for a synchronous Step generator."""
        ...

    def write(
        self,
        context: Context,
        value: ValueT,
    ) -> StepOutput | None:
        """Emit one best-effort message from the current Step execution.

        A synchronous generator must yield the returned StepOutput. An asynchronous
        Step calls this method without ``await``; the message enters the invocation's
        ordered output queue immediately. Neither form waits for Dex Stream Store
        persistence. Calls may write the same Stream any number of times. RPC and
        Flow-timeout Contexts reject Stream writes.

        Args:
            context: Current synchronous or asynchronous Step Context.
            value: Typed message to append.

        Returns:
            A StepOutput for a synchronous Context, or ``None`` for AsyncContext.

        Raises:
            ValueError: If the Stream is unregistered or the Context is not a Step.
            ValueMappingError: If ``value`` cannot be encoded.
        """
        return context._write_stream(self, value)


@dataclass(frozen=True)
class StreamMessage(Generic[ValueT]):
    """Describe one retained Stream message returned by a Client.

    Attributes:
        value: Decoded application message.
        resume_token: Opaque token for the next read.
        created_time: Server-assigned UTC creation time.
        source: Informational client source or Step-generated ``#stepExecutionID``.
    """

    value: ValueT
    resume_token: str
    created_time: datetime
    source: str
