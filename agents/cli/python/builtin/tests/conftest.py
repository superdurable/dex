# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Fixtures: the command against the scripted Responses API, with a private home.

LiteLLM sends ``openai/`` models to the Responses API at ``OPENAI_BASE_URL``.
No ``OPENAI*`` or other provider variable of the host reaches the command.
"""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path

import pytest

from dex_agents.testing import PrivateDexEngine
from dex_agents_cli_testing.commands import CommandHome
from dex_agents_cli_testing.responses_api import ResponsesApiStub

pytest_plugins = ["dex_agents_cli_testing.fixtures"]

MOCK_API_KEY = "sk-mock-builtin-cli-key"


@pytest.fixture
def command_home(
    tmp_path: Path, shared_engine: PrivateDexEngine, responses_api: ResponsesApiStub
) -> Iterator[CommandHome]:
    """Give a test a private home whose commands reuse the shared dexcli dev."""
    home = CommandHome(
        home=tmp_path / "home",
        command_module="dex_agents_builtin_cli",
        harness_name="builtin",
        dex_port=shared_engine.dex_port,
        web_port=shared_engine.web_port,
        model_api=responses_api,
        model_environment={
            "OPENAI_API_KEY": MOCK_API_KEY,
            "OPENAI_BASE_URL": f"{responses_api.base_url}/v1",
            "LITELLM_LOCAL_MODEL_COST_MAP": "True",
        },
    )
    home.home.mkdir()
    yield home
    home.close()
