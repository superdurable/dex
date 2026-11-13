# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The terminal chat: messages, streamed replies, approvals, questions, and Ctrl-C."""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import signal
import stat
import sys
from collections import deque
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass, field
from datetime import timedelta
from enum import Enum
from pathlib import Path
from typing import Any, TextIO, TypeVar

from dex import (
    AsyncClient,
    DexServiceError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    LongPollTimeoutError,
)

from dex_agents import (
    MAX_IMAGE_BYTES,
    MAX_IMAGES_PER_MESSAGE,
    AgentEvent,
    AgentEventType,
    AgentImageReference,
    AgentInterrupt,
    AgentSessionClient,
    AgentSessionStatus,
    AgentSnapshot,
    ImageValidationError,
    InterruptKind,
    MediaStore,
    MessageRole,
    detect_image_mime_type,
)

#: The line that ends the chat, like Ctrl-D.
EXIT_COMMAND = "/exit"
#: The command that attaches an image file to the next message: ``/image <path>``.
IMAGE_COMMAND = "/image"
_POLL_INTERVAL_SECONDS = 0.3
_EVENT_LONG_POLL = timedelta(seconds=5)
_MAX_SHOWN_ARGUMENT_CHARACTERS = 2000
_KIB = 1024
# A Worker of the shared Flow type may be briefly unreachable when a chat starts.
_CHAT_START_RETRY_SECONDS = 10.0
_CHAT_START_RETRY_INTERVAL_SECONDS = 0.5
_Read = TypeVar("_Read")


class TerminalSignal(Enum):
    """Input that is not a line: Ctrl-C, or the end of input (Ctrl-D)."""

    INTERRUPT = "interrupt"
    END_OF_INPUT = "end_of_input"


TerminalInput = str | TerminalSignal


class Terminal:
    """Read lines and Ctrl-C from the terminal on the event loop, and write output.

    Lines are read with ``loop.add_reader``, so no thread blocks on input,
    and SIGINT becomes ``TerminalSignal.INTERRUPT`` instead of
    ``KeyboardInterrupt``.
    """

    def __init__(self, input_fd: int | None = None, output: TextIO | None = None) -> None:
        """Use a terminal's file descriptors.

        Args:
            input_fd: The input file descriptor; ``None`` uses standard input.
            output: The output stream; ``None`` uses standard output.
        """
        self._input_fd = sys.stdin.fileno() if input_fd is None else input_fd
        self._output = output or sys.stdout
        self._queue: asyncio.Queue[TerminalInput] = asyncio.Queue()
        self._partial = b""
        self._is_input_open = True

    def start(self) -> None:
        """Start reading input and catching Ctrl-C."""
        loop = asyncio.get_running_loop()
        try:
            loop.add_reader(self._input_fd, self._read_input)
        except (OSError, ValueError):
            # Input the selector cannot watch, such as /dev/null, reads as ended.
            self._is_input_open = False
            self._queue.put_nowait(TerminalSignal.END_OF_INPUT)
        loop.add_signal_handler(signal.SIGINT, self._queue.put_nowait, TerminalSignal.INTERRUPT)

    def stop(self) -> None:
        """Stop reading input and restore Ctrl-C."""
        loop = asyncio.get_running_loop()
        if self._is_input_open:
            loop.remove_reader(self._input_fd)
        loop.remove_signal_handler(signal.SIGINT)

    async def next_input(self, timeout: float | None = None) -> TerminalInput | None:
        """Return the next line or signal.

        Args:
            timeout: Seconds to wait, or ``None`` to wait for input.

        Returns:
            The input, or ``None`` when the timeout passes first.
        """
        try:
            return await asyncio.wait_for(self._queue.get(), timeout)
        except TimeoutError:
            return None

    def write(self, text: str) -> None:
        """Write text and flush it."""
        self._output.write(text)
        self._output.flush()

    def _read_input(self) -> None:
        try:
            chunk = os.read(self._input_fd, 4096)
        except BlockingIOError:
            return
        except OSError:
            chunk = b""
        if not chunk:
            asyncio.get_running_loop().remove_reader(self._input_fd)
            self._is_input_open = False
            if self._partial:
                self._queue.put_nowait(self._partial.decode("utf-8", errors="replace"))
                self._partial = b""
            self._queue.put_nowait(TerminalSignal.END_OF_INPUT)
            return
        self._partial += chunk
        *lines, self._partial = self._partial.split(b"\n")
        for line in lines:
            self._queue.put_nowait(line.decode("utf-8", errors="replace").rstrip("\r"))


