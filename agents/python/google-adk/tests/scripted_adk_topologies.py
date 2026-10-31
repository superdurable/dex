# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Multi-agent ADK trees with scripted models, one Flow type per tree."""

from __future__ import annotations

from collections.abc import AsyncGenerator, Sequence

from google.adk.agents import BaseAgent, LlmAgent, ParallelAgent, SequentialAgent
from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.models.base_llm import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.adk.tools.agent_tool import AgentTool
from google.adk.tools.base_tool import BaseTool
from google.adk.tools.base_toolset import BaseToolset
from google.adk.tools.function_tool import FunctionTool
from google.genai import types

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents_google_adk import adk_agent_session_flow
from tests.scripted_adk_agent import (
    MODEL_CALL_LOG_NAME,
    ScriptedModel,
    ScriptedReviewerModel,
    _describe_tool_response,
    _function_call_response,
    _function_responses,
    _invocations,
    _text_response,
    _user_texts,
)

PARALLEL_RESEARCH_FLOW_TYPE = "ScriptedAdkParallelResearch"
SINGLE_TURN_DELEGATION_FLOW_TYPE = "ScriptedAdkSingleTurnDelegation"
TASK_DELEGATION_FLOW_TYPE = "ScriptedAdkTaskDelegation"
READ_AGENT_TOOL_FLOW_TYPE = "ScriptedAdkReadAgentTool"
NESTED_PARALLEL_FLOW_TYPE = "ScriptedAdkNestedParallel"
TWO_SINGLE_TURN_DELEGATIONS_FLOW_TYPE = "ScriptedAdkTwoSingleTurnDelegations"
TOOLSET_NAME_CLASH_FLOW_TYPE = "ScriptedAdkToolsetNameClash"
PARALLEL_OF_SEQUENTIAL_FLOW_TYPE = "ScriptedAdkParallelOfSequential"
TASK_QUESTION = "Shall I finish? (yes/no)"
BRANCH_TOOL_POLICIES = {
    "record_a": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "record_b": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "record_c": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
}


class ScriptedBranchModel(BaseLlm):
    """Call one tool per user message, then report its result and how many it has seen."""

    model: str = "scripted-branch-model"
    tool_name: str

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        _invocations().record(MODEL_CALL_LOG_NAME)
        command_index, command = _user_texts(llm_request.contents)[-1]
        responses = _named_responses(llm_request.contents[command_index + 1 :], self.tool_name)
        if not responses:
            yield _function_call_response([(self.tool_name, {"note": command})])
            return
        seen = len(_named_responses(llm_request.contents, self.tool_name))
        yield _text_response(
            f"{self.tool_name}: {_describe_tool_response(responses[-1].response or {})}; "
            f"results seen: {seen}"
        )


class ScriptedDelegatingModel(BaseLlm):
    """Hand the user message to sub-agent tools in one response, then report their answers."""

    model: str = "scripted-delegating-model"
    delegate_names: list[str]

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        _invocations().record(MODEL_CALL_LOG_NAME)
        command_index, command = _user_texts(llm_request.contents)[-1]
        later_contents = llm_request.contents[command_index + 1 :]
        answers = [_named_responses(later_contents, name) for name in self.delegate_names]
        if not all(answers):
            yield _function_call_response(
                [(name, {"request": command}) for name in self.delegate_names]
            )
            return
        yield _text_response(
            "; ".join(
                f"{name}: {_describe_tool_response(responses[-1].response or {})}"
                for name, responses in zip(self.delegate_names, answers, strict=True)
            )
        )


class ScriptedHelperModel(BaseLlm):
    """Call one tool, then answer; a task-mode helper also asks before it finishes."""

    model: str = "scripted-helper-model"
    tool_name: str
    asks_before_finishing: bool = False

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        _invocations().record(MODEL_CALL_LOG_NAME)
        responses = _named_responses(llm_request.contents, self.tool_name)
        if not responses:
            yield _function_call_response([(self.tool_name, {"message": "report"})])
            return
        result = _describe_tool_response(responses[-1].response or {})
        if not self.asks_before_finishing:
            yield _text_response(f"helper done: {result}")
            return
        replies = [text for _, text in _user_texts(llm_request.contents)][1:]
        if "yes" not in replies:
            yield _text_response(TASK_QUESTION)
            return
        yield _function_call_response(
            [("finish_task", {"result": f"finished after {result}; replies: {', '.join(replies)}"})]
        )


