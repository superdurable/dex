# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The serialized LangGraph checkpoint that a turn stores while its tool calls run."""

from __future__ import annotations

import base64
import json
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any

from langchain_core.runnables import RunnableConfig
from langgraph.checkpoint.base import ChannelVersions, Checkpoint, CheckpointMetadata
from langgraph.checkpoint.memory import InMemorySaver

# Bump when the JSON shape changes; restore must keep reading every earlier version.
RUN_STATE_FORMAT = "dex-agents-langgraph.checkpoint.v1"


# Checkpoint IDs follow each Worker's clock, so the largest ID may be stale; track the last put.
class TurnCheckpointSaver(InMemorySaver):
    def __init__(self, **saver_options: Any) -> None:
        super().__init__(**saver_options)
        self.latest_checkpoint_ids: dict[str, str] = {}

    def put(
        self,
        config: RunnableConfig,
        checkpoint: Checkpoint,
        metadata: CheckpointMetadata,
        new_versions: ChannelVersions,
    ) -> RunnableConfig:
        saved_config = super().put(config, checkpoint, metadata, new_versions)
        self.latest_checkpoint_ids[config["configurable"]["checkpoint_ns"]] = checkpoint["id"]
        return saved_config

    def latest_checkpoint_config(self, thread_id: str, namespace: str = "") -> RunnableConfig:
        return {
            "configurable": {
                "thread_id": thread_id,
                "checkpoint_ns": namespace,
                "checkpoint_id": self.latest_checkpoint_ids[namespace],
            }
        }


@dataclass(frozen=True, kw_only=True)
class PendingInterrupt:
    interrupt_id: str
    tool_call_ids: list[str]


@dataclass(frozen=True, kw_only=True)
class SuspendedGraphRunState:
    thread_id: str
    checkpoints: list[dict[str, Any]]
    interrupts: list[PendingInterrupt]


async def encode_suspended_graph_run_state(
    saver: TurnCheckpointSaver,
    thread_id: str,
    interrupts: Sequence[PendingInterrupt],
) -> str:
    serde = saver.serde
    checkpoints: list[dict[str, Any]] = []
    for namespace in sorted(saver.latest_checkpoint_ids):
        checkpoint_tuple = await saver.aget_tuple(
            saver.latest_checkpoint_config(thread_id, namespace)
        )
        if checkpoint_tuple is None:
            raise RuntimeError(f"checkpoint namespace {namespace!r} lost its latest checkpoint")
        checkpoints.append(
            {
                "checkpoint_ns": namespace,
                "checkpoint": _encode_typed(serde.dumps_typed(checkpoint_tuple.checkpoint)),
                "metadata": _encode_typed(serde.dumps_typed(checkpoint_tuple.metadata)),
                "pending_writes": [
                    {
                        "task_id": task_id,
                        "channel": channel,
                        "value": _encode_typed(serde.dumps_typed(value)),
                    }
                    for task_id, channel, value in checkpoint_tuple.pending_writes or ()
                ],
            }
        )
    return json.dumps(
        {
            "format": RUN_STATE_FORMAT,
            "thread_id": thread_id,
            "checkpoints": checkpoints,
            "interrupts": [
                {"interrupt_id": interrupt.interrupt_id, "tool_call_ids": interrupt.tool_call_ids}
                for interrupt in interrupts
            ],
        },
        separators=(",", ":"),
    )


def decode_suspended_graph_run_state(framework_run_state: str) -> SuspendedGraphRunState:
    document = json.loads(framework_run_state)
    if not isinstance(document, dict) or document.get("format") != RUN_STATE_FORMAT:
        raise ValueError("the framework run state is not a LangGraph checkpoint of this adapter")
    return SuspendedGraphRunState(
        thread_id=str(document["thread_id"]),
        checkpoints=list(document["checkpoints"]),
        interrupts=[
            PendingInterrupt(
                interrupt_id=str(interrupt["interrupt_id"]),
                tool_call_ids=[str(tool_call_id) for tool_call_id in interrupt["tool_call_ids"]],
            )
            for interrupt in document["interrupts"]
        ],
    )


async def restore_suspended_graph_checkpoints(
    saver: TurnCheckpointSaver,
    run_state: SuspendedGraphRunState,
) -> None:
    serde = saver.serde
    for stored in run_state.checkpoints:
        checkpoint = serde.loads_typed(_decode_typed(stored["checkpoint"]))
        metadata = serde.loads_typed(_decode_typed(stored["metadata"]))
        namespace_config: RunnableConfig = {
            "configurable": {
                "thread_id": run_state.thread_id,
                "checkpoint_ns": stored["checkpoint_ns"],
            }
        }
        saved_config = await saver.aput(
            namespace_config, checkpoint, metadata, checkpoint["channel_versions"]
        )
        # put_writes indexes writes by position, so each task's writes go in one call.
        writes_by_task_id: dict[str, list[tuple[str, Any]]] = {}
        for write in stored["pending_writes"]:
            writes_by_task_id.setdefault(write["task_id"], []).append(
                (write["channel"], serde.loads_typed(_decode_typed(write["value"])))
            )
        for task_id, writes in writes_by_task_id.items():
            await saver.aput_writes(saved_config, writes, task_id)


def _encode_typed(typed: tuple[str, bytes]) -> dict[str, str]:
    return {"type": typed[0], "data": base64.b64encode(typed[1]).decode("ascii")}


def _decode_typed(encoded: dict[str, str]) -> tuple[str, bytes]:
    return encoded["type"], base64.b64decode(encoded["data"])
