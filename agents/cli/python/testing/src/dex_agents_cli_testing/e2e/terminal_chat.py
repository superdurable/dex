# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Drive one command's chat in a pseudo-terminal, in either CLI core's output format."""

from __future__ import annotations

import re
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import TextIO

import pexpect

# Python: "Approve? [y/N, or n <reason>] "; TypeScript: "Approve <tool>? [y/n, or n <reason>]: ".
APPROVAL_PROMPT = re.compile(r"Approve(?: \S+)?\? \[y/[nN], or n <reason>\]:? ?")
SESSION_LINE = re.compile(r"Session (\S+?)(?:\.\s|\s)")
RESUME_LINE = re.compile(r"--resume (\S+)")
TURN_FAILURE = re.compile(r"\[(?:turn|session) failed\][^\n]*")
CHAT_PROMPT = "\n> "
_TRANSCRIPT_TAIL_CHARACTERS = 1500


@dataclass
class TurnOutcome:
    """What the terminal showed for one prompt.

    Attributes:
        approval_prompts: The text before each approval prompt, newest last.
        reply: The text from the reply marker to the next chat prompt, or ``""``.
        failure: A failure line, a timeout, or an early exit, or ``None``.
    """

    approval_prompts: list[str] = field(default_factory=list)
    reply: str = ""
    failure: str | None = None


class TerminalChat:
    """One command process in a pseudo-terminal, with its transcript in a file."""

    def __init__(
        self,
        argv: Sequence[str],
        environment: Mapping[str, str],
        cwd: Path,
        transcript_path: Path,
        timeout_seconds: float,
    ) -> None:
        """Start the command."""
        self._transcript: TextIO = transcript_path.open("a", encoding="utf-8")
        self._timeout_seconds = timeout_seconds
        self.child = pexpect.spawn(
            argv[0],
            list(argv[1:]),
            cwd=str(cwd),
            env=dict(environment),
            encoding="utf-8",
            codec_errors="replace",
            timeout=timeout_seconds,
            dimensions=(50, 400),
        )
        self.child.logfile_read = self._transcript

    def wait_for_session(self) -> str:
        """Wait for the session line and the first chat prompt; return the session ID."""
        self.child.expect(SESSION_LINE)
        session_id = str(self.child.match.group(1))
        self.child.expect_exact(CHAT_PROMPT)
        return session_id

    def send_turn(
        self,
        prompt: str,
        reply_marker: str,
        approval_answer: str | None,
        before_answer: Callable[[], None] | None = None,
    ) -> TurnOutcome:
        """Send a prompt and answer each approval until the reply marker and the next prompt.

        An approval prompt with no ``approval_answer`` is rejected and recorded.
        """
        outcome = TurnOutcome()
        if not self.child.isalive():
            outcome.failure = f"the command is no longer running; tail: {self.tail()}"
            return outcome
        self.child.sendline(prompt)
        while True:
            index = self.child.expect(
                [
                    APPROVAL_PROMPT,
                    re.escape(reply_marker),
                    TURN_FAILURE,
                    pexpect.EOF,
                    pexpect.TIMEOUT,
                ]
            )
            if index == 0:
                outcome.approval_prompts.append(str(self.child.before)[-600:])
                if before_answer is not None:
                    before_answer()
                self.child.sendline(approval_answer or "n the e2e check expected no approval")
                continue
            if index == 1:
                try:
                    self.child.expect_exact(CHAT_PROMPT)
                except (pexpect.EOF, pexpect.TIMEOUT):
                    outcome.failure = f"no chat prompt after the reply; tail: {self.tail()}"
                outcome.reply = reply_marker + str(self.child.before)
                return outcome
            if index == 2:
                outcome.failure = str(self.child.after)
                try:
                    self.child.expect_exact(CHAT_PROMPT)
                except (pexpect.EOF, pexpect.TIMEOUT):
                    pass
                return outcome
            event = (
                "the command exited" if index == 3 else f"no reply within {self._timeout_seconds}s"
            )
            outcome.failure = f"{event}; tail: {self.tail()}"
            return outcome

    def exit_chat(self) -> tuple[int, str | None]:
        """Send ``/exit``; return the exit code and the session ID of the ``--resume`` line."""
        if not self.child.isalive():
            self.child.close()
            return int(self.child.exitstatus if self.child.exitstatus is not None else -1), None
        self.child.sendline("/exit")
        try:
            self.child.expect(pexpect.EOF, timeout=60)
        except pexpect.TIMEOUT:
            self.close()
            return -1, None
        tail = str(self.child.before)
        self.child.close()
        match = RESUME_LINE.search(tail)
        exit_code = self.child.exitstatus if self.child.exitstatus is not None else -1
        return int(exit_code), match.group(1).rstrip(".") if match else None

    def tail(self) -> str:
        """Return the end of what the terminal showed since the last match."""
        before = self.child.before if isinstance(self.child.before, str) else ""
        return before[-_TRANSCRIPT_TAIL_CHARACTERS:].replace("\r", "")

    def close(self) -> None:
        """Kill the command if it still runs and close the transcript."""
        if self.child.isalive():
            self.child.close(force=True)
        self._transcript.close()
