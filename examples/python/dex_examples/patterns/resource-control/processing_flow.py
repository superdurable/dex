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

"""Runs one request on one spot instance and reports back to its ControllerFlow."""

from __future__ import annotations

from datetime import timedelta
from typing import TYPE_CHECKING, Callable

from dex import (
    AsyncClient,
    Attribute,
    Context,
    FlowNotActiveError,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.resource_control.request import Request

if TYPE_CHECKING:
    from dex_examples.patterns.resource_control.controller_flow import ControllerFlow

POLL_INTERVAL = timedelta(seconds=5)

STATUS_VALIDATION_STARTED = "validation started"
STATUS_VALIDATION_COMPLETED = "validation completed"
STATUS_PROCESSING_STARTED = "processing started"
STATUS_PROCESSING_COMPLETED = "gpu processing completed"


class Complete(Step[None]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        controller_flow_provider: Callable[[], ControllerFlow],
        parent_flow_id: Attribute[str],
    ) -> None:
        self.client_provider = client_provider
        self.controller_flow_provider = controller_flow_provider
        self.parent_flow_id = parent_flow_id

    async def execute(  # type: ignore[override]
        self, context: Context, input: None
    ) -> StepDecision:
        parent_flow_id = self.parent_flow_id.get(context)
        if parent_flow_id:
            try:
                await self.client_provider().invoke_rpc(
                    self.controller_flow_provider().complete_child_flow,
                    parent_flow_id,
                    context.flow_id,
                )
            except FlowNotActiveError:
                print(
                    "Parent flow may have completed, possibly a duplicate "
                    "completion request, ignoring it."
                )
        return graceful_complete()


class GpuProcessingComplete(Step[None]):
    def __init__(
        self,
        complete: Complete,
        instance_id: Attribute[str],
        status: Attribute[str],
    ) -> None:
        self.complete = complete
        self.instance_id = instance_id
        self.status = status

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(Timer.by_duration(POLL_INTERVAL))

    def execute(self, context: Context, input: None) -> StepDecision:
        print(
            "check gpu processing in "
            f"{self.instance_id.get(context)} by calling the instance API"
        )
        self.status.set(context, STATUS_PROCESSING_COMPLETED)
        return go_to(Complete, None)


class GpuProcessingStart(Step[None]):
    def __init__(
        self,
        gpu_processing_complete: GpuProcessingComplete,
        request: Attribute[Request],
        instance_id: Attribute[str],
        status: Attribute[str],
    ) -> None:
        self.gpu_processing_complete = gpu_processing_complete
        self.request = request
        self.instance_id = instance_id
        self.status = status

    def execute(self, context: Context, input: None) -> StepDecision:
        print(
            f"start processing of request {self.request.get(context)} in "
            f"{self.instance_id.get(context)} by calling the instance API"
        )
        self.status.set(context, STATUS_PROCESSING_STARTED)
        return go_to(GpuProcessingComplete, None)


class ValidationComplete(Step[None]):
    def __init__(
        self,
        gpu_processing_start: GpuProcessingStart,
        instance_id: Attribute[str],
        status: Attribute[str],
    ) -> None:
        self.gpu_processing_start = gpu_processing_start
        self.instance_id = instance_id
        self.status = status

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(Timer.by_duration(POLL_INTERVAL))

    def execute(self, context: Context, input: None) -> StepDecision:
        print(
            "completed validation in "
            f"{self.instance_id.get(context)} by calling the instance API"
        )
        self.status.set(context, STATUS_VALIDATION_COMPLETED)
        return go_to(GpuProcessingStart, None)


class ValidationStart(Step[Request]):
    def __init__(
        self,
        validation_complete: ValidationComplete,
        request: Attribute[Request],
        instance_id: Attribute[str],
        status: Attribute[str],
    ) -> None:
        self.validation_complete = validation_complete
        self.request = request
        self.instance_id = instance_id
        self.status = status

    def execute(self, context: Context, input: Request) -> StepDecision:
        # Persisting the request keeps later Steps from passing it along as input.
        self.request.set(context, input)
        print(
            f"start validation of request {input} in "
            f"{self.instance_id.get(context)} by calling the instance API"
        )
        self.status.set(context, STATUS_VALIDATION_STARTED)
        return go_to(ValidationComplete, None)


class ProcessingFlow(Flow[Request]):
    DA_PARENT_FLOW_ID = "ParentFlowId"
    DA_STATUS = "Status"
    DA_INSTANCE_ID = "InstanceId"
    DA_REQUEST = "Request"

    parent_flow_id = Attribute(DA_PARENT_FLOW_ID, str)
    status = Attribute(DA_STATUS, str)
    instance_id = Attribute(DA_INSTANCE_ID, str)
    request = Attribute(DA_REQUEST, Request)

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        controller_flow_provider: Callable[[], ControllerFlow],
    ) -> None:
        self.complete = Complete(
            client_provider,
            controller_flow_provider,
            self.parent_flow_id,
        )
        self.gpu_processing_complete = GpuProcessingComplete(
            self.complete,
            self.instance_id,
            self.status,
        )
        self.gpu_processing_start = GpuProcessingStart(
            self.gpu_processing_complete,
            self.request,
            self.instance_id,
            self.status,
        )
        self.validation_complete = ValidationComplete(
            self.gpu_processing_start,
            self.instance_id,
            self.status,
        )
        self.validation_start = ValidationStart(
            self.validation_complete,
            self.request,
            self.instance_id,
            self.status,
        )

    def get_steps(self) -> StepList[Request]:
        return StepList.start_step(self.validation_start).other_steps(
            self.validation_complete,
            self.gpu_processing_start,
            self.gpu_processing_complete,
            self.complete,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.parent_flow_id,
            self.status,
            self.instance_id,
            self.request,
        )

    @rpc
    def describe(self, context: Context) -> RPCResult[str]:
        return RPCResult(self.status.get(context) or "")
