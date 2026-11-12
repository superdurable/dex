# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Picks one framework's quick start agent, on the local demo model unless one is set."""

from __future__ import annotations

from dex_agents import AgentSessionFlow

from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.demo_model_script import is_demo_model
from agent_harness_examples.google_adk_demo_model import GoogleADKDemoModel
from agent_harness_examples.langchain_demo_model import LangChainDemoChatModel
from agent_harness_examples.openai_agents_demo_model import OpenAIAgentsDemoModel
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.pydantic_ai_demo_model import build_pydantic_ai_demo_model
from agent_harness_examples.quick_start import (
    claude_agent_sdk_quick_start,
    google_adk_quick_start,
    langchain_quick_start,
    openai_agents_quick_start,
    pydantic_ai_quick_start,
)


def build_quick_start_agent(
    framework: str, order_desk: OrderDesk, config: AgentHarnessExamplesConfig
) -> AgentSessionFlow:
    """Returns the durable agent of one framework, on the local demo model unless one is set."""
    if framework == "claude-agent-sdk":
        return claude_agent_sdk_quick_start.build_durable_agent(
            order_desk, config.claude_agent_sdk_model
        )
    if framework == "google-adk":
        model = config.google_adk_model
        return google_adk_quick_start.build_durable_agent(
            order_desk, GoogleADKDemoModel() if is_demo_model(model) else model
        )
    if framework == "langchain":
        model = config.langchain_model
        return langchain_quick_start.build_durable_agent(
            order_desk, LangChainDemoChatModel() if is_demo_model(model) else model
        )
    if framework == "openai-agents":
        model = config.openai_agents_model
        return openai_agents_quick_start.build_durable_agent(
            order_desk, OpenAIAgentsDemoModel() if is_demo_model(model) else model
        )
    if framework == "pydantic-ai":
        model = config.pydantic_ai_model
        return pydantic_ai_quick_start.build_durable_agent(
            order_desk, build_pydantic_ai_demo_model() if is_demo_model(model) else model
        )
    raise ValueError(f"unknown framework {framework!r}; use one of {', '.join(FRAMEWORKS)}")


FRAMEWORKS = ("claude-agent-sdk", "google-adk", "langchain", "openai-agents", "pydantic-ai")