@dataclass
class _TurnView:
    """What the terminal showed of one turn."""

    is_text_shown: bool = False
    is_failure_shown: bool = False
    attempts: dict[str, int] = field(default_factory=dict)


class TerminalChat:
    """Chat with one agent session in the terminal.

    Each line is a user message. Replies and tool calls stream from the
    session's events. Approvals and questions are answered inline. The first
    Ctrl-C during a turn cancels it; another Ctrl-C, or Ctrl-C, Ctrl-D, or
    ``/exit`` while idle, exits. Exiting never ends the session.
    ``/image <path>`` copies an image file into the media store and sends it
    with the next message.
    """

    def __init__(
        self,
        client: AsyncClient,
        sessions: AgentSessionClient,
        session_id: str,
        terminal: Terminal,
        resume_command: str,
        *,
        media_store: MediaStore,
        home: Path,
    ) -> None:
        """Chat with one session.

        Args:
            client: The Dex Client, to find the newest session event.
            sessions: The session client.
            session_id: The session.
            terminal: The terminal.
            resume_command: The command that resumes this session, shown on exit.
            media_store: The store that ``/image`` uploads to.
            home: The home directory that a leading ``~`` in an image path means.
        """
        self._client = client
        self._sessions = sessions
        self._session_id = session_id
        self._terminal = terminal
        self._resume_command = resume_command
        self._media_store = media_store
        self._home = home
        self._typeahead: deque[str] = deque()
        self._pending_images: list[AgentImageReference] = []
        self._is_exit_requested = False
        self._is_input_ended = False
        self._has_failure = False

    async def run(self) -> bool:
        """Chat until the user exits.

        Returns:
            ``False`` when a turn or the session failed during the chat, so the
            command exits with code 1; ``True`` otherwise.
        """
        self._terminal.write(
            f"Type a message; {IMAGE_COMMAND} <path> attaches an image to the next one; "
            f"{EXIT_COMMAND} quits. Ctrl-C cancels a turn, and again exits.\n"
        )
        snapshot = await _retry_at_chat_start(
            lambda: self._sessions.get_agent_snapshot(self._session_id)
        )
        if snapshot.state is not None and snapshot.state.active_turn_id is not None:
            self._terminal.write("A turn is running; following it.\n")
            tail = await _retry_at_chat_start(self._event_tail)
            await self._follow_turn(snapshot.state.turn_count, tail)
        while not self._is_exit_requested:
            self._terminal.write("\n> ")
            line = await self._next_line()
            if line is None:
                break
            if not line.strip():
                continue
            if line.strip() == EXIT_COMMAND:
                break
            command, _, argument = line.strip().partition(" ")
            if command == IMAGE_COMMAND:
                await self._attach_image(argument)
                continue
            await self._send(line)
        self._terminal.write(
            f"\nSession {self._session_id} keeps running. Resume it with: "
            f"{self._resume_command} --resume {self._session_id}\n"
        )
        return not self._has_failure

    async def _send(self, text: str) -> None:
        snapshot = await self._sessions.get_agent_snapshot(self._session_id)
        if snapshot.state is None or snapshot.state.status is AgentSessionStatus.FAILED:
            reason = snapshot.state.last_turn_error if snapshot.state else "it has not started"
            self._terminal.write(f"[session failed] {reason}\n")
            self._has_failure = True
            self._is_exit_requested = True
            return
        tail = await self._event_tail()
        images, self._pending_images = self._pending_images, []
        if not await self._sessions.send_user_message(self._session_id, text, images=images):
            if images:
                self._terminal.write(
                    "[not sent] this agent does not take images. The attached images "
                    f"({_count(images, 'image')}) were removed; send the message again "
                    "without them.\n"
                )
            else:
                self._terminal.write("[not sent]\n")
            return
        await self._follow_turn(snapshot.state.turn_count, tail)

    async def _attach_image(self, argument: str) -> None:
        """Copy one image file into the media store and keep it for the next message."""
        path_text = _unquote(argument.strip())
        if not path_text:
            self._terminal.write(f"[image not attached] usage: {IMAGE_COMMAND} <path>\n")
            return
        if len(self._pending_images) >= MAX_IMAGES_PER_MESSAGE:
            self._terminal.write(
                f"[image not attached] a message carries at most {MAX_IMAGES_PER_MESSAGE} images\n"
            )
            return
        path = self._resolve_image_path(path_text)
        try:
            data = _read_image_file(path)
            mime_type = detect_image_mime_type(data)
        except OSError as error:
            self._terminal.write(f"[image not attached] {error}\n")
            return
        except ImageValidationError as error:
            self._terminal.write(f"[image not attached] {path}: {error}\n")
            return
        try:
            reference = await self._media_store.put_image(data)
        except Exception as error:  # Each store raises its own upload errors, such as S3's.
            self._terminal.write(f"[image not attached] cannot store {path}: {error}\n")
            return
        self._pending_images.append(reference)
        self._terminal.write(
            f"[image attached] {path.name} ({mime_type}, {_format_size(len(data))}); "
            f"it goes with your next message ({_count(self._pending_images, 'image')} attached)\n"
        )

    def _resolve_image_path(self, path_text: str) -> Path:
        if path_text == "~" or path_text.startswith("~/"):
            return self._home / path_text[2:]
        return Path.cwd() / path_text

    async def _follow_turn(self, turn_count_before: int, event_tail: str) -> None:
        view = _TurnView()
        events = asyncio.create_task(self._render_events(event_tail, view))
        handled_interrupts: set[str] = set()
        is_cancelling = False
        try:
            while True:
                snapshot = await self._sessions.get_agent_snapshot(self._session_id)
                state = snapshot.state
                if state is None:
                    return
                if state.status is AgentSessionStatus.FAILED:
                    self._terminal.write(f"\n[session failed] {state.last_turn_error}\n")
                    self._has_failure = True
                    self._is_exit_requested = True
                    return
                if state.turn_count > turn_count_before and state.active_turn_id is None:
                    await asyncio.sleep(_POLL_INTERVAL_SECONDS)
                    self._finish_turn(snapshot, f"turn-{turn_count_before + 1}", view)
                    return
                if not is_cancelling:
                    for interrupt in snapshot.pending_interrupts:
                        if interrupt.interrupt_id in handled_interrupts:
                            continue
                        handled_interrupts.add(interrupt.interrupt_id)
                        if not await self._answer_interrupt(interrupt):
                            if self._is_exit_requested:
                                return
                            is_cancelling = await self._on_interrupt_key(is_cancelling)
                            break
                received = await self._terminal.next_input(_POLL_INTERVAL_SECONDS)
                if received is TerminalSignal.INTERRUPT:
                    is_cancelling = await self._on_interrupt_key(is_cancelling)
                elif received is TerminalSignal.END_OF_INPUT:
                    # Piped input ends early; the turn still finishes before the command exits.
                    self._is_input_ended = True
                elif isinstance(received, str):
                    self._typeahead.append(received)
                if self._is_exit_requested:
                    return
        finally:
            events.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await events

    async def _on_interrupt_key(self, is_cancelling: bool) -> bool:
        """Cancel the turn on the first Ctrl-C and request exit on the next; return cancelling."""
        if is_cancelling:
            self._is_exit_requested = True
            return True
        self._terminal.write("\n[cancelling the turn; Ctrl-C again exits]\n")
        if not await self._sessions.cancel_turn(self._session_id):
            self._terminal.write("[no turn to cancel]\n")
        return True

    async def _answer_interrupt(self, interrupt: AgentInterrupt) -> bool:
        """Ask the user about one interrupt; return ``False`` when they pressed Ctrl-C."""
        if interrupt.kind is InterruptKind.QUESTION:
            return await self._answer_question(interrupt)
        arguments = interrupt.arguments
        if len(arguments) > _MAX_SHOWN_ARGUMENT_CHARACTERS:
            arguments = arguments[:_MAX_SHOWN_ARGUMENT_CHARACTERS] + " ..."
        self._terminal.write(
            f"\n[approval] {interrupt.tool_name} {arguments}\nApprove? [y/N, or n <reason>] "
        )
        answer = await self._next_answer()
        if answer is None:
            return False
        approved = answer.strip().lower() in ("y", "yes")
        reason = None
        if not approved:
            words = answer.strip().split(maxsplit=1)
            if words and words[0].lower() in ("n", "no"):
                words = words[1:]
            reason = words[0] if words else None
        resolved = await self._sessions.resolve_interrupt(
            self._session_id, interrupt.interrupt_id, approved=approved, rejection_message=reason
        )
        self._terminal.write(
            ("[approved]\n" if approved else "[rejected]\n") if resolved else "[already resolved]\n"
        )
        return True

    async def _answer_question(self, interrupt: AgentInterrupt) -> bool:
        choices = "".join(
            f"\n  {index}) {choice}" for index, choice in enumerate(interrupt.choices, 1)
        )
        self._terminal.write(
            f"\n[question] {interrupt.question}{choices}\nAnswer (empty declines): "
        )
        answer = await self._next_answer()
        if answer is None:
            return False
        answer = answer.strip()
        if not answer:
            resolved = await self._sessions.resolve_interrupt(
                self._session_id, interrupt.interrupt_id, approved=False
            )
        else:
            if answer.isdigit() and 1 <= int(answer) <= len(interrupt.choices):
                answer = interrupt.choices[int(answer) - 1]
            resolved = await self._sessions.answer_question(
                self._session_id, interrupt.interrupt_id, answer
            )
        self._terminal.write("[answered]\n" if resolved else "[not accepted]\n")
        return True

    def _finish_turn(self, snapshot: AgentSnapshot, turn_id: str, view: _TurnView) -> None:
        if not view.is_text_shown:
            replies = [
                message.text
                for message in snapshot.messages
                if message.turn_id == turn_id
                and message.role is MessageRole.ASSISTANT
                and message.text
            ]
            if replies:
                self._terminal.write(replies[-1])
        state = snapshot.state
        if state is not None and state.last_turn_error and not view.is_failure_shown:
            self._terminal.write(f"\n[turn failed] {state.last_turn_error}")
        if state is not None and state.last_turn_error:
            self._has_failure = True
        self._terminal.write("\n")

    async def _render_events(self, resume_token: str, view: _TurnView) -> None:
        token = resume_token
        while True:
            try:
                message = await self._sessions.read_agent_event(
                    self._session_id, token, _EVENT_LONG_POLL
                )
            except LongPollTimeoutError:
                continue
            token = message.resume_token
            self._render_event(message.value, view)

    def _render_event(self, event: AgentEvent, view: _TurnView) -> None:
        if event.step_execution_id:
            seen = view.attempts.get(event.step_execution_id, 0)
            if event.attempt < seen:
                return
            if seen and event.attempt > seen and view.is_text_shown:
                self._terminal.write("\n[retrying]\n")
            view.attempts[event.step_execution_id] = event.attempt
        payload = _read_payload(event.payload)
        match event.type:
            case AgentEventType.TEXT_DELTA:
                text = str(payload.get("text", ""))
                if text:
                    view.is_text_shown = True
                    self._terminal.write(text)
            case AgentEventType.TOOL_CALL_STARTED:
                self._terminal.write(f"\n[tool] {payload.get('tool_name')} started\n")
            case AgentEventType.TOOL_CALL_FINISHED:
                self._terminal.write(f"[tool] {payload.get('tool_name')} {payload.get('status')}\n")
            case AgentEventType.TURN_FAILED:
                view.is_failure_shown = True
                self._terminal.write(f"\n[turn failed] {payload.get('error')}")
            case AgentEventType.TURN_CANCELLED:
                self._terminal.write("\n[turn cancelled]")

    async def _next_line(self) -> str | None:
        """Return the next message line; ``None`` when the user exits."""
        if self._typeahead:
            line = self._typeahead.popleft()
            self._terminal.write(line + "\n")
            return line
        if self._is_input_ended:
            self._is_exit_requested = True
            return None
        received = await self._terminal.next_input()
        if isinstance(received, str):
            return received
        self._is_exit_requested = True
        return None

    async def _next_answer(self) -> str | None:
        """Return the answer to a prompt; ``None`` for Ctrl-C or the end of input."""
        if self._typeahead:
            return self._typeahead.popleft()
        if self._is_input_ended:
            self._is_exit_requested = True
            return None
        received = await self._terminal.next_input()
        if received is TerminalSignal.END_OF_INPUT:
            self._is_input_ended = True
            self._is_exit_requested = True
        return received if isinstance(received, str) else None

    async def _event_tail(self) -> str:
        """Return the resume token of the newest event, so old events are not shown again."""
        page = await self._client.list_stream_messages(
            self._session_id, self._sessions.flow.agent_events, 1
        )
        return page.messages[0].resume_token if page.messages else ""


