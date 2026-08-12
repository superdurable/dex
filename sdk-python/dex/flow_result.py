# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from collections.abc import Callable, Sequence
from typing import Any, TypeVar, cast

from dex.dexpb import dex_pb2 as pb

OutputT = TypeVar("OutputT")
_Decoder = Callable[[pb.Value, type[Any]], Any]


class StepCompletion:
    """Contain one output-bearing Step completion returned by ``wait_for_flow``.

    Attributes:
        step_type: The registered Step type that produced the output.
        step_execution_id: The exact server Step execution identity.
    """

    __slots__ = ("_decoder", "_output", "_step_execution_id", "_step_type")

    def __init__(
        self,
        completion: pb.StepCompletionOutput,
        decoder: _Decoder,
    ) -> None:
        """Create a completion around an already hydrated wire value.

        Args:
            completion: The hydrated completion message returned by Dex.
            decoder: The SDK value decoder used by :meth:`decode`.
        """
        if not completion.HasField("completed_step_output"):
            raise ValueError("Step completion output is required")
        self._step_type = completion.completed_step_type
        self._step_execution_id = completion.completed_step_execution_id
        self._output = completion.completed_step_output
        self._decoder = decoder

    @property
    def step_type(self) -> str:
        """Return the registered Step type that produced this output.

        Returns:
            The registered Step type.
        """
        return self._step_type

    @property
    def step_execution_id(self) -> str:
        """Return the exact server Step execution identity.

        Returns:
            The server-assigned Step execution ID.
        """
        return self._step_execution_id

    def decode(self, output_type: type[OutputT]) -> OutputT:
        """Decode this hydrated Step output as ``output_type``.

        Args:
            output_type: The concrete Python type expected by the caller.

        Returns:
            The decoded Step output.

        Raises:
            ValueMappingError: If the output cannot be decoded as ``output_type``.
        """
        return cast(OutputT, self._decoder(self._output, output_type))


class WaitForFlowResult:
    """Contain every output-bearing completion from a successful Flow.

    ``completions`` preserves server collection order, which is not deterministic
    for parallel Steps. Select by Step type or Step execution ID when identity matters.

    Attributes:
        completions: An immutable sequence of Step completions.
    """

    __slots__ = ("_completions",)

    def __init__(self, completions: Sequence[StepCompletion]) -> None:
        """Create a result with an immutable completion snapshot.

        Args:
            completions: Output-bearing Step completions in server collection order.
        """
        self._completions = tuple(completions)

    @property
    def completions(self) -> tuple[StepCompletion, ...]:
        """Return completions in server collection order.

        Returns:
            An immutable tuple of output-bearing Step completions.
        """
        return self._completions

    def single_output(self, output_type: type[OutputT]) -> OutputT:
        """Decode the output when exactly one completion exists.

        Args:
            output_type: The concrete Python type expected by the caller.

        Returns:
            The only decoded Step output.

        Raises:
            ValueError: If the Flow returned zero or multiple outputs.
            ValueMappingError: If the output cannot be decoded as ``output_type``.
        """
        if len(self.completions) != 1:
            raise ValueError(
                f"Expected exactly one Step output, found {len(self.completions)}"
            )
        return self.completions[0].decode(output_type)
