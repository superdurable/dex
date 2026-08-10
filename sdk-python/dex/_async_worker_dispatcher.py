# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from inspect import isawaitable
from typing import Any

from dex._async_value_hydrator import AsyncValueHydrator
from dex._invocation_context import InvocationContext, InvocationMethod
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb
from dex.flow import RPCResult, Registry
from dex.runtime_errors import InvalidStepResultError, ValueMappingError
from dex.step import StepDecision
from dex.wait import Wait


class AsyncWorkerDispatcher(WorkerDispatcher):
    def __init__(
        self,
        registry: Registry,
        values: ValueMapper,
        hydrator: AsyncValueHydrator,
    ) -> None:
        self._registry = registry
        self._values = values
        self._async_hydrator = hydrator

    async def invoke_wait_for(  # type: ignore[override]
        self,
        original: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodResponse:
        request = await self._async_hydrator.wait_for_request(original)
        flow = self._registry._flow_by_type(request.flow_type)
        step = flow.step(request.step_type)
        context = InvocationContext(
            InvocationMethod.WAIT_FOR,
            flow,
            request.context,
            self._values,
            request.attributes,
        )
        input = self._values.decode(request.step_input, step.input_codec)
        wait = step.step.wait_for(context, input)
        if isawaitable(wait):
            wait = await wait
        try:
            if not isinstance(wait, Wait):
                raise TypeError("wait_for must return Wait")
            response = pb.InvokeWaitForMethodResponse(
                upsert_attributes=list(context.attribute_writes.values()),
                upsert_step_exe_locals=list(context.local_writes.values()),
                record_events=context.events,
                publish_to_channel=context.publications,
            )
            waiting = self._map_wait(flow, wait)
            if waiting is not None:
                response.waiting_condition.CopyFrom(waiting)
            return response
        except (TypeError, ValueError) as error:
            raise InvalidStepResultError(
                flow.name, step.name, "wait_for", str(error)
            ) from error

    async def invoke_execute(  # type: ignore[override]
        self,
        original: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodResponse:
        request = await self._async_hydrator.execute_request(original)
        flow = self._registry._flow_by_type(request.flow_type)
        step = flow.step(request.step_type)
        condition_results = (
            request.condition_results if request.HasField("condition_results") else None
        )
        context = InvocationContext(
            InvocationMethod.EXECUTE,
            flow,
            request.context,
            self._values,
            request.attributes,
            request.step_exe_locals,
            condition_results,
        )
        input = self._values.decode(request.step_input, step.input_codec)
        decision: Any = step.step.execute(context, input)
        if isawaitable(decision):
            decision = await decision
        try:
            if not isinstance(decision, StepDecision):
                raise TypeError("execute must return StepDecision")
            return pb.InvokeExecuteMethodResponse(
                step_decision=self._map_decision(flow, decision),
                upsert_attributes=list(context.attribute_writes.values()),
                record_events=context.events,
                upsert_step_exe_locals=list(context.local_writes.values()),
                publish_to_channel=context.publications,
            )
        except (TypeError, ValueError) as error:
            raise InvalidStepResultError(
                flow.name, step.name, "execute", str(error)
            ) from error

    async def invoke_rpc(  # type: ignore[override]
        self,
        original: pb.InvokeWorkerRPCRequest,
    ) -> pb.InvokeWorkerRPCResponse:
        request = await self._async_hydrator.rpc_request(original)
        flow = self._registry._flow_by_type(request.flow_type)
        rpc = flow.rpc(request.rpc_name)
        context = InvocationContext(
            InvocationMethod.RPC,
            flow,
            request.context,
            self._values,
            request.attributes,
            channel_infos=dict(request.channel_infos),
        )
        arguments: list[object] = [context]
        if rpc.input_codec is not None:
            arguments.append(self._values.decode(request.input, rpc.input_codec))
        returned: Any = rpc.method(*arguments)
        if isawaitable(returned):
            returned = await returned
        try:
            response = pb.InvokeWorkerRPCResponse(
                upsert_attributes=list(context.attribute_writes.values()),
                record_events=context.events,
                publish_to_channel=context.publications,
            )
            if isinstance(returned, RPCResult):
                if rpc.output_codec is None:
                    raise TypeError("RPCResult requires an output type")
                response.output.CopyFrom(
                    self._values.encode(returned.output, rpc.output_codec)
                )
                if returned.next_steps:
                    response.step_decision.next_steps.extend(
                        self._map_movements(flow, returned.next_steps)
                    )
            elif returned is None and rpc.output_codec is None:
                response.output.CopyFrom(self._values.encode_dynamic(None))
            else:
                raise TypeError("RPC must return RPCResult or None")
            return response
        except ValueMappingError:
            raise
        except (TypeError, ValueError) as error:
            raise InvalidStepResultError(flow.name, None, "rpc", str(error)) from error
