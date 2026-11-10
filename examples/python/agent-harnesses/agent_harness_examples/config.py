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

"""Configuration of the agent harness examples, read from environment variables."""

from __future__ import annotations

import os
import secrets
from dataclasses import dataclass, field
from pathlib import Path

from agent_harness_examples.demo_model_script import DEMO_MODEL

DEFAULT_CLAUDE_MODEL = "claude-opus-5-5"


@dataclass(frozen=True)
class AgentHarnessExamplesConfig:
    # The Dex server's gRPC address that the Worker and the Client connect to.
    server_address: str
    # The address the HTTP routes bind to.
    http_address: str
    blob_cache_dir: Path
    # Each model name is in its framework's own syntax; a "mock/" name is the local demo model.
    claude_agent_sdk_model: str = DEFAULT_CLAUDE_MODEL
    # Whether ANTHROPIC_API_KEY is set; the Claude Agent SDK agent is registered only then.
    has_anthropic_api_key: bool = False
    openai_agents_model: str = DEMO_MODEL
    pydantic_ai_model: str = DEMO_MODEL
    langchain_model: str = DEMO_MODEL
    google_adk_model: str = DEMO_MODEL
    # Signs session tokens. A random key ends every token when the process restarts.
    session_key: bytes = field(default_factory=lambda: secrets.token_bytes(32), repr=False)

    @staticmethod
    def from_env() -> AgentHarnessExamplesConfig:
        return AgentHarnessExamplesConfig(
            server_address=os.environ.get("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
            http_address=os.environ.get("DEX_AGENT_HARNESS_HTTP_ADDRESS", "127.0.0.1:8081"),
            blob_cache_dir=Path(
                os.environ.get("DEX_BLOB_CACHE_DIR", "/tmp/dex-agent-harness-examples-blob-cache")
            ),
            claude_agent_sdk_model=os.environ.get(
                "DEX_CLAUDE_AGENT_SDK_MODEL", DEFAULT_CLAUDE_MODEL
            ),
            has_anthropic_api_key=bool(os.environ.get("ANTHROPIC_API_KEY")),
            openai_agents_model=os.environ.get("DEX_OPENAI_AGENTS_MODEL", DEMO_MODEL),
            pydantic_ai_model=os.environ.get("DEX_PYDANTIC_AI_MODEL", DEMO_MODEL),
            langchain_model=os.environ.get("DEX_LANGCHAIN_MODEL", DEMO_MODEL),
            google_adk_model=os.environ.get("DEX_GOOGLE_ADK_MODEL", DEMO_MODEL),
            session_key=(
                session_key.encode()
                if (session_key := os.environ.get("DEX_AGENT_SESSION_KEY"))
                else secrets.token_bytes(32)
            ),
        )
