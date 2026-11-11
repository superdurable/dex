# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The deprecated dex_agents.stepwise modules still work and warn on every name they serve."""

from __future__ import annotations

import importlib

import pytest

import dex_agents.builtin_loop as builtin_loop
from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ScriptedModelClient,
    ScriptedModelReply,
    UserTextItem,
    builtin_agent_session_flow,
    builtin_agent_session_options,
    decode_builtin_loop_message,
    encode_builtin_loop_message,
)

RENAMED_NAMES = {
    "StepwiseAgentRunner": "BuiltinLoopAgentRunner",
    "stepwise_agent_session_flow": "builtin_agent_session_flow",
    "stepwise_agent_session_options": "builtin_agent_session_options",
    "encode_stepwise_message": "encode_builtin_loop_message",
    "decode_stepwise_message": "decode_builtin_loop_message",
}


def scripted_definition() -> AgentDefinition:
    return AgentDefinition(
        name="agent",
        instructions="Help.",
        model=ScriptedModelClient(lambda request: ScriptedModelReply(text="Done.")),
    )


def test_every_old_package_name_is_the_new_object_and_warns() -> None:
    stepwise = importlib.import_module("dex_agents.stepwise")

    for old_name in stepwise.__all__:
        with pytest.warns(DeprecationWarning, match=rf"dex_agents\.stepwise\.{old_name} "):
            value = getattr(stepwise, old_name)
        assert value is getattr(builtin_loop, RENAMED_NAMES.get(old_name, old_name))


def test_old_import_statements_warn_and_build_the_same_flow() -> None:
    with pytest.warns(DeprecationWarning, match="builtin_agent_session_flow"):
        from dex_agents.stepwise import stepwise_agent_session_flow
    with pytest.warns(DeprecationWarning, match="builtin_agent_session_options"):
        from dex_agents.stepwise import stepwise_agent_session_options
    with pytest.warns(DeprecationWarning, match="BuiltinLoopAgentRunner"):
        from dex_agents.stepwise import StepwiseAgentRunner

    definition = scripted_definition()
    flow = stepwise_agent_session_flow(definition, flow_type="DeprecatedAliasAgent")

    assert isinstance(flow.runner, StepwiseAgentRunner)
    assert isinstance(flow.runner, BuiltinLoopAgentRunner)
    assert stepwise_agent_session_options(definition) == builtin_agent_session_options(definition)
    new_flow = builtin_agent_session_flow(definition, flow_type="DeprecatedAliasAgent")
    assert type(flow) is type(new_flow)


def test_old_message_codec_names_round_trip() -> None:
    with pytest.warns(DeprecationWarning):
        from dex_agents.stepwise import encode_stepwise_message
    with pytest.warns(DeprecationWarning):
        from dex_agents.stepwise.transcript import decode_stepwise_message

    assert encode_stepwise_message is encode_builtin_loop_message
    assert decode_stepwise_message is decode_builtin_loop_message


def test_old_submodules_keep_their_old_names() -> None:
    from dex_agents.stepwise import runner as old_runner

    assert old_runner.__name__ == "dex_agents.stepwise.runner"
    with pytest.warns(DeprecationWarning, match=r"dex_agents\.builtin_loop\.runner"):
        assert old_runner.StepwiseAgentRunner is BuiltinLoopAgentRunner
    for module_name in (
        "framework_tool_sources",
        "function_tools",
        "model",
        "scripted_model",
        "tool_sources",
    ):
        old_module = importlib.import_module(f"dex_agents.stepwise.{module_name}")
        new_module = importlib.import_module(f"dex_agents.builtin_loop.{module_name}")
        with pytest.warns(DeprecationWarning):
            old_names = [name for name in dir(new_module) if not name.startswith("_")]
            for name in old_names:
                assert getattr(old_module, name) is getattr(new_module, name)
    with pytest.warns(DeprecationWarning):
        assert old_runner.UserTextItem is UserTextItem


def test_unknown_old_names_raise_attribute_error_without_warning() -> None:
    stepwise = importlib.import_module("dex_agents.stepwise")

    with pytest.raises(AttributeError, match="dex_agents.stepwise"):
        _ = stepwise.NoSuchName
