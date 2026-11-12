# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Claude Code CLI's standard error in the command's log file.

These drive ``ClaudeCodeStderrLog`` directly: the command opens its log file at
startup, so a chat process cannot make that file unwritable later, and the CLI
writes control sequences only on demand of a real terminal.
"""

from __future__ import annotations

import logging
import stat
from pathlib import Path

import pytest

from dex_agents_claude_sdk_cli.harness import ClaudeCodeStderrLog

LOG_FORMAT = "%(levelname)s %(name)s: %(message)s"


def test_control_sequences_never_reach_the_log(tmp_path: Path) -> None:
    log_path = tmp_path / "claude-sdk.log"
    stderr_log = ClaudeCodeStderrLog(log_path, logging.Formatter(LOG_FORMAT))
    for line in [
        "\x1b[?25h",
        "\x1b]0;Claude Code\x07starting",
        "see \x1b]8;;https://example.com\x1b\\the docs\x1b]8;;\x1b\\",
        "\x1b7saved\x1b8 \x1b(Bcharset\r",
        "\x1b[31mwarning:\x1b[0m slow network",
        "plain line",
    ]:
        stderr_log.write_line(line)

    prefix = "INFO dex_agents_claude_sdk_cli.harness: Claude Code stderr: "
    assert log_path.read_text(encoding="utf-8").splitlines() == [
        f"{prefix}starting",
        f"{prefix}see the docs",
        f"{prefix}saved charset",
        f"{prefix}warning: slow network",
        f"{prefix}plain line",
    ]


@pytest.mark.parametrize("is_missing_directory", [True, False])
def test_an_unwritable_log_drops_the_line_without_writing_to_the_terminal(
    tmp_path: Path, capfd: pytest.CaptureFixture[str], is_missing_directory: bool
) -> None:
    log_directory = tmp_path / "logs"
    if not is_missing_directory:
        log_directory.mkdir()
        log_directory.chmod(stat.S_IRUSR | stat.S_IXUSR)
    try:
        stderr_log = ClaudeCodeStderrLog(
            log_directory / "claude-sdk.log", logging.Formatter(LOG_FORMAT)
        )
        stderr_log.write_line("Claude Code failed to start")
    finally:
        if not is_missing_directory:
            log_directory.chmod(stat.S_IRWXU)

    captured = capfd.readouterr()
    assert (captured.out, captured.err) == ("", "")
    assert not (log_directory / "claude-sdk.log").exists()
