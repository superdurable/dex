# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The emulator's additions to pyte, checked with byte strings a Bubble Tea UI sends."""

from __future__ import annotations

from dex_agents_cli_terminal_ui_e2e.terminal_screen import TerminalScreen


def test_keyboard_protocol_sequences_draw_nothing() -> None:
    screen = TerminalScreen(20, 3)
    screen.feed(b"\x1b[>1u\x1b[?u\x1b[>4;2m\x1b[?2026$p\x1b[1 qhello\x1b[<1u")
    assert screen.display()[0] == "hello"


def test_sequence_split_across_reads_is_applied_once_whole() -> None:
    screen = TerminalScreen(20, 3)
    screen.feed(b"ab\x1b[<")
    screen.feed(b"1ucd")
    assert screen.display()[0] == "abcd"


def test_scroll_up_moves_lines_into_scrollback() -> None:
    screen = TerminalScreen(10, 3)
    screen.feed(b"one\r\ntwo\r\nthree\x1b[2S")
    assert screen.display() == ["three", "", ""]
    assert screen.scrollback() == ["one", "two"]


def test_scroll_down_inside_margins_keeps_scrollback_empty() -> None:
    screen = TerminalScreen(10, 4)
    screen.feed(b"top\r\na\r\nb\r\nbottom\x1b[2;3r\x1b[1T")
    assert screen.display() == ["top", "", "a", "bottom"]
    assert screen.scrollback() == []


def test_alternate_screen_restores_the_main_screen() -> None:
    screen = TerminalScreen(10, 3)
    screen.feed(b"main")
    screen.feed(b"\x1b[?1049h\x1b[Hfull view")
    assert screen.is_alternate_screen
    assert screen.display()[0] == "full view"
    screen.feed(b"\x1b[?1049l")
    assert not screen.is_alternate_screen
    assert screen.display()[0] == "main"
    assert "full view" in screen.seen_lines()


def test_output_text_drops_escape_sequences() -> None:
    screen = TerminalScreen(20, 3)
    screen.feed(b"\x1b[1mbold\x1b[m \x1b]8;;http://x\x07link\x1b]8;;\x07\r\n")
    assert screen.output_text() == "bold link\n"
