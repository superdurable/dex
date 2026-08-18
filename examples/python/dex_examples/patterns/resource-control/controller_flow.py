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

"""Caps how many requests one spot instance processes at a time.

One ControllerFlow owns one instance. It buffers requests in a Channel and
starts at most CONCURRENCY_PER_CONTROLLER_FLOW child ProcessingFlows.
"""

from __future__ import annotations

from datetime import timedelta
from typing import TYPE_CHECKING, Callable

from dex import (
    AsyncClient,
    Attribute,
    Channel,
    ChannelMap,
    Context,
    FlowAlreadyStartedError,
    Flow,
    IdReusePolicy,
    PersistenceSchema,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    force_complete_if_channels_empty,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.resource_control.request import Request

if TYPE_CHECKING:
    from dex_examples.patterns.resource_control.processing_flow import ProcessingFlow

# Permanent IDs of the spot instances in use; they survive restarts and AWS
# interrupts. Scale up by requesting an instance and adding its ID; scale down by
# shutting the instance down first, then removing its ID. Order does not matter.
SPOT_INSTANCE_IDS = ("permanentID1", "permanentID2")

CONCURRENCY_PER_CONTROLLER_FLOW = 5
MAX_BUFFERED_REQUESTS = 20


class MoveToAnotherInstance(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        print("move all the started child flows to another instance")
        return graceful_complete("moved to another instance")


class LoopForNextRequest(Step[None]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        processing_flow_provider: Callable[[], ProcessingFlow],
        move_to_another_instance: MoveToAnotherInstance,
        request_queue: Channel[Request],
        child_complete: ChannelMap[None],
        current_wait_child_wfs: Attribute[list[str]],
        instance_id: Attribute[str],
        shutdown_requested: Attribute[bool],
    ) -> None:
        self.client_provider = client_provider
        self.processing_flow_provider = processing_flow_provider
        self.move_to_another_instance = move_to_another_instance
        self.request_queue = request_queue
        self.child_complete = child_complete
        self.current_wait_child_wfs = current_wait_child_wfs
        self.instance_id = instance_id
        self.shutdown_requested = shutdown_requested

    def wait_for(self, context: Context, input: None) -> Wait:
        del input
        waiting = self.current_wait_child_wfs.get(context) or []
        conditions = [self.child_complete.for_one(child_id) for child_id in waiting]
        if len(waiting) < CONCURRENCY_PER_CONTROLLER_FLOW:
            conditions.insert(0, self.request_queue.for_one())
        return Wait.any_of(*conditions)

    async def execute(  # type: ignore[override]
        self, context: Context, input: None
    ) -> StepDecision:
        del input
        waiting = list(self.current_wait_child_wfs.get(context) or [])

        requests = self.request_queue.results(context)
        if requests:
            started = await self.start_child_flow(context, requests[0])
            if started is not None:
                waiting.append(started)

        for child_id in list(waiting):
            if self.child_complete.results(context, child_id):
                waiting.remove(child_id)

        self.current_wait_child_wfs.set(context, waiting)

        if self.shutdown_requested.get(context):
            return go_to(self.move_to_another_instance, None)
        if not waiting:
            return force_complete_if_channels_empty(
                "done",
                StepMovement.of(self, None),
                self.request_queue,
            )
        return go_to(self, None)

    async def start_child_flow(self, context: Context, request: Request) -> str | None:
        """Returns the child Flow ID to wait for, or None when another run owns it."""
        processing_flow = self.processing_flow_provider()
        child_flow_id = f"processing-{request.id}"
        try:
            await self.client_provider().start_flow(
                processing_flow,
                child_flow_id,
                request,
                StartFlowOptions(
                    timeout=timedelta(hours=1),
                    id_reuse_policy=IdReusePolicy.DISALLOW,
                    ignore_already_started=True,
                    request_id=context.step_execution_id,
                )
                .with_attribute(processing_flow.parent_flow_id, context.flow_id)
                .with_attribute(
                    processing_flow.instance_id,
                    self.instance_id.get(context) or "",
                ),
            )
        except FlowAlreadyStartedError:
            print("already started by another run, ignore it -- not waiting for it")
            return None
        return child_flow_id


class Init(Step[Request]):
    def __init__(
        self,
        loop_for_next_request: LoopForNextRequest,
        request_queue: Channel[Request],
        current_wait_child_wfs: Attribute[list[str]],
    ) -> None:
        self.loop_for_next_request = loop_for_next_request
        self.request_queue = request_queue
        self.current_wait_child_wfs = current_wait_child_wfs

    def execute(self, context: Context, input: Request) -> StepDecision:
        self.request_queue.publish(context, input)
        self.current_wait_child_wfs.set(context, [])
        return go_to(self.loop_for_next_request, None)


class ControllerFlow(Flow[Request]):
    REQUEST_QUEUE = "RequestQueue"
    CHILD_COMPLETE_CHANNEL_PREFIX = "ChildComplete_"
    DA_CURRENT_WAIT_CHILD_WFS = "CurrentWaitChildWfs"
    DA_INSTANCE_ID = "InstanceId"
    DA_SHUTDOWN = "Shutdown"

    request_queue = Channel(REQUEST_QUEUE, Request)
    child_complete = ChannelMap[None](CHILD_COMPLETE_CHANNEL_PREFIX, type(None))
    current_wait_child_wfs = Attribute(DA_CURRENT_WAIT_CHILD_WFS, list[str])
    instance_id = Attribute(DA_INSTANCE_ID, str)
    shutdown_requested = Attribute(DA_SHUTDOWN, bool)

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        processing_flow_provider: Callable[[], ProcessingFlow],
    ) -> None:
        self.move_to_another_instance = MoveToAnotherInstance()
        self.loop_for_next_request = LoopForNextRequest(
            client_provider,
            processing_flow_provider,
            self.move_to_another_instance,
            self.request_queue,
            self.child_complete,
            self.current_wait_child_wfs,
            self.instance_id,
            self.shutdown_requested,
        )
        self.init = Init(
            self.loop_for_next_request,
            self.request_queue,
            self.current_wait_child_wfs,
        )

    def get_steps(self) -> StepList[Request]:
        return StepList.start_step(self.init).other_steps(
            self.loop_for_next_request,
            self.move_to_another_instance,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.request_queue,
            self.child_complete,
            self.current_wait_child_wfs,
            self.instance_id,
            self.shutdown_requested,
        )

    @rpc
    def shutdown(self, context: Context) -> RPCResult[bool]:
        if self.shutdown_requested.get(context):
            return RPCResult(False)
        self.shutdown_requested.set(context, True)
        return RPCResult(True)

    @rpc
    def enqueue(self, context: Context, input: Request) -> RPCResult[bool]:
        if self.shutdown_requested.get(context):
            return RPCResult(False)
        if self.request_queue.size(context) + 1 > MAX_BUFFERED_REQUESTS:
            return RPCResult(False)
        self.request_queue.publish(context, input)
        return RPCResult(True)

    @rpc
    def complete_child_flow(self, context: Context, input: str) -> None:
        # An overloaded server can retry completions; only forward the ones we
        # still wait for so the Channel stays free of garbage.
        if input not in (self.current_wait_child_wfs.get(context) or []):
            return
        self.child_complete.publish(context, input, None)
