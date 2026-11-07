# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sessions of an LangChain agent outlive the context window, on a real Dex Server."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from pathlib import Path

from dex_agents.testing import (
    AgentTestStack,
    ContextWindowSessionScenario,
    ContextWindowSettings,
)

from tests.compaction_agent import (
    STATE_DIR_ENVIRONMENT_VARIABLE,
    build_compaction_agent_flow,
)
from tests.conftest import PACKAGE_DIR

WORKER_MODULE = "tests.compaction_worker"


@asynccontextmanager
async def _scenario(
    tmp_path: Path, settings: ContextWindowSettings
) -> AsyncIterator[ContextWindowSessionScenario]:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_compaction_agent_flow(state_dir, settings)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={
            STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir),
            **settings.to_environment(),
        },
    ) as stack:
        yield ContextWindowSessionScenario(stack, flow, state_dir, settings)


async def test_a_session_keeps_working_past_the_context_window(tmp_path: Path) -> None:
    async with _scenario(tmp_path, ContextWindowSettings()) as scenario:
        await scenario.assert_outlives_the_window()


async def test_retention_deletes_only_chunks_behind_the_summary(tmp_path: Path) -> None:
    settings = ContextWindowSettings(retained_message_limit=8)
    async with _scenario(tmp_path, settings) as scenario:
        await scenario.assert_retention_follows_the_summary()


async def test_without_a_policy_nothing_changes_and_the_window_ends_the_session(
    tmp_path: Path,
) -> None:
    settings = ContextWindowSettings(is_compaction_enabled=False)
    async with _scenario(tmp_path, settings) as scenario:
        await scenario.assert_fails_at_the_window_without_a_policy()


async def test_a_session_started_without_a_policy_compacts_after_an_upgrade(
    tmp_path: Path,
) -> None:
    async with _scenario(tmp_path, ContextWindowSettings()) as scenario:
        await scenario.assert_starts_compacting_after_an_upgrade()


async def test_worker_killed_during_compact_context_reruns_only_the_summary(
    tmp_path: Path,
) -> None:
    async with _scenario(tmp_path, ContextWindowSettings()) as scenario:
        await scenario.assert_worker_kill_reruns_only_the_summary()
