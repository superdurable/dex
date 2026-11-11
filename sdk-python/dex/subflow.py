# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from typing import Any

from dex._invocation_context import InvocationContext
from dex.condition import Condition, SubFlowCondition
from dex.context import Context
from dex.flow import Flow
from dex.flow_options import SubFlowOptions
from dex.flow_result import FlowResult


class SubFlow:
    """Create durable SubFlow Conditions and read their Execute results.

    A child has its own Flow ID and lifecycle. An AnyOf winner does not stop
    losing children; use get_flow_id and Client.stop_flow when they must stop.
    Read child results during execute using the zero-based SubFlow declaration
    order, independent of Timer and Channel positions in the Wait tree.

    Examples:
        A parent Step waits for a registered child Flow and returns its output::

            from dex import Context, Flow, Step, StepDecision, SubFlow, Wait, graceful_complete

            class AwaitReceipt(Step[int]):
                def __init__(self, receipt_flow: Flow[int]) -> None:
                    self.receipt_flow = receipt_flow

                def wait_for(self, context: Context, input: int) -> Wait:
                    return Wait.until(SubFlow.run(self.receipt_flow, input))

                def execute(self, context: Context, input: int) -> StepDecision:
                    result = SubFlow.get_condition_results(context)
                    return graceful_complete(result.single_output(int))
    """

    @staticmethod
    def run(
        flow: Flow[Any],
        input: object,
        options: SubFlowOptions | None = None,
    ) -> Condition:
        """Create a Condition that starts or reuses ``flow`` and awaits completion.

        Calling this factory describes work; Dex starts the child after the
        returned Wait is committed. None options use SubFlowOptions defaults,
        including RESTART_IF_PREVIOUS_EXITS_ABNORMALLY reuse. The child inherits
        the parent's effective FlowConfig unless options override its fields.
        Dex generates stable Flow and request IDs so retries of the same start
        attach rather than creating duplicate child executions.

        Only a Step whose Step type contains no ``-`` can start SubFlows. The
        generated child Flow ID joins the parent Flow ID, the Step execution ID,
        and the SubFlow index with ``-``. A dash in the Step type would make
        that ID ambiguous between two parents. Default Step types are class
        names and contain no dash. Rename a Step type set by ``get_step_type``
        if it contains one.

        .. versionchanged:: 1.44.0
           The Worker rejects a Wait that contains a SubFlow Condition when the
           returning Step's Step type contains ``-``. The Step's ``wait_for``
           fails with :class:`dex.InvalidStepResultError` before anything is
           sent to the server.

        Args:
            flow: The exact target Flow instance registered by the Worker.
            input: Input accepted by the target Flow's starting Step.
            options: Optional reuse, timing, Attribute, configuration, and ID settings.

        Returns:
            A durable Condition accepted by :class:`dex.Wait`.

        Raises:
            ValueError: If the condition ID is invalid, or when the Worker maps a
                Flow without a starting Step.
            FlowDefinitionError: When the Worker maps an unregistered Flow.
            InvalidStepResultError: When the Worker maps the Condition for a Step
                whose Step type contains ``-``.
            ValueMappingError: When the Worker cannot encode the child input.
        """
        resolved = options or SubFlowOptions()
        return SubFlowCondition(
            condition_id=resolved.condition_id,
            flow=flow,
            input=input,
            options=resolved,
        )

    @staticmethod
    def get_condition_results(context: Context, index: int = 0) -> FlowResult:
        """Return one stable-indexed SubFlow result during Step ``execute``.

        Args:
            context: The current Dex Execute context.
            index: Zero-based SubFlow order within the surrounding Wait.

        Returns:
            A terminal result or an AnyOf loser's running snapshot.

        Raises:
            TypeError: If ``context`` is not managed by Dex.
            ValueError: If called outside Execute or ``index`` is unavailable.
        """
        return SubFlow._context(context).sub_flow_result(index)

    @staticmethod
    def get_flow_id(context: Context, index: int = 0) -> str:
        """Return the generated Flow ID for one SubFlow Condition.

        The ID remains usable after an AnyOf winner completes and may be passed to
        ``Client.stop_flow`` to stop a still-running loser.

        Args:
            context: The current Dex Execute context.
            index: Zero-based SubFlow order within the surrounding Wait.

        Returns:
            The stable server-generated SubFlow Flow ID.
        """
        return SubFlow._context(context).sub_flow_id(index)

    @staticmethod
    def _context(context: Context) -> InvocationContext:
        if not isinstance(context, InvocationContext):
            raise TypeError("SubFlow access requires a Dex invocation Context")
        return context
