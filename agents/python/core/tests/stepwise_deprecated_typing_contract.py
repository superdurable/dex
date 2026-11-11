# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Typing contract checked by mypy: the deprecated dex_agents.stepwise paths keep their exact types.

Never imported at run time; ``assert_type`` fails mypy when an old name resolves to ``Any``.
"""

from typing import assert_type

from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ModelInputItem,
    UserTextItem,
)
from dex_agents.runner import FrameworkMessage
from dex_agents.session_flow import AgentSessionFlow, AgentSessionOptions
from dex_agents.stepwise import (
    StepwiseAgentRunner,
    decode_stepwise_message,
    encode_stepwise_message,
    stepwise_agent_session_flow,
    stepwise_agent_session_options,
)
from dex_agents.stepwise.runner import StepwiseAgentRunner as RunnerOfRunnerModule
from dex_agents.stepwise.runner import stepwise_agent_session_flow as flow_of_runner_module
from dex_agents.stepwise.runner import stepwise_agent_session_options as options_of_runner_module
from dex_agents.stepwise.transcript import decode_stepwise_message as decode_of_transcript_module
from dex_agents.stepwise.transcript import encode_stepwise_message as encode_of_transcript_module
from dex_agents.values import AgentMessage


def check_package_names(definition: AgentDefinition, message: AgentMessage) -> None:
    runner = StepwiseAgentRunner(definition)
    assert_type(runner, BuiltinLoopAgentRunner)
    assert_type(stepwise_agent_session_flow(definition, flow_type="Agent"), AgentSessionFlow)
    assert_type(stepwise_agent_session_options(definition), AgentSessionOptions)
    assert_type(encode_stepwise_message(UserTextItem(text="hi")), FrameworkMessage)
    assert_type(decode_stepwise_message(message), ModelInputItem)


def check_submodule_names(definition: AgentDefinition, message: AgentMessage) -> None:
    assert_type(RunnerOfRunnerModule(definition), BuiltinLoopAgentRunner)
    assert_type(flow_of_runner_module(definition, flow_type="Agent"), AgentSessionFlow)
    assert_type(options_of_runner_module(definition), AgentSessionOptions)
    assert_type(encode_of_transcript_module(UserTextItem(text="hi")), FrameworkMessage)
    assert_type(decode_of_transcript_module(message), ModelInputItem)
