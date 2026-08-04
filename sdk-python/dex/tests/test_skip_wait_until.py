# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

import unittest

from dex.command_request import CommandRequest
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.persistence import Persistence
from dex.state_decision import StateDecision
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState, should_skip_wait_until

class DirectStateSkip(WorkflowState[None]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        raise NotImplementedError

class DirectStateNotSkip(WorkflowState[int]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: int,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        raise NotImplementedError

    def execute(
        self,
        ctx: WorkflowContext,
        input: int,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        raise NotImplementedError

class IndirectStateSkip(DirectStateSkip):
    pass

class IndirectStateNotSkip(DirectStateSkip):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        raise NotImplementedError

class TestSkipWaitUntil(unittest.TestCase):
    def test_should_skip_wait_until(self) -> None:
        direct_skip = DirectStateSkip()
        direct_not_skip = DirectStateNotSkip()
        indirect_skip = IndirectStateSkip()
        indirect_not_skip = IndirectStateNotSkip()

        assert should_skip_wait_until(direct_skip)
        assert should_skip_wait_until(indirect_skip)

        assert not should_skip_wait_until(direct_not_skip)
        assert not should_skip_wait_until(indirect_not_skip)
