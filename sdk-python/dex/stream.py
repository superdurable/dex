# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import TYPE_CHECKING, Generic, TypeVar, cast, overload

from dex._utils import require_name
from dex.step import StepOutput

if TYPE_CHECKING:
    from dex.context import AsyncContext, Context

ValueT = TypeVar("ValueT")
_DEFAULT_BUFFERED_TEXT_FLUSH_INTERVAL = timedelta(seconds=1)
_DEFAULT_BUFFERED_TEXT_MAX_BYTES = 16 * 1024


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

    @overload
    def buffered(  # type: ignore[overload-overlap]
        self: Stream[str],
        context: AsyncContext,
        *,
        flush_interval: timedelta = _DEFAULT_BUFFERED_TEXT_FLUSH_INTERVAL,
        max_buffered_bytes: int = _DEFAULT_BUFFERED_TEXT_MAX_BYTES,
    ) -> AsyncBufferedTextStream: ...

    @overload
    def buffered(
        self: Stream[str],
        context: Context,
        *,
        flush_interval: timedelta = _DEFAULT_BUFFERED_TEXT_FLUSH_INTERVAL,
        max_buffered_bytes: int = _DEFAULT_BUFFERED_TEXT_MAX_BYTES,
    ) -> BufferedTextStream: ...

    def buffered(
        self: Stream[str],
        context: Context,
        *,
        flush_interval: timedelta = _DEFAULT_BUFFERED_TEXT_FLUSH_INTERVAL,
        max_buffered_bytes: int = _DEFAULT_BUFFERED_TEXT_MAX_BYTES,
    ) -> AsyncBufferedTextStream | BufferedTextStream:
        """Create an invocation-managed writer that batches text chunks.

        The first non-empty chunk starts a one-shot timer. Async Step writers append their current
        batch after ``flush_interval`` even without another chunk. Synchronous generator writers
        check the interval only when ``write`` is called and require an explicit final ``flush``.
        Both forms flush early after crossing the soft UTF-8 byte threshold. Chunks are never split
        or modified.

        AsyncWorker finalizes the writer before sending the invocation result or error. Its
        synchronous ``write`` method can be passed directly as a text-delta callback. Neither form
        waits for Dex Stream Store acknowledgement.

        Args:
            context: Current asynchronous or synchronous Step Context.
            flush_interval: Positive maximum async buffering interval. Defaults to one second.
            max_buffered_bytes: Positive soft UTF-8 threshold. Defaults to 16 KiB.

        Returns:
            An async invocation-managed writer, or a cooperative synchronous writer.

        Raises:
            TypeError: If this Stream does not carry ``str`` values or an option has the wrong type.
            ValueError: If an option is not positive, the Stream is unregistered, or the Context is
                not a Step invocation.
        """
        if self.value_type is not str:
            raise TypeError("Buffered Streams require Stream[str]")
        _validate_buffered_text_options(flush_interval, max_buffered_bytes)
        is_async = context._prepare_buffered_stream(cast(Stream[object], self))
        if is_async:
            writer = AsyncBufferedTextStream(
                self,
                context,
                flush_interval,
                max_buffered_bytes,
            )
            context._register_step_output_finalizer(writer)
            return writer
        return BufferedTextStream(
            self,
            context,
            flush_interval,
            max_buffered_bytes,
        )


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


