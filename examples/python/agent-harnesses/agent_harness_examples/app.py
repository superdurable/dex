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

"""Registers every order support agent's session Flow on one Worker and one Client."""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass

from agents import Model
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)
from dex_agents import AgentSessionFlow
from dex_connectors import slack
from google.adk.models.base_llm import BaseLlm
from langchain_core.language_models import BaseChatModel
from pydantic_ai.models import Model as PydanticAIModel

from agent_harness_examples.claude_agent_sdk_order_support import (
    build_claude_agent_sdk_order_support_flow,
)
from agent_harness_examples.config import AgentHarnessExamplesConfig
from agent_harness_examples.demo_model_script import is_demo_model
from agent_harness_examples.google_adk_demo_model import GoogleADKDemoModel
from agent_harness_examples.google_adk_order_support import build_google_adk_order_support_flow
from agent_harness_examples.langchain_demo_model import LangChainDemoChatModel
from agent_harness_examples.langchain_order_support import build_langchain_order_support_flow
from agent_harness_examples.openai_agents_demo_model import OpenAIAgentsDemoModel
from agent_harness_examples.openai_agents_order_support import (
    build_openai_agents_order_support_flow,
)
from agent_harness_examples.order_desk import OrderDesk
from agent_harness_examples.pydantic_ai_demo_model import build_pydantic_ai_demo_model
from agent_harness_examples.pydantic_ai_order_support import build_pydantic_ai_order_support_flow

PULL_REGISTRATION_TIMEOUT_SECONDS = 30.0
# Tool calls run as parallel Steps, so the Worker needs a slot for each one.
MAX_CONCURRENT_TASKS = 64

_logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class AgentHarnessExample:
    """One framework's order support agent: its URL segment, session Flow, and order system."""

    harness_name: str
    session_flow: AgentSessionFlow
    order_desk: OrderDesk


class AgentHarnessExamplesApp:
    def __init__(
        self, config: AgentHarnessExamplesConfig, slack_connection: slack.Connection | None
    ) -> None:
        """Builds every agent; ``slack_connection`` None leaves out the Slack connector tools.

        The caller owns ``slack_connection`` and closes it after ``close``.
        """
        self.config = config
        examples = [
            _new_openai_agents_example(config, slack_connection),
            _new_pydantic_ai_example(config, slack_connection),
            _new_langchain_example(config, slack_connection),
            _new_google_adk_example(config, slack_connection),
        ]
        if config.has_anthropic_api_key:
            examples.insert(0, _new_claude_agent_sdk_example(config, slack_connection))
        else:
            # Without a key, the Claude Code CLI could fall back to a developer's own login.
            _logger.warning("ANTHROPIC_API_KEY is not set; the Claude Agent SDK agent is off")
        self.examples = tuple(examples)

        # Agent session Steps are async, so the Registry allows async handlers.
        self.registry = Registry(
            tuple(example.session_flow for example in self.examples), allow_async_handlers=True
        )
        config.blob_cache_dir.mkdir(parents=True, exist_ok=True)
        self.blob_cache = open_blob_cache(BlobCacheConfig(str(config.blob_cache_dir), 1 << 30))
        self.worker = AsyncWorker(
            self.registry,
            self.blob_cache,
            WorkerOptions(
                server_address=config.server_address,
                pull=PullWorkerOptions(max_concurrent_tasks=MAX_CONCURRENT_TASKS),
            ),
        )
        self.client = AsyncClient(
            self.registry, self.blob_cache, ClientOptions(server_address=config.server_address)
        )
        self._worker_task: asyncio.Task[None] | None = None

    async def start_worker(self) -> None:
        self._worker_task = asyncio.create_task(self.worker.start())
        await self.worker.wait_pull_registered(timeout=PULL_REGISTRATION_TIMEOUT_SECONDS)

    async def close(self) -> None:
        """Closes the Client, the Worker, and the BlobCache; every step runs even after a failure.

        The caller owns the Slack connection and closes it.

        Raises:
            Exception: The first failure from a close step.
        """
        close_errors: list[Exception] = []
        try:
            await self.client.close()
        except Exception as error:
            close_errors.append(error)
        try:
            await self.worker.close()
        except Exception as error:
            close_errors.append(error)
        if self._worker_task is not None:
            try:
                await asyncio.wait_for(self._worker_task, timeout=10)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                self._worker_task.cancel()
            except Exception:
                _logger.exception("Worker task failed")
            self._worker_task = None
        try:
            self.blob_cache.close()
        except Exception as error:
            close_errors.append(error)
        for other_error in close_errors[1:]:
            _logger.error("another close step failed", exc_info=other_error)
        if close_errors:
            raise close_errors[0]


def _new_claude_agent_sdk_example(
    config: AgentHarnessExamplesConfig, slack_connection: slack.Connection | None
) -> AgentHarnessExample:
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    flow = build_claude_agent_sdk_order_support_flow(
        order_desk, config.claude_agent_sdk_model, slack_connection
    )
    return AgentHarnessExample("claude-agent-sdk", flow, order_desk)


def _new_openai_agents_example(
    config: AgentHarnessExamplesConfig, slack_connection: slack.Connection | None
) -> AgentHarnessExample:
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    model = _openai_agents_model(config.openai_agents_model)
    flow = build_openai_agents_order_support_flow(order_desk, model, slack_connection)
    return AgentHarnessExample("openai-agents", flow, order_desk)


def _new_pydantic_ai_example(
    config: AgentHarnessExamplesConfig, slack_connection: slack.Connection | None
) -> AgentHarnessExample:
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    model = _pydantic_ai_model(config.pydantic_ai_model)
    flow = build_pydantic_ai_order_support_flow(order_desk, model, slack_connection)
    return AgentHarnessExample("pydantic-ai", flow, order_desk)


def _new_langchain_example(
    config: AgentHarnessExamplesConfig, slack_connection: slack.Connection | None
) -> AgentHarnessExample:
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    model = _langchain_model(config.langchain_model)
    flow = build_langchain_order_support_flow(order_desk, model, slack_connection)
    return AgentHarnessExample("langchain", flow, order_desk)


def _new_google_adk_example(
    config: AgentHarnessExamplesConfig, slack_connection: slack.Connection | None
) -> AgentHarnessExample:
    order_desk = OrderDesk(config.order_desk_tool_delay_seconds)
    model = _google_adk_model(config.google_adk_model)
    flow = build_google_adk_order_support_flow(order_desk, model, slack_connection)
    return AgentHarnessExample("google-adk", flow, order_desk)


def _openai_agents_model(model: str) -> str | Model:
    return OpenAIAgentsDemoModel() if is_demo_model(model) else model


def _pydantic_ai_model(model: str) -> str | PydanticAIModel:
    return build_pydantic_ai_demo_model() if is_demo_model(model) else model


def _langchain_model(model: str) -> str | BaseChatModel:
    return LangChainDemoChatModel() if is_demo_model(model) else model


def _google_adk_model(model: str) -> str | BaseLlm:
    return GoogleADKDemoModel() if is_demo_model(model) else model
