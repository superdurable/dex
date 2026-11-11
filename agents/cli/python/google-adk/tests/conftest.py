# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fixtures: the command against the scripted Gemini API, with a private home.

The Google Gen AI SDK sends Gemini API requests to ``GOOGLE_GEMINI_BASE_URL``.
No ``GOOGLE*``, ``GEMINI*``, or other provider variable of the host reaches the command.
"""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path

import pytest

from dex_agents.testing import PrivateDexEngine
from dex_agents_cli_testing.commands import CommandHome
from dex_agents_cli_testing.gemini_api import GeminiApiStub

pytest_plugins = ["dex_agents_cli_testing.fixtures"]

MOCK_API_KEY = "mock-google-adk-cli-key"


@pytest.fixture
def command_home(
    tmp_path: Path, shared_engine: PrivateDexEngine, gemini_api: GeminiApiStub
) -> Iterator[CommandHome]:
    """Give a test a private home whose commands reuse the shared dexcli dev."""
    home = CommandHome(
        home=tmp_path / "home",
        command_module="dex_agents_google_adk_cli",
        harness_name="google-adk",
        dex_port=shared_engine.dex_port,
        web_port=shared_engine.web_port,
        model_api=gemini_api,
        model_environment={
            "GOOGLE_API_KEY": MOCK_API_KEY,
            "GOOGLE_GEMINI_BASE_URL": gemini_api.base_url,
            "GOOGLE_GENAI_USE_VERTEXAI": "false",
        },
    )
    home.home.mkdir()
    yield home
    home.close()
