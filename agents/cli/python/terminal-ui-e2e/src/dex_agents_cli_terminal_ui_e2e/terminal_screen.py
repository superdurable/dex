# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A terminal emulator for assertions: pyte, plus what a Bubble Tea UI sends and pyte lacks.

pyte has no scroll up and down (``CSI n S``, ``CSI n T``), no alternate
screen, and draws unknown private sequences, such as the kitty keyboard
``CSI < 1 u``, as text. ``TerminalScreen`` handles the first two itself and
drops the rest before pyte sees them. Lines that scroll off the top of a
full-screen scroll region go to ``scrollback``, as a real terminal keeps them.

The terminal UI draws on the alternate screen and scrolls its own
transcript, so a line can leave the screen without reaching the scrollback.
``seen_lines`` keeps every line any frame showed, for "did it show" checks.
A frame is the screen at a frame boundary: the end of a read, a cursor hide
or show, the end of a synchronized update, a full clear, or a screen switch.
One read can hold several frames, so each boundary records the screen first.
"""

from __future__ import annotations

import copy
import re
from dataclasses import dataclass

import pyte

_CSI = re.compile(rb"\x1b\[([\x30-\x3f]*)([\x20-\x2f]*)([\x40-\x7e])")
# A CSI sequence cut off at the end of one read.
_PARTIAL_CSI = re.compile(rb"\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*)?\Z")
_ANY_ESCAPE = re.compile(
    rb"\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]"
    rb"|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)"
    rb"|\x1b[PX^_][^\x1b]*\x1b\\"
    rb"|\x1b[\x20-\x2f]*[\x30-\x7e]"
)
_PYTE_CSI_FINALS = frozenset(pyte.Stream.csi)
_ALTERNATE_SCREEN_MODES = frozenset({47, 1047, 1049})
# Cursor visibility and synchronized output, which renderers toggle around each frame.
_FRAME_BOUNDARY_MODES = frozenset({25, 2026})


@dataclass
class _SavedScreen:
    buffer: dict[int, pyte.screens.StaticDefaultDict[int, pyte.screens.Char]]
    cursor: pyte.screens.Cursor


class _ScrollbackScreen(pyte.Screen):
    """A pyte screen that keeps the lines a full-screen scroll pushes off the top."""

    def __init__(self, columns: int, lines: int) -> None:
        super().__init__(columns, lines)
        self.scrollback: list[str] = []
        self.is_recording_scrollback = True

    def index(self) -> None:
        top, bottom = self.margins or pyte.screens.Margins(0, self.lines - 1)
        if self.is_recording_scrollback and self.cursor.y == bottom and top == 0:
            self.scrollback.append(self.render_row(top))
        super().index()

    def render_row(self, row_index: int) -> str:
        row = self.buffer[row_index]
        return "".join(row[column].data for column in range(self.columns)).rstrip()


class TerminalScreen:
    """What a terminal of a given size shows after the bytes fed to it.

    Attributes:
        raw: Every byte fed, unchanged, for leak checks and transcripts.
    """

    def __init__(self, columns: int, lines: int) -> None:
        """Create an empty screen."""
        self._screen = _ScrollbackScreen(columns, lines)
        self._stream = pyte.ByteStream(self._screen)
        self._pending = b""
        self._saved_main_screen: _SavedScreen | None = None
        self._seen_lines: dict[str, None] = {}
        self.raw = bytearray()

    @property
    def columns(self) -> int:
        """Return the width."""
        return int(self._screen.columns)

    @property
    def lines(self) -> int:
        """Return the height."""
        return int(self._screen.lines)

    @property
    def is_alternate_screen(self) -> bool:
        """Report whether a full-screen view, such as a pager, is open."""
        return self._saved_main_screen is not None

    def feed(self, data: bytes) -> None:
        """Interpret bytes the program wrote."""
        self.raw += data
        buffered = self._pending + data
        partial = _PARTIAL_CSI.search(buffered)
        if partial is not None:
            self._pending = buffered[partial.start() :]
            buffered = buffered[: partial.start()]
        else:
            self._pending = b""
        position = 0
        for match in _CSI.finditer(buffered):
            self._stream.feed(buffered[position : match.start()])
            self._apply_control_sequence(match)
            position = match.end()
        self._stream.feed(buffered[position:])
        self._record_frame()

    def resize(self, columns: int, lines: int) -> None:
        """Change the size, as a terminal window resize does."""
        self._screen.resize(lines, columns)

    def display(self) -> list[str]:
        """Return the visible lines, without trailing spaces."""
        return [line.rstrip() for line in self._screen.display]

    def scrollback(self) -> list[str]:
        """Return the lines scrolled off the top, oldest first."""
        return list(self._screen.scrollback)

    def seen_lines(self) -> list[str]:
        """Return every distinct line that the screen showed after any feed, first seen first."""
        return list(self._seen_lines)

    def all_lines(self) -> list[str]:
        """Return the scrollback, then every line any frame showed, then the visible lines."""
        return [*self.scrollback(), *self.seen_lines(), *self.display()]

    def text(self) -> str:
        """Return the visible lines joined with newlines."""
        return "\n".join(self.display())

    def cursor_row(self) -> int:
        """Return the cursor's row, 0 at the top."""
        return int(self._screen.cursor.y)

    def output_text(self) -> str:
        """Return every byte fed as text, without escape sequences and carriage returns."""
        decoded = _ANY_ESCAPE.sub(b"", bytes(self.raw)).decode("utf-8", errors="replace")
        return decoded.replace("\r", "")

    def _record_frame(self) -> None:
        for line in self.display():
            self._seen_lines.setdefault(line, None)

    def _apply_control_sequence(self, match: re.Match[bytes]) -> None:
        parameters, intermediates, final = match.group(1), match.group(2), match.group(3)
        final_character = final.decode()
        if intermediates or parameters[:1] in (b"<", b">", b"="):
            return
        if parameters[:1] == b"?":
            modes = _numbers(parameters[1:])
            if final_character in "hl" and (
                _FRAME_BOUNDARY_MODES.union(_ALTERNATE_SCREEN_MODES).intersection(modes)
            ):
                self._record_frame()
            if final_character in "hl" and _ALTERNATE_SCREEN_MODES.intersection(modes):
                self._switch_alternate_screen(final_character == "h")
                others = [mode for mode in modes if mode not in _ALTERNATE_SCREEN_MODES]
                if others:
                    self._stream.feed(_private_mode_sequence(others, final_character))
                return
            if final_character in _PYTE_CSI_FINALS:
                self._stream.feed(match.group(0))
            return
        if final_character == "J" and (_numbers(parameters) or [0])[0] in (2, 3):
            self._record_frame()
        if final_character in "ST":
            count = max(1, (_numbers(parameters) or [1])[0])
            self._scroll(count, is_up=final_character == "S")
            return
        if final_character in _PYTE_CSI_FINALS:
            self._stream.feed(match.group(0))

    def _scroll(self, count: int, is_up: bool) -> None:
        screen = self._screen
        top, bottom = screen.margins or pyte.screens.Margins(0, screen.lines - 1)
        saved_x, saved_y = screen.cursor.x, screen.cursor.y
        screen.cursor.y = bottom if is_up else top
        for _ in range(count):
            if is_up:
                screen.index()
            else:
                screen.reverse_index()
        screen.cursor.x, screen.cursor.y = saved_x, saved_y

    def _switch_alternate_screen(self, is_entering: bool) -> None:
        screen = self._screen
        if is_entering and self._saved_main_screen is None:
            self._saved_main_screen = _SavedScreen(
                copy.deepcopy(screen.buffer), copy.copy(screen.cursor)
            )
            screen.erase_in_display(2)
            screen.is_recording_scrollback = False
        elif not is_entering and self._saved_main_screen is not None:
            screen.buffer = self._saved_main_screen.buffer
            screen.cursor = self._saved_main_screen.cursor
            screen.dirty.update(range(screen.lines))
            self._saved_main_screen = None
            screen.is_recording_scrollback = True


def _numbers(parameters: bytes) -> list[int]:
    return [int(part) for part in parameters.split(b";") if part.isdigit()]


def _private_mode_sequence(modes: list[int], final_character: str) -> bytes:
    return f"\x1b[?{';'.join(str(mode) for mode in modes)}{final_character}".encode()