def record_a(note: str) -> str:
    """Record branch A's finding."""
    _invocations().record("record_a")
    return f"recorded a: {note}"


def record_b(note: str) -> str:
    """Record branch B's finding."""
    _invocations().record("record_b")
    return f"recorded b: {note}"


def record_c(note: str) -> str:
    """Record step C's finding."""
    _invocations().record("record_c")
    return f"recorded c: {note}"


def file_report(message: str) -> str:
    """File a report."""
    _invocations().record("file_report")
    return f"filed {message}"


def file_report_one(message: str) -> str:
    """File the first report."""
    _invocations().record("file_report_one")
    return f"filed one {message}"


def file_report_two(message: str) -> str:
    """File the second report."""
    _invocations().record("file_report_two")
    return f"filed two {message}"


def file_charge(message: str) -> str:
    """File a charge."""
    _invocations().record("file_charge")
    return f"charged {message}"


def save_draft(message: str) -> str:
    """Save a draft."""
    _invocations().record("save_draft")
    return f"saved {message}"


def build_parallel_research_flow() -> AgentSessionFlow:
    branches: list[BaseAgent] = [
        LlmAgent(
            name=f"researcher_{letter}",
            model=ScriptedBranchModel(tool_name=tool.__name__),
            tools=[tool],
        )
        for letter, tool in (("a", record_a), ("b", record_b))
    ]
    return adk_agent_session_flow(
        ParallelAgent(name="research", sub_agents=branches),
        flow_type=PARALLEL_RESEARCH_FLOW_TYPE,
        tool_policies=BRANCH_TOOL_POLICIES,
    )


def build_single_turn_delegation_flow() -> AgentSessionFlow:
    helper = LlmAgent(
        name="helper",
        mode="single_turn",
        description="Files reports.",
        model=ScriptedHelperModel(tool_name="file_report"),
        tools=[file_report],
    )
    return adk_agent_session_flow(
        LlmAgent(
            name="coordinator",
            model=ScriptedDelegatingModel(delegate_names=["helper"]),
            sub_agents=[helper],
        ),
        flow_type=SINGLE_TURN_DELEGATION_FLOW_TYPE,
        tool_policies={
            "file_report": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False)
        },
    )


