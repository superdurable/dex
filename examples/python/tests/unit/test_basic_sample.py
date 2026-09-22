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

"""Construction smoke test for ChannelFlow; behaviour lives in tests/integ."""

from __future__ import annotations

from dex import Registry

from dex_examples.primitives.channel.channel_flow import ChannelFlow, ChannelWait


def test_channel_flow_declares_a_start_step() -> None:
    flow = ChannelFlow()

    definitions = list(flow.get_steps())

    assert [definition.step for definition in definitions] == [flow.wait_for_approval]
    assert [definition.is_start_step for definition in definitions] == [True]
    assert isinstance(flow.wait_for_approval, ChannelWait)


def test_channel_flow_registers_its_channel_and_rpc() -> None:
    flow = ChannelFlow()

    registry = Registry((flow,))
    registered = registry._flow_for_instance(flow)

    assert {rpc.name for rpc in registered.rpcs.values()} == {
        "delete_queued_message",
        "enqueue_channel_message",
        "get_prioritized_messages",
        "get_queued_messages",
        "move_queued_message_to_prioritized_messages",
        "publish_approval_message",
    }
    assert registered.rpcs[
        "move_queued_message_to_prioritized_messages"
    ].options.is_transactional
    assert flow.approval_messages.name == ChannelFlow.approval_messages.name
