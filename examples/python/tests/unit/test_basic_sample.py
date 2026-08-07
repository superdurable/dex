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

"""Construction smoke test for BasicFlow; the behaviour lives in tests/integ."""

from __future__ import annotations

from dex import Registry

from dex_examples.basic.basic_flow import BasicFlow, Increment, WaitForApproval


def test_basic_flow_declares_a_start_step() -> None:
    flow = BasicFlow()

    definitions = list(flow.get_steps())

    assert [definition.step for definition in definitions] == [
        flow.increment,
        flow.wait_for_approval,
    ]
    assert [definition.is_start_step for definition in definitions] == [True, False]
    assert isinstance(flow.increment, Increment)
    assert isinstance(flow.wait_for_approval, WaitForApproval)


def test_basic_flow_registers_its_attribute_channel_and_rpcs() -> None:
    flow = BasicFlow()

    registry = Registry((flow,))
    registered = registry._flow_for_instance(flow)

    assert {rpc.name for rpc in registered.rpcs.values()} == {
        "append_string",
        "approve",
    }
    assert flow.test_string.name == BasicFlow.DA_TEST_STRING
    assert flow.approval.name == BasicFlow.CHANNEL_APPROVAL
