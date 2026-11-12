# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run one command in a pseudo-terminal, render what it draws, and wait for screen states."""

from __future__ import annotations

import contextlib
import os
import signal
import time
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from typing import BinaryIO

import pexpect

from dex_agents_cli_terminal_ui_e2e.process_tree import ProcessTreeRecord
from dex_agents_cli_terminal_ui_e2e.terminal_screen import TerminalScreen

KEYS = {
    "enter": "\r",
    "tab": "\t",
    "escape": "\x1b",
    "ctrl_c": "\x03",
    "ctrl_d": "\x04",
    "backspace": "\x7f",
    "up": "\x1b[A",
    "down": "\x1b[B",
    "page_up": "\x1b[5~",
    "page_down": "\x1b[6~",
    "end": "\x1b[F",
}
_READ_CHUNK_BYTES = 65536
_POLL_SECONDS = 0.05
# Bubble Tea reads a lone ESC as the Escape key only after a short pause.
_KEY_GAP_SECONDS = 0.15
_HANGUP_EXIT_SECONDS = 30.0
_PROCESS_RECORD_SECONDS = 1.0


class ScreenWaitTimeout(AssertionError):
    """The screen did not reach the expected state in time; the message holds the screen."""


class TerminalSession:
    """One command process on a pseudo-terminal of a fixed size.

    Attributes:
        screen: What the terminal shows.
        exit_code: The exit code once the process ended, or a negative signal number.
        process_tree: Every process the command started, recorded about once a
            second while output is read.
    """

    def __init__(
        self,
        argv: Sequence[str],
        environment: Mapping[str, str],
        cwd: Path,
        transcript_path: Path,
        columns: int = 80,
        lines: int = 24,
    ) -> None:
        """Start the command."""
        self.screen = TerminalScreen(columns, lines)
        self.exit_code: int | None = None
        self._transcript: BinaryIO = transcript_path.open("ab")
        self._child = pexpect.spawn(
            argv[0],
            list(argv[1:]),
            cwd=str(cwd),
            env=dict(environment),
            dimensions=(lines, columns),
        )
        self.process_tree = ProcessTreeRecord(self.pid)
        self._last_process_record = 0.0

    @property
    def pid(self) -> int:
        """Return the command's process ID."""
        return int(self._child.pid)

    def is_running(self) -> bool:
        """Report whether the command still runs."""
        return self.exit_code is None and bool(self._child.isalive())

    def pump(self, seconds: float) -> None:
        """Read and render what the command writes for ``seconds``."""
        deadline = time.monotonic() + seconds
        while True:
            self._read_available(min(_POLL_SECONDS, max(0.0, deadline - time.monotonic())))
            if self.exit_code is not None or time.monotonic() >= deadline:
                return

    def wait_until(
        self,
        is_reached: Callable[[TerminalScreen], bool],
        description: str,
        timeout_seconds: float = 60.0,
    ) -> None:
        """Render output until ``is_reached`` holds; raise ``ScreenWaitTimeout`` on timeout.

        A command that exits first also raises, unless the state already holds.
        """
        deadline = time.monotonic() + timeout_seconds
        while True:
            if is_reached(self.screen):
                return
            if self.exit_code is not None:
                self._fail(f"the command exited with {self.exit_code} before {description}")
            if time.monotonic() >= deadline:
                self._fail(f"no {description} within {timeout_seconds:.0f} s")
            self._read_available(_POLL_SECONDS)

    def wait_for_text(self, text: str, timeout_seconds: float = 60.0) -> None:
        """Wait until a visible or scrolled-back line contains ``text``."""
        self.wait_until(
            lambda screen: any(text in line for line in screen.all_lines()),
            f"line with {text!r}",
            timeout_seconds,
        )

    def wait_for_output(self, text: str, timeout_seconds: float = 60.0) -> None:
        """Wait until the output, without escape sequences or line wrapping, contains ``text``."""
        self.wait_until(
            lambda screen: text in screen.output_text(), f"output {text!r}", timeout_seconds
        )

    def wait_for_display(self, text: str, timeout_seconds: float = 60.0) -> None:
        """Wait until a visible line contains ``text``."""
        self.wait_until(
            lambda screen: any(text in line for line in screen.display()),
            f"visible line with {text!r}",
            timeout_seconds,
        )

    def wait_for_display_gone(self, text: str, timeout_seconds: float = 60.0) -> None:
        """Wait until no visible line contains ``text``."""
        self.wait_until(
            lambda screen: not any(text in line for line in screen.display()),
            f"screen without {text!r}",
            timeout_seconds,
        )

    def type_text(self, text: str) -> None:
        """Type text without pressing Enter."""
        self._child.send(text)
        self.pump(_KEY_GAP_SECONDS)

    def press(self, key: str) -> None:
        """Press one named key from ``KEYS``."""
        self._child.send(KEYS[key])
        self.pump(_KEY_GAP_SECONDS)

    def submit(self, text: str) -> None:
        """Type a message and press Enter."""
        self.type_text(text)
        self.press("enter")

    def resize(self, columns: int, lines: int) -> None:
        """Resize the terminal; the command gets SIGWINCH."""
        self.screen.resize(columns, lines)
        self._child.setwinsize(lines, columns)

    def wait_for_exit(self, timeout_seconds: float = 60.0) -> int:
        """Render output until the command exits; return its exit code."""
        deadline = time.monotonic() + timeout_seconds
        while self.exit_code is None:
            if time.monotonic() >= deadline:
                self._fail(f"the command did not exit within {timeout_seconds:.0f} s")
            self._read_available(_POLL_SECONDS)
        return self.exit_code

    def close(self) -> None:
        """Stop the command if it still runs, as a closed terminal does, and close the transcript.

        The command gets SIGHUP and time to stop what it started, such as a
        ``dexcli dev``; only one that outlives that is killed.
        """
        if self.exit_code is None and self._child.isalive():
            self.process_tree.record()
            with contextlib.suppress(ProcessLookupError):
                os.kill(self.pid, signal.SIGHUP)
            try:
                self.wait_for_exit(_HANGUP_EXIT_SECONDS)
            except ScreenWaitTimeout:
                self._child.close(force=True)
                self._record_exit()
        self._transcript.close()

    def describe(self) -> str:
        """Return the visible screen and the scrollback tail, for failure messages."""
        tail = self.screen.scrollback()[-15:]
        return "\n".join(
            [
                f"--- scrollback tail ({len(tail)} lines) ---",
                *tail,
                f"--- screen {self.screen.columns}x{self.screen.lines} ---",
                *(f"|{line}" for line in self.screen.display()),
            ]
        )

    def _read_available(self, timeout_seconds: float) -> None:
        if self.exit_code is None and (
            time.monotonic() - self._last_process_record >= _PROCESS_RECORD_SECONDS
        ):
            self.process_tree.record()
            self._last_process_record = time.monotonic()
        if self.exit_code is not None:
            time.sleep(timeout_seconds)
            return
        try:
            data = self._child.read_nonblocking(_READ_CHUNK_BYTES, timeout=timeout_seconds)
        except pexpect.TIMEOUT:
            return
        except pexpect.EOF:
            self._child.close()
            self._record_exit()
            return
        self._transcript.write(data)
        self._transcript.flush()
        self.screen.feed(data)

    def _record_exit(self) -> None:
        if self._child.exitstatus is not None:
            self.exit_code = int(self._child.exitstatus)
        elif self._child.signalstatus is not None:
            self.exit_code = -int(self._child.signalstatus)
        else:
            self.exit_code = -1

    def _fail(self, problem: str) -> None:
        raise ScreenWaitTimeout(f"{problem}\n{self.describe()}")
