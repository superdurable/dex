# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The runner refuses an agent whose runs would all fail, before any Step runs."""

from __future__ import annotations

import pytest
from pydantic_ai import Agent, DeferredToolRequests
from pydantic_ai.agent import WrapperAgent
from pydantic_ai.models.test import TestModel

from dex_agents_pydantic_ai import PydanticAIRunner


def test_a_wrapped_agent_with_output_validators_must_list_deferred_tool_requests() -> None:
    agent = Agent(TestModel(), output_type=str)

    @agent.output_validator
    def keep_output(output: str) -> str:
        return output

    with pytest.raises(ValueError, match="DeferredToolRequests"):
        PydanticAIRunner(WrapperAgent(agent))

    listed = Agent(TestModel(), output_type=[str, DeferredToolRequests])

    @listed.output_validator
    def keep_listed_output(output: str) -> str:
        return output

    assert PydanticAIRunner(WrapperAgent(listed)).agent.output_type == [str, DeferredToolRequests]
