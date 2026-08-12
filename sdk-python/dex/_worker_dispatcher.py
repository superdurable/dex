# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from datetime import timedelta
from inspect import isawaitable
from typing import Any, cast

from dex._invocation_context import InvocationContext, InvocationMethod
from dex._value_hydrator import ValueHydrator
from dex._value_mapper import ValueMapper
from dex.attribute import AttributeLock
from dex.channel import Channel, ChannelMap
from dex.condition import ChannelCondition, Condition, TimerCondition
from dex.dexpb import dex_pb2 as pb
from dex.flow import Registry, RPCResult, _RegisteredFlow, _RegisteredStep
from dex.runtime_errors import InvalidStepResultError, ValueMappingError
from dex.step import (
    DecisionKind,
    RetryPolicy,
    StepDecision,
    StepDurability,
    StepMovement,
    StepOptions,
    WaitForFailurePolicy,
)
from dex.wait import Wait, WaitKind


class WorkerDispatcher:
    def __init__(
        self,
        registry: Registry,
        values: ValueMapper,
        hydrator: ValueHydrator,
    ) -> None:
        self._registry = registry
        self._values = values
        self._hydrator = hydrator

    def invoke_wait_for(
        self,
        original: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodResponse:
        request = self._hydrator.wait_for_request(original)
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
        try:
            if isawaitable(wait):
                raise TypeError(
                    "wait_for returned an awaitable; use AsyncWorker for async handlers"
                )
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

    def invoke_execute(
        self,
        original: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodResponse:
        request = self._hydrator.execute_request(original)
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
        decision = step.step.execute(context, input)
        try:
            if isawaitable(decision):
                raise TypeError(
                    "execute returned an awaitable; use AsyncWorker for async handlers"
                )
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

    def invoke_rpc(
        self,
        original: pb.InvokeWorkerRPCRequest,
    ) -> pb.InvokeWorkerRPCResponse:
        request = self._hydrator.rpc_request(original)
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
        returned = rpc.method(*arguments)
        if isawaitable(returned):
            raise TypeError(
                "RPC returned an awaitable; use AsyncWorker for async handlers"
            )
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

    def map_step_options(
        self,
        flow: _RegisteredFlow,
        options: StepOptions | None,
    ) -> pb.StepOptions | None:
        if options is None:
            return None
        mapped = pb.StepOptions(
            wait_for_failure_policy=(
                pb.WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE
                if options.wait_for_failure is WaitForFailurePolicy.PROCEED
                else pb.WAIT_FOR_METHOD_FAILURE_POLICY_FAIL_FLOW_ON_FAILURE
            ),
            wait_for_durability_override=cast(
                Any, self._map_durability(options.wait_for_durability)
            ),
            execute_durability_override=cast(
                Any, self._map_durability(options.execute_durability)
            ),
            wait_for_lock_attribute_keys=[
                self._map_lock(lock) for lock in options.wait_for_lock_attributes
            ],
            execute_lock_attribute_keys=[
                self._map_lock(lock) for lock in options.execute_lock_attributes
            ],
        )
        if options.wait_for_method_timeout is not None:
            mapped.wait_for_timeout_seconds = self._seconds32(
                options.wait_for_method_timeout
            )
        if options.execute_method_timeout is not None:
            mapped.execute_timeout_seconds = self._seconds32(
                options.execute_method_timeout
            )
        if options.wait_for_retry is not None:
            mapped.wait_for_retry_policy.CopyFrom(
                self._map_retry(options.wait_for_retry)
            )
        if options.execute_retry is not None:
            mapped.execute_retry_policy.CopyFrom(self._map_retry(options.execute_retry))
        if options._execute_failure_target is not None:
            target = self._registered_movement_target(
                flow,
                options._execute_failure_target,
            )
            mapped.execute_failure_policy = (
                pb.EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP
            )
            mapped.execute_failure_proceed_step_type = target.name
            target_options = self.map_step_options(
                flow,
                (
                    options._execute_failure_options
                    if options._execute_failure_options is not None
                    else target.step.get_step_options()
                ),
            )
            if target_options is not None:
                mapped.execute_failure_proceed_step_options.CopyFrom(target_options)
            mapped.execute_failure_proceed_step_options.skip_wait_for = (
                target.skips_wait_for
            )
        return mapped

    def _map_wait(
        self,
        flow: _RegisteredFlow,
        wait: Wait,
    ) -> pb.WaitingCondition | None:
        if wait.kind is WaitKind.SKIP_IMMEDIATELY:
            return None
        mapper = _ConditionMapper(flow)
        waiting = pb.WaitingCondition()
        if wait.kind is WaitKind.ALL_OF:
            waiting.waiting_condition_type = pb.WAITING_CONDITION_TYPE_ALL_COMPLETED
            mapper.add_all(wait.conditions)
        elif wait.kind is WaitKind.ANY_OF:
            waiting.waiting_condition_type = pb.WAITING_CONDITION_TYPE_ANY_COMPLETED
            mapper.add_all(wait.conditions)
        elif wait.kind is WaitKind.ANY_COMBINATION_OF:
            waiting.waiting_condition_type = (
                pb.WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED
            )
            for combination in wait.combinations:
                waiting.condition_combinations.add(
                    condition_ids=[
                        mapper.add(condition, id_required=True)
                        for condition in combination.conditions
                    ]
                )
        else:
            raise ValueError("unsupported Wait kind")
        waiting.timer_conditions.extend(mapper.timers)
        waiting.channel_conditions.extend(mapper.channels)
        return waiting

    def _map_decision(
        self,
        flow: _RegisteredFlow,
        decision: StepDecision,
    ) -> pb.StepDecision:
        mapped = pb.StepDecision()
        if decision.kind is DecisionKind.NEXT:
            if not decision.movements:
                raise ValueError("go_to_multi requires a movement")
            mapped.next_steps.extend(self._map_movements(flow, decision.movements))
        elif decision.kind is DecisionKind.GRACEFUL_COMPLETE:
            mapped.close_decision.CopyFrom(
                self._close(
                    pb.CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
                    decision.output,
                    decision._has_output(),
                )
            )
        elif decision.kind is DecisionKind.FORCE_COMPLETE:
            mapped.close_decision.CopyFrom(
                self._close(
                    pb.CLOSE_DECISION_TYPE_FORCE_COMPLETE,
                    decision.output,
                    decision._has_output(),
                )
            )
        elif decision.kind is DecisionKind.FORCE_FAIL:
            mapped.close_decision.CopyFrom(
                self._close(pb.CLOSE_DECISION_TYPE_FORCE_FAIL, decision.reason)
            )
        elif decision.kind is DecisionKind.DEAD_END:
            mapped.close_decision.close_decision_type = pb.CLOSE_DECISION_TYPE_DEAD_END
        elif decision.kind is DecisionKind.FORCE_COMPLETE_IF_CHANNELS_EMPTY:
            if decision.fallback is None:
                raise ValueError("conditional close requires a fallback movement")
            names: list[str] = []
            for channel in decision.empty_channels:
                if not isinstance(channel, Channel):
                    raise ValueError("conditional close requires static Channels")
                self._require_persistence_identity(flow, channel)
                names.append(channel.name)
            mapped.close_decision.CopyFrom(
                pb.CloseDecision(
                    close_decision_type=(
                        pb.CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY
                    ),
                    close_input=self._values.encode_dynamic(decision.output),
                    conditional_channel_names=names,
                )
            )
            mapped.next_steps.append(self._map_movement(flow, decision.fallback))
        else:
            raise ValueError("unsupported StepDecision kind")
        return mapped

    def _close(
        self,
        close_type: int,
        output: object,
        has_output: bool = True,
    ) -> pb.CloseDecision:
        close = pb.CloseDecision(close_decision_type=cast(Any, close_type))
        if has_output:
            close.close_input.CopyFrom(self._values.encode_dynamic(output))
        return close

    def _map_movements(
        self,
        flow: _RegisteredFlow,
        movements: tuple[StepMovement[Any], ...],
    ) -> list[pb.StepMovement]:
        return [self._map_movement(flow, movement) for movement in movements]

    def _map_movement(
        self,
        flow: _RegisteredFlow,
        movement: StepMovement[Any],
    ) -> pb.StepMovement:
        target = self._registered_movement_target(flow, movement.step)
        mapped = pb.StepMovement(
            step_type=target.name,
            step_input=self._values.encode(movement.input, target.input_codec),
        )
        options = self.map_step_options(
            flow,
            (
                movement.options
                if movement.options is not None
                else target.step.get_step_options()
            ),
        )
        if options is not None:
            mapped.step_options.CopyFrom(options)
        mapped.step_options.skip_wait_for = target.skips_wait_for
        return mapped

    @staticmethod
    def _registered_movement_target(
        flow: _RegisteredFlow,
        step: object,
    ) -> _RegisteredStep:
        step_type = getattr(step, "get_step_type", lambda: "")()
        target = flow.step(step_type)
        if target.step is not step:
            raise ValueError("Step movement target does not belong to Flow")
        return target

    @staticmethod
    def _require_persistence_identity(
        flow: _RegisteredFlow,
        definition: Channel[Any] | ChannelMap[Any],
    ) -> None:
        if flow.persistence.get(definition.name) is not definition:
            raise ValueError("Channel does not belong to Flow")

    @staticmethod
    def _map_retry(retry: RetryPolicy) -> pb.RetryPolicy:
        mapped = pb.RetryPolicy(
            backoff_coefficient=retry.backoff_coefficient,
            maximum_attempts=retry.maximum_attempts,
        )
        if retry.initial_interval is not None:
            mapped.initial_interval_seconds = WorkerDispatcher._seconds32(
                retry.initial_interval
            )
        if retry.maximum_interval is not None:
            mapped.maximum_interval_seconds = WorkerDispatcher._seconds32(
                retry.maximum_interval
            )
        if retry.total_duration is not None:
            mapped.total_duration_seconds = WorkerDispatcher._seconds32(
                retry.total_duration
            )
        return mapped

    @staticmethod
    def _map_durability(durability: StepDurability) -> int:
        return {
            StepDurability.DEFAULT: pb.STEP_DURABILITY_UNSPECIFIED,
            StepDurability.SYNC: pb.STEP_DURABILITY_SYNC,
            StepDurability.ASYNC: pb.STEP_DURABILITY_ASYNC,
        }[durability]

    @staticmethod
    def _map_lock(lock: AttributeLock) -> str:
        if lock.instance is None:
            return lock.attribute.name
        return Registry.physical_name(lock.attribute.name, lock.instance)

    @staticmethod
    def _seconds32(duration: timedelta) -> int:
        seconds = duration.total_seconds()
        if seconds < 0 or not seconds.is_integer() or seconds > 2**31 - 1:
            raise ValueError("duration must be whole seconds within int32")
        return int(seconds)


class _ConditionMapper:
    def __init__(self, flow: _RegisteredFlow) -> None:
        self._flow = flow
        self._ids: dict[int, str] = {}
        self._used: set[str] = set()
        self.timers: list[pb.TimerCondition] = []
        self.channels: list[pb.ChannelCondition] = []

    def add_all(self, conditions: tuple[Condition, ...]) -> None:
        if not conditions:
            raise ValueError("Wait requires at least one Condition")
        for condition in conditions:
            self.add(condition)

    def add(self, condition: Condition, *, id_required: bool = False) -> str:
        identity = id(condition)
        existing = self._ids.get(identity)
        if existing is not None:
            return existing
        condition_id = condition.condition_id or ""
        if id_required and not condition_id:
            raise ValueError(
                "any_combination_of requires every Condition to have an ID"
            )
        if condition.condition_id is not None and not condition_id:
            raise ValueError("empty Condition ID")
        if condition_id and condition_id in self._used:
            raise ValueError("duplicate Condition ID")
        if condition_id:
            self._used.add(condition_id)
        if isinstance(condition, TimerCondition):
            seconds = condition.duration.total_seconds()
            if not seconds.is_integer():
                raise ValueError("timer duration must use whole seconds")
            self.timers.append(
                pb.TimerCondition(
                    condition_id=condition_id,
                    duration_seconds=int(seconds),
                )
            )
        elif isinstance(condition, ChannelCondition):
            channel = condition.channel
            if (
                channel is None
                or self._flow.persistence.get(channel.name) is not channel
            ):
                raise ValueError(
                    f"Channel is not registered: {getattr(channel, 'name', '')}"
                )
            channel_name = channel.name
            if isinstance(channel, ChannelMap):
                if condition.instance is None:
                    raise ValueError("ChannelMap condition requires an instance")
                channel_name = Registry.physical_name(channel.name, condition.instance)
            elif condition.instance is not None:
                raise ValueError("static Channel cannot use an instance")
            mapped = pb.ChannelCondition(
                condition_id=condition_id,
                channel_name=channel_name,
            )
            if condition.at_least is not None:
                mapped.at_least = condition.at_least
            if condition.at_most is not None:
                mapped.at_most = condition.at_most
            self.channels.append(mapped)
        else:
            raise TypeError("unsupported Condition")
        self._ids[identity] = condition_id
        return condition_id