class AsyncBufferedTextStream:
    """Batch text chunks for an asynchronous Step invocation.

    Create this writer with :meth:`Stream.buffered`. ``write`` and ``flush`` are synchronous and
    return after local buffering or Worker-output enqueueing. AsyncWorker automatically stops its
    timer and flushes remaining text before the invocation result or error.
    """

    def __init__(
        self,
        stream: Stream[str],
        context: Context,
        flush_interval: timedelta,
        max_buffered_bytes: int,
    ) -> None:
        """Initialize an invocation-managed writer from validated factory inputs.

        Args:
            stream: Registered string Stream receiving each batch.
            context: Current asynchronous Step Context.
            flush_interval: Positive one-shot timer interval.
            max_buffered_bytes: Positive soft UTF-8 batch threshold.
        """
        self._stream = stream
        self._context = context
        self._flush_interval_seconds = flush_interval.total_seconds()
        self._max_buffered_bytes = max_buffered_bytes
        self._buffer: list[str] = []
        self._buffered_bytes = 0
        self._timer: asyncio.TimerHandle | None = None
        self._timer_generation = 0
        self._is_closed = False
        self._terminal_error: BaseException | None = None
        self._loop = asyncio.get_running_loop()

    def write(self, chunk: str) -> None:
        """Append one text chunk and flush after reaching the soft size threshold.

        Empty chunks are ignored. The first non-empty chunk starts the configured one-shot timer.
        This method does not wait for the timer or Stream Store acknowledgement.

        Args:
            chunk: Text to append without modification.

        Raises:
            TypeError: If ``chunk`` is not a string.
            BaseException: The previously latched timer or Worker-output failure.
            ValueError: If the invocation already finished.
        """
        self._require_open()
        if not isinstance(chunk, str):
            raise TypeError("Buffered Stream chunks must be strings")
        if not chunk:
            return
        was_empty = not self._buffer
        self._buffer.append(chunk)
        self._buffered_bytes += len(chunk.encode("utf-8"))
        if was_empty:
            self._start_timer()
        if self._buffered_bytes >= self._max_buffered_bytes:
            self.flush()

    def flush(self) -> None:
        """Immediately enqueue the current non-empty batch.

        A later write starts a new timer. Stream Store failures remain unacknowledged.

        Raises:
            BaseException: A timer or Worker-output failure.
            ValueError: If the invocation already finished.
        """
        self._require_open()
        self._stop_timer()
        self._flush_buffer()

    def _finalize_step_output(self) -> None:
        if self._is_closed:
            if self._terminal_error is not None:
                raise self._terminal_error
            return
        self._stop_timer()
        try:
            if self._terminal_error is not None:
                raise self._terminal_error
            self._flush_buffer()
        finally:
            self._is_closed = True

    def _cancel_step_output(self) -> None:
        self._stop_timer()
        self._buffer.clear()
        self._buffered_bytes = 0
        self._is_closed = True

    def _start_timer(self) -> None:
        self._timer_generation += 1
        generation = self._timer_generation
        self._timer = self._loop.call_later(
            self._flush_interval_seconds,
            self._flush_from_timer,
            generation,
        )

    def _flush_from_timer(self, generation: int) -> None:
        if (
            self._is_closed
            or self._terminal_error is not None
            or generation != self._timer_generation
        ):
            return
        self._timer = None
        try:
            self._flush_buffer()
        except BaseException as error:
            self._terminal_error = error

    def _stop_timer(self) -> None:
        self._timer_generation += 1
        if self._timer is not None:
            self._timer.cancel()
            self._timer = None

    def _flush_buffer(self) -> None:
        if not self._buffer:
            return
        value = "".join(self._buffer)
        self._buffer.clear()
        self._buffered_bytes = 0
        try:
            output = self._stream.write(self._context, value)
            if output is not None:
                raise RuntimeError(
                    "Async Buffered Stream produced a synchronous StepOutput"
                )
        except BaseException as error:
            self._terminal_error = error
            raise

    def _require_open(self) -> None:
        if self._terminal_error is not None:
            raise self._terminal_error
        if self._is_closed:
            raise ValueError("Buffered Stream invocation has finished")


class BufferedTextStream:
    """Batch text chunks for a synchronous Step generator.

    Create this writer with :meth:`Stream.buffered`. Yield every output returned by ``write`` and
    ``flush``. The interval is cooperative: elapsed time is checked when another chunk arrives.
    """

    def __init__(
        self,
        stream: Stream[str],
        context: Context,
        flush_interval: timedelta,
        max_buffered_bytes: int,
    ) -> None:
        """Initialize a cooperative writer from validated factory inputs.

        Args:
            stream: Registered string Stream receiving each batch.
            context: Current synchronous Step Context.
            flush_interval: Positive cooperative flush interval.
            max_buffered_bytes: Positive soft UTF-8 batch threshold.
        """
        self._stream = stream
        self._context = context
        self._flush_interval_seconds = flush_interval.total_seconds()
        self._max_buffered_bytes = max_buffered_bytes
        self._buffer: list[str] = []
        self._buffered_bytes = 0
        self._started_at: float | None = None

    def write(self, chunk: str) -> tuple[StepOutput, ...]:
        """Append one chunk and return an output when a threshold is reached.

        Args:
            chunk: Text to append without modification.

        Returns:
            An empty tuple while buffering, or one StepOutput that the handler must yield.

        Raises:
            TypeError: If ``chunk`` is not a string.
            ValueMappingError: If the combined text cannot be encoded.
        """
        if not isinstance(chunk, str):
            raise TypeError("Buffered Stream chunks must be strings")
        if not chunk:
            return ()
        if self._started_at is None:
            self._started_at = time.monotonic()
        self._buffer.append(chunk)
        self._buffered_bytes += len(chunk.encode("utf-8"))
        has_elapsed = (
            time.monotonic() - self._started_at >= self._flush_interval_seconds
        )
        if self._buffered_bytes < self._max_buffered_bytes and not has_elapsed:
            return ()
        return self.flush()

    def flush(self) -> tuple[StepOutput, ...]:
        """Return the current batch as one StepOutput for the handler to yield.

        Returns:
            An empty tuple for an empty buffer, or one Stream StepOutput.

        Raises:
            ValueMappingError: If the combined text cannot be encoded.
        """
        if not self._buffer:
            return ()
        value = "".join(self._buffer)
        self._buffer.clear()
        self._buffered_bytes = 0
        self._started_at = None
        output = self._stream.write(self._context, value)
        if output is None:
            raise RuntimeError(
                "Synchronous Buffered Stream did not produce a StepOutput"
            )
        return (output,)


def _validate_buffered_text_options(
    flush_interval: timedelta,
    max_buffered_bytes: int,
) -> None:
    if not isinstance(flush_interval, timedelta):
        raise TypeError("Buffered Stream flush_interval must be timedelta")
    if flush_interval <= timedelta(0):
        raise ValueError("Buffered Stream flush_interval must be positive")
    if not isinstance(max_buffered_bytes, int) or isinstance(max_buffered_bytes, bool):
        raise TypeError("Buffered Stream max_buffered_bytes must be an integer")
    if max_buffered_bytes <= 0:
        raise ValueError("Buffered Stream max_buffered_bytes must be positive")
