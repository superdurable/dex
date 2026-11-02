# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Plan mode: the plan's statuses and the plan texts the model sees.

The texts are identical in the Python and TypeScript cores. A runner that
honors plan mode adds ``build_plan_note`` to its model input and offers only
``write_todos`` in a ``PLANNING`` turn.
"""

from __future__ import annotations

from collections.abc import Sequence

from dex_agents.values import (
    AgentPlan,
    AgentPlanTask,
    InteractionMode,
    PlanStatus,
    PlanTaskStatus,
)

#: Rejection message of a call to any tool other than ``write_todos`` in a ``PLANNING`` turn.
PLANNING_TURN_TOOL_REJECTION = (
    "The tool call was not run: a planning turn can only call write_todos."
)

#: First line of the plan note while no plan exists.
NO_PLAN_NOTE = "[Dex] There is no plan yet."

#: Last line of the plan note in a ``PLANNING`` turn.
PLANNING_TURN_INSTRUCTION = (
    "This turn only plans: write or revise the plan with write_todos, and do not do or "
    "claim any of its tasks. The user reviews the plan before it runs."
)


def build_plan_execution_message(revision: int) -> str:
    """Return the user message text of a turn that ``execute_plan`` started.

    Args:
        revision: The approved ``AgentPlan.revision``.

    Returns:
        ``[Dex] The user approved plan revision <n>. Execute it and keep the
        task statuses current with write_todos.``
    """
    return (
        f"[Dex] The user approved plan revision {revision}. "
        "Execute it and keep the task statuses current with write_todos."
    )


def build_plan_note(plan: AgentPlan | None, interaction_mode: InteractionMode) -> str | None:
    """Return the text that shows the model the current plan, or ``None`` for none.

    A runner that honors plan mode adds it to the model input of every model
    call, after the context summary and before the turn's messages, as user
    text. It is not stored as a message, so it always shows the latest plan.

    .. code-block:: text

        [Dex] The current plan, revision 2, is draft:
        - [completed] Look up the order
        - [pending] Refund the order
        This turn only plans: write or revise the plan with write_todos, ...

    Args:
        plan: The ``AgentPlan``, or ``None``.
        interaction_mode: The turn's mode.

    Returns:
        One line for the plan and one per task, then ``PLANNING_TURN_INSTRUCTION``
        in a ``PLANNING`` turn, joined with newlines. ``NO_PLAN_NOTE`` stands
        for a missing or cleared plan in a ``PLANNING`` turn. ``None`` when
        there is no plan to show outside a ``PLANNING`` turn.
    """
    is_planning = interaction_mode == InteractionMode.PLANNING
    if plan is None or plan.status == PlanStatus.CLEARED:
        if not is_planning:
            return None
        lines = [NO_PLAN_NOTE]
    else:
        lines = [f"[Dex] The current plan, revision {plan.revision}, is {plan.status}:"]
        lines.extend(f"- [{task.status}] {task.content}" for task in plan.tasks)
    if is_planning:
        lines.append(PLANNING_TURN_INSTRUCTION)
    return "\n".join(lines)


def plan_status_after_write(
    tasks: Sequence[AgentPlanTask],
    interaction_mode: InteractionMode,
) -> PlanStatus:
    """Return the status of the plan that ``write_todos`` writes.

    Args:
        tasks: The tasks written.
        interaction_mode: The mode of the turn that wrote them.

    Returns:
        ``CLEARED`` for no tasks, ``DRAFT`` in a ``PLANNING`` turn,
        ``COMPLETED`` when every task is completed, and ``ACTIVE`` otherwise.
    """
    if not tasks:
        return PlanStatus.CLEARED
    if interaction_mode == InteractionMode.PLANNING:
        return PlanStatus.DRAFT
    if all(task.status == PlanTaskStatus.COMPLETED for task in tasks):
        return PlanStatus.COMPLETED
    return PlanStatus.ACTIVE
