# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import pytest

from dex_agents_cli_core.session_list import (
    SESSION_LIST_REQUEST_LIMIT,
    SESSION_LIST_ROW_LIMIT,
    collect_sessions_for_list,
)

SESSION_LIST_FIXTURE: dict[str, Any] = json.loads(
    (Path(__file__).resolve().parents[4] / "conformance" / "cli_session_list.json").read_text(
        encoding="utf-8"
    )
)


@dataclass(frozen=True)
class _FakePage:
    sessions: list[str]
    next_page_token: str


def _page_sessions(page_number: int, count: int) -> list[str]:
    return [f"page-{page_number}-session-{index}" for index in range(count)]


def test_the_limits_match_the_shared_fixture() -> None:
    assert SESSION_LIST_FIXTURE["row_limit"] == SESSION_LIST_ROW_LIMIT
    assert SESSION_LIST_FIXTURE["request_limit"] == SESSION_LIST_REQUEST_LIMIT


@pytest.mark.parametrize("case", SESSION_LIST_FIXTURE["cases"], ids=lambda case: case["name"])
async def test_sessions_load_pages_as_the_shared_cases_say(case: dict[str, Any]) -> None:
    pages: list[dict[str, Any]] = case["pages"]
    requested_tokens: list[str] = []

    async def load_page(next_page_token: str) -> _FakePage:
        page_number = len(requested_tokens)
        requested_tokens.append(next_page_token)
        assert page_number < len(pages), f"request {page_number + 1} goes past the last page"
        page = pages[page_number]
        return _FakePage(
            sessions=_page_sessions(page_number, page["count"]),
            next_page_token=f"token-{page_number + 1}" if page["has_next_page_token"] else "",
        )

    collected = await collect_sessions_for_list(load_page)

    assert len(requested_tokens) == case["requests"]
    expected_tokens = ["" if index == 0 else f"token-{index}" for index in range(case["requests"])]
    assert requested_tokens == expected_tokens
    expected = [
        session
        for page_number, page in enumerate(pages[: case["requests"]])
        for session in _page_sessions(page_number, page["count"])
    ][: case["rows"]]
    assert collected.sessions == expected
    assert collected.has_more_sessions is case["has_more"]