async def _retry_at_chat_start(read: Callable[[], Awaitable[_Read]]) -> _Read:
    """Retry a read that failed for a reason other than a missing session, for a few seconds.

    Raises:
        DexServiceError: The last failure, once ``_CHAT_START_RETRY_SECONDS`` passed.
    """
    loop = asyncio.get_running_loop()
    deadline = loop.time() + _CHAT_START_RETRY_SECONDS
    while True:
        try:
            return await read()
        except (FlowNotFoundError, FlowNotActiveOrNotFoundError):
            raise
        except DexServiceError:
            if loop.time() >= deadline:
                raise
        await asyncio.sleep(_CHAT_START_RETRY_INTERVAL_SECONDS)


def _read_image_file(path: Path) -> bytes:
    """Read an image file, refusing a missing, special, or oversized file before reading it."""
    try:
        status = path.stat()
    except FileNotFoundError as error:
        raise FileNotFoundError(f"{path} does not exist") from error
    if not stat.S_ISREG(status.st_mode):
        raise OSError(f"{path} is not a regular file")
    if status.st_size > MAX_IMAGE_BYTES:
        raise OSError(_too_large_message(path, status.st_size))
    with path.open("rb") as file:
        data = file.read(MAX_IMAGE_BYTES + 1)
    if len(data) > MAX_IMAGE_BYTES:
        raise OSError(_too_large_message(path, len(data)))
    return data


def _too_large_message(path: Path, size: int) -> str:
    return f"{path} has {size:,} bytes; an image may have at most {MAX_IMAGE_BYTES:,}"


def _format_size(size: int) -> str:
    if size < _KIB:
        return f"{size} bytes"
    if size < _KIB * _KIB:
        return f"{round(size / _KIB)} KB"
    return f"{size / (_KIB * _KIB):.1f} MB"


def _count(items: Sequence[object], noun: str) -> str:
    return f"1 {noun}" if len(items) == 1 else f"{len(items)} {noun}s"


def _unquote(text: str) -> str:
    """Drop one pair of matching quotes, which terminals add around a dropped file's path."""
    if len(text) >= 2 and text[0] == text[-1] and text[0] in "'\"":
        return text[1:-1]
    return text


def _read_payload(payload: str) -> dict[str, Any]:
    try:
        parsed = json.loads(payload)
    except json.JSONDecodeError:
        return {}
    return parsed if isinstance(parsed, dict) else {}
