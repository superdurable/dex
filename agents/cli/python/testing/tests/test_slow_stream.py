# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scenario step with ``stream_delay_seconds`` streams its text one word at a time."""

from __future__ import annotations

import json
import time
from pathlib import Path

from dex_agents_cli_testing.mock_model_api import MockModelApi
from dex_agents_cli_testing.model_api import ModelApiRequest
from dex_agents_cli_testing.model_scenario import ScenarioFile


def _mock(tmp_path: Path, delay_seconds: float) -> MockModelApi:
    scenario_path = tmp_path / "scenario.json"
    step = {"text": "one two three", "stream_delay_seconds": delay_seconds}
    scenario_path.write_text(json.dumps({"turns": [{"prompt": "go", "steps": [step]}]}))
    return MockModelApi(ScenarioFile(scenario_path), tmp_path / "requests.jsonl")


def test_responses_stream_sends_one_delta_per_word(tmp_path: Path) -> None:
    request = ModelApiRequest(
        "POST", "/v1/responses", {}, {"model": "m", "input": "go", "stream": True}
    )
    started = time.monotonic()
    events = list(_mock(tmp_path, 0.05).respond(request).events or [])
    deltas = [data["delta"] for name, data in events if name == "response.output_text.delta"]
    assert deltas == ["one ", "two ", "three"]
    assert time.monotonic() - started >= 0.1


def test_chat_completions_stream_without_delay_sends_the_text_at_once(tmp_path: Path) -> None:
    request = ModelApiRequest(
        "POST",
        "/v1/chat/completions",
        {},
        {"model": "m", "messages": [{"role": "user", "content": "go"}], "stream": True},
    )
    events = list(_mock(tmp_path, 0).respond(request).events or [])
    contents = [
        data["choices"][0]["delta"].get("content")
        for _, data in events
        if data.get("choices") and data["choices"][0]["delta"].get("content")
    ]
    assert contents == ["one two three"]
