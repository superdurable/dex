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

"""Durable value types for the AI Agent example."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


DEFAULT_MODEL = "mock/dex"
DEFAULT_SYSTEM_PROMPT = (
    "You are a helpful durable AI agent. Use tools when they help, explain "
    "important actions, and never claim a tool succeeded unless its result says so."
)
DEFAULT_CONTEXT_TOKENS = 32_000
DEFAULT_MESSAGE_RETENTION = 2_000


@dataclass(frozen=True)
class AgentConfig:
    model: str = DEFAULT_MODEL
    system_prompt: str = DEFAULT_SYSTEM_PROMPT
    compaction_model: str | None = None
    max_context_tokens: int = DEFAULT_CONTEXT_TOKENS
    compaction_trigger_fraction: float = 0.85
    compaction_keep_fraction: float = 0.10
    message_retention_limit: int = DEFAULT_MESSAGE_RETENTION
    mcp_enabled: bool = True
    enabled_mcp_servers: list[str] = field(default_factory=list)
    enabled_tools: list[str] = field(default_factory=list)

    def validate(self) -> None:
        if not self.model.strip():
            raise ValueError("model must not be empty")
        if not self.system_prompt.strip():
            raise ValueError("system_prompt must not be empty")
        if self.max_context_tokens <= 0:
            raise ValueError("max_context_tokens must be positive")
        if not 0 < self.compaction_keep_fraction < self.compaction_trigger_fraction < 1:
            raise ValueError(
                "compaction fractions must satisfy 0 < keep < trigger < 1"
            )
        if self.message_retention_limit <= 0:
            raise ValueError("message_retention_limit must be positive")


@dataclass(frozen=True)
class ToolCall:
    id: str
    name: str
    arguments_json: str


@dataclass(frozen=True)
class AgentMessage:
    role: str
    content: str
    tool_calls: list[ToolCall] = field(default_factory=list)
    tool_call_id: str | None = None
    tool_name: str | None = None


@dataclass(frozen=True)
class AgentState:
    next_sequence: int = 1
    first_retained_sequence: int = 1
    last_sequence: int = 0
    summarized_through_sequence: int = 0
    compaction_generation: int = 0
    status: str = "initializing"
    pending_tool_calls: list[ToolCall] = field(default_factory=list)
    pending_tool_index: int = 0
    plan_revision: int = 0
    interaction_mode: str = "chat"
    planning_requires_write: bool = False
    planning_allows_write: bool = False
    pending_plan_execution_revision: int | None = None
    pending_user_message_count: int = 0


@dataclass(frozen=True)
class ContextSummary:
    generation: int
    summarized_through_sequence: int
    content: str


@dataclass(frozen=True)
class UserMessage:
    content: str
    plan_mode: bool = False


@dataclass(frozen=True)
class PlanTask:
    content: str
    status: str


@dataclass(frozen=True)
class AgentPlan:
    revision: int
    status: str
    tasks: list[PlanTask]


@dataclass(frozen=True)
class PlanExecutionRequest:
    revision: int


@dataclass(frozen=True)
class ToolApproval:
    approved: bool


@dataclass(frozen=True)
class ToolApprovalRequest:
    call_id: str
    approved: bool


@dataclass(frozen=True)
class PendingApproval:
    call_id: str
    tool_name: str
    arguments_json: str


@dataclass(frozen=True)
class PendingTimer:
    call_id: str
    duration_seconds: int
    reason: str


@dataclass(frozen=True)
class PendingUserInput:
    call_id: str
    prompt: str


@dataclass(frozen=True)
class AgentEvent:
    kind: str
    message: str
    call_id: str | None = None
    tool_name: str | None = None


@dataclass(frozen=True)
class HistoryRequest:
    before_sequence: int | None = None
    limit: int = 50


@dataclass(frozen=True)
class SequencedMessage:
    sequence: int
    message: AgentMessage


@dataclass(frozen=True)
class HistoryPage:
    messages: list[SequencedMessage]
    next_before_sequence: int | None


@dataclass(frozen=True)
class AgentDescription:
    status: str
    model: str
    system_prompt: str
    first_retained_sequence: int
    last_sequence: int
    summarized_through_sequence: int
    pending_approval_call_id: str | None
    pending_approval_tool_name: str | None
    pending_approval_arguments_json: str | None
    pending_timer_call_id: str | None
    pending_timer_duration_seconds: int | None
    pending_timer_reason: str | None
    pending_user_input_call_id: str | None
    pending_user_input_prompt: str | None
    plan: dict[str, Any] | None
    plan_execution_requested: bool
    available_mcp_servers: list[str]
    available_tools: list[str]


@dataclass(frozen=True)
class ModelReply:
    content: str
    tool_calls: list[ToolCall] = field(default_factory=list)


@dataclass(frozen=True)
class ToolDefinition:
    name: str
    description: str
    input_schema: dict[str, object]
    requires_approval: bool
    timeout_seconds: float
    maximum_attempts: int
    retry_total_seconds: float


@dataclass(frozen=True)
class ToolExecutionResult:
    content: str
    is_error: bool
