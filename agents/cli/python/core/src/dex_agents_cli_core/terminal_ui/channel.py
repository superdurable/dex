# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Newline-delimited JSON messages over one end of a Unix stream socket, on the event loop."""

from __future__ import annotations

import asyncio
import contextlib
import socket
from collections.abc import Mapping
from typing import Any

from dex_agents_cli_core.terminal_ui.contract import (
    MAX_LINE_BYTES,
    ChannelProtocolError,
    encode_message,
    parse_message_line,
)


class MessageChannel:
    """Send and receive contract-framed messages: one compact JSON object per line.

    Both the terminal UI channel and the internal Worker host control channel use it.
    """

    def __init__(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        """Wrap an open stream pair; use ``open`` to make one from a socket."""
        self._reader = reader
        self._writer = writer
        self._send_lock = asyncio.Lock()
        self._is_closed = False

    @classmethod
    async def open(cls, channel_socket: socket.socket) -> MessageChannel:
        """Open a channel on a connected Unix stream socket, which it then owns."""
        reader, writer = await asyncio.open_unix_connection(
            sock=channel_socket, limit=MAX_LINE_BYTES
        )
        return cls(reader, writer)

    @property
    def is_closed(self) -> bool:
        """Return whether this side closed, or a send found the peer gone."""
        return self._is_closed

    async def receive(self) -> dict[str, Any] | None:
        """Return the next message, or ``None`` when the peer closed the channel.

        Raises:
            ChannelProtocolError: For a line over 1 MiB, bytes after the last
                newline, or a line that is not a JSON object with a string ``type``.
        """
        try:
            line = await self._reader.readuntil(b"\n")
        except asyncio.IncompleteReadError as error:
            if error.partial:
                raise ChannelProtocolError("the channel ended inside a line") from error
            return None
        except asyncio.LimitOverrunError as error:
            raise ChannelProtocolError("a channel line is longer than 1 MiB") from error
        except (ConnectionResetError, BrokenPipeError):
            return None
        return parse_message_line(line[:-1])

    async def send(self, message: Mapping[str, Any]) -> None:
        """Send one message; a peer that already closed is the end, not an error."""
        line = encode_message(message)
        async with self._send_lock:
            if self._is_closed:
                return
            try:
                self._writer.write(line)
                await self._writer.drain()
            except (BrokenPipeError, ConnectionResetError):
                self._is_closed = True

    def post(self, message: Mapping[str, Any]) -> None:
        """Queue one message without waiting, for synchronous callers such as log reports."""
        if self._is_closed:
            return
        try:
            self._writer.write(encode_message(message))
        except (BrokenPipeError, ConnectionResetError):
            self._is_closed = True

    async def close(self) -> None:
        """Close this end; the peer reads EOF."""
        if self._is_closed and self._writer.is_closing():
            return
        self._is_closed = True
        self._writer.close()
        with contextlib.suppress(OSError):
            await self._writer.wait_closed()