def build_task_delegation_flow() -> AgentSessionFlow:
    approver = LlmAgent(
        name="approver",
        mode="task",
        description="Files a charge, then asks before it finishes.",
        model=ScriptedHelperModel(tool_name="file_charge", asks_before_finishing=True),
        tools=[file_charge],
    )
    return adk_agent_session_flow(
        LlmAgent(
            name="task_coordinator",
            model=ScriptedDelegatingModel(delegate_names=["approver"]),
            sub_agents=[approver],
        ),
        flow_type=TASK_DELEGATION_FLOW_TYPE,
        tool_policies={"file_charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE)},
    )


def build_read_agent_tool_flow() -> AgentSessionFlow:
    """A read AgentTool whose agent calls a read AgentTool whose agent writes."""
    saving_agent = LlmAgent(
        name="saving_agent",
        description="Saves a draft.",
        model=ScriptedHelperModel(tool_name="save_draft"),
        tools=[save_draft],
    )
    drafting_agent = LlmAgent(
        name="drafting_agent",
        description="Drafts a document and has it saved.",
        model=ScriptedHelperModel(tool_name="saving_agent"),
        tools=[AgentTool(saving_agent)],
    )
    return adk_agent_session_flow(
        LlmAgent(
            name="planner",
            model=ScriptedDelegatingModel(delegate_names=["drafting_agent"]),
            tools=[AgentTool(drafting_agent)],
        ),
        flow_type=READ_AGENT_TOOL_FLOW_TYPE,
        tool_policies={
            "drafting_agent": ToolPolicy(ToolEffect.READ),
            "saving_agent": ToolPolicy(ToolEffect.READ),
        },
    )


def build_nested_parallel_flow() -> AgentSessionFlow:
    """Two branches of a ParallelAgent that a SequentialAgent runs before a summarizer."""
    branches: list[BaseAgent] = [
        LlmAgent(
            name=f"branch_{letter}",
            model=ScriptedBranchModel(tool_name=tool.__name__),
            tools=[tool],
        )
        for letter, tool in (("a", record_a), ("b", record_b))
    ]
    return adk_agent_session_flow(
        SequentialAgent(
            name="branching_pipeline",
            sub_agents=[
                ParallelAgent(name="branches", sub_agents=branches),
                LlmAgent(name="summarizer", model=ScriptedReviewerModel()),
            ],
        ),
        flow_type=NESTED_PARALLEL_FLOW_TYPE,
        tool_policies=BRANCH_TOOL_POLICIES,
    )


def build_parallel_of_sequential_flow() -> AgentSessionFlow:
    """A root ParallelAgent whose first branch is a SequentialAgent of two agents."""
    ordered: list[BaseAgent] = [
        LlmAgent(
            name=f"ordered_{position}",
            model=ScriptedBranchModel(tool_name=tool.__name__),
            tools=[tool],
        )
        for position, tool in (("first", record_a), ("second", record_c))
    ]
    branches: list[BaseAgent] = [
        SequentialAgent(name="ordered", sub_agents=ordered),
        LlmAgent(
            name="branch_b", model=ScriptedBranchModel(tool_name="record_b"), tools=[record_b]
        ),
    ]
    return adk_agent_session_flow(
        ParallelAgent(name="fanout", sub_agents=branches),
        flow_type=PARALLEL_OF_SEQUENTIAL_FLOW_TYPE,
        tool_policies=BRANCH_TOOL_POLICIES,
    )


def build_two_single_turn_delegations_flow() -> AgentSessionFlow:
    reporters: list[BaseAgent] = [
        LlmAgent(
            name=f"reporter_{suffix}",
            mode="single_turn",
            description="Files a report.",
            model=ScriptedHelperModel(tool_name=tool.__name__),
            tools=[tool],
        )
        for suffix, tool in (("one", file_report_one), ("two", file_report_two))
    ]
    return adk_agent_session_flow(
        LlmAgent(
            name="dispatcher",
            model=ScriptedDelegatingModel(delegate_names=["reporter_one", "reporter_two"]),
            sub_agents=reporters,
        ),
        flow_type=TWO_SINGLE_TURN_DELEGATIONS_FLOW_TYPE,
        tool_policies={
            "file_report_one": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
            "file_report_two": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
        },
    )


def build_toolset_name_clash_flow() -> AgentSessionFlow:
    """A root agent and its sub-agent's toolset give different tools the name record_a."""
    return adk_agent_session_flow(
        LlmAgent(
            name="toolset_root",
            model=ScriptedModel(),
            tools=[record_a],
            sub_agents=[
                LlmAgent(
                    name="toolset_child",
                    model=ScriptedBranchModel(tool_name="record_a"),
                    tools=[ClashingRecordToolset()],
                )
            ],
        ),
        flow_type=TOOLSET_NAME_CLASH_FLOW_TYPE,
        tool_policies={"record_a": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE)},
    )


class ClashingRecordToolset(BaseToolset):
    """A toolset whose only tool takes a name another agent's tool already has."""

    async def get_tools(self, readonly_context: ReadonlyContext | None = None) -> list[BaseTool]:
        return [FunctionTool(_record_a_from_toolset)]

    async def close(self) -> None:
        return None


def _record_a_from_toolset(note: str) -> str:
    """Record a note through the toolset."""
    _invocations().record("record_a_from_toolset")
    return f"toolset recorded: {note}"


_record_a_from_toolset.__name__ = "record_a"


def build_topology_flows() -> list[AgentSessionFlow]:
    return [
        build_parallel_research_flow(),
        build_single_turn_delegation_flow(),
        build_task_delegation_flow(),
        build_read_agent_tool_flow(),
        build_nested_parallel_flow(),
        build_parallel_of_sequential_flow(),
        build_two_single_turn_delegations_flow(),
        build_toolset_name_clash_flow(),
    ]


def _named_responses(
    contents: Sequence[types.Content], tool_name: str
) -> list[types.FunctionResponse]:
    return [response for response in _function_responses(contents) if response.name == tool_name]
