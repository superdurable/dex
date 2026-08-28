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

from __future__ import annotations

import asyncio
import socket
import time
from typing import Any, Callable

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Flow,
    Registry,
    WorkerOptions,
    WorkerTarget,
    open_blob_cache,
)

from dex_examples.config import ExamplesConfig
from dex_examples.patterns.cron.cron_schedule_flow import CronScheduleFlow
from dex_examples.patterns.drain_channels.internal.drain_internal_channels_flow import (
    DrainInternalChannelFlow,
)
from dex_examples.patterns.drain_channels.external_publishing.draining_channel_flow import (
    DrainingExternalChannelFlow,
)
from dex_examples.patterns.entity_store.user_profile_flow import UserProfileFlow
from dex_examples.patterns.interruptible.interruptible_execution_flow import (
    InterruptibleFlow,
)
from dex_examples.patterns.intervention.manual_recovery_flow import (
    ManualRecoveryFlow,
)
from dex_examples.patterns.parallel.await_parallel_steps_flow import AwaitParallelStepsFlow
from dex_examples.patterns.parallel.dynamic_parallel_steps_flow import DynamicParallelStepsFlow
from dex_examples.patterns.parallel.first_win_parallel_steps_flow import FirstWinParallelStepsFlow
from dex_examples.patterns.parallel.static_parallel_steps_flow import StaticParallelStepsFlow
from dex_examples.patterns.parent_child.parent_flow_v2 import ParentFlowV2
from dex_examples.patterns.polling.backoff_polling_flow import BackoffPollingFlow
from dex_examples.patterns.polling.simple_polling_flow import SimplePollingFlow
from dex_examples.patterns.recovery.failure_recovery_flow import FailureRecoveryFlow
from dex_examples.patterns.reminders.reminder_flow import ReminderFlow
from dex_examples.patterns.resettable_timer.resettable_timer_flow import (
    ResettableTimerFlow,
)
from dex_examples.patterns.resource_control.controller_flow import ControllerFlow
from dex_examples.patterns.resource_control.processing_flow import ProcessingFlow
from dex_examples.patterns.scalable_parallel.child_flow import ChildFlow
from dex_examples.patterns.scalable_parallel.parent_flow import ParentFlow
from dex_examples.patterns.scalable_parallel.request_receiver_flow import (
    RequestReceiverFlow,
)
from dex_examples.patterns.shared.service_dependency import ServiceDependency
from dex_examples.patterns.timeout.flow_graceful_timeout import FlowGracefulTimeout
from dex_examples.patterns.wait_for_state_completion.wait_for_state_completion_flow import (
    WaitForStateCompletionFlow,
)
from dex_examples.primitives.attribute.attribute_flow import AttributeFlow
from dex_examples.primitives.channel.channel_flow import ChannelFlow
from dex_examples.primitives.client_apis.client_apis_flow import ClientApisFlow
from dex_examples.primitives.custom_retry.custom_retry_flow import CustomRetryFlow
from dex_examples.primitives.flow.example_flow import ExampleFlow
from dex_examples.primitives.durability.durability_flow import DurabilityFlow
from dex_examples.primitives.heartbeat.heartbeat_flow import HeartbeatFlow
from dex_examples.primitives.options_override.options_override_flow import (
    OptionsOverrideFlow,
)
from dex_examples.primitives.proceed_on_wait_failure.proceed_on_wait_failure_flow import (
    ProceedOnWaitFailureFlow,
)
from dex_examples.primitives.rpc.rpc_flow import RpcFlow
from dex_examples.primitives.step.retry_flow import RetryFlow
from dex_examples.primitives.step.step_flow import StepFlow
from dex_examples.primitives.step_execution_local.step_execution_local_flow import (
    StepExecutionLocalFlow,
)
from dex_examples.primitives.step_decision.step_decision_flow import StepDecisionFlow
from dex_examples.primitives.stream.stream_flow import StreamFlow
from dex_examples.primitives.subflow.subflow_flow import SubFlowChildFlow, SubFlowParentFlow
from dex_examples.primitives.timer.timer_flow import TimerFlow
from dex_examples.primitives.wait_types.wait_types_flow import WaitTypesFlow
from dex_examples.products.ai_agent_email.ai_agent_flow import EmailAgentFlow
from dex_examples.products.engagement.engagement_flow import EngagementFlow
from dex_examples.products.job_post.job_post_flow import JobPostFlow
from dex_examples.products.microservices.orchestration_flow import OrchestrationFlow
from dex_examples.products.money_transfer.money_transfer_flow import MoneyTransferFlow
from dex_examples.products.order_processing.order_processing_flow import OrderProcessingFlow
from dex_examples.products.polling.polling_flow import PollingFlow
from dex_examples.products.shortlist_candidates.employer_opt_in_flow import EmployerOptInFlow
from dex_examples.products.shortlist_candidates.shortlist_flow import ShortlistFlow
from dex_examples.products.shortlist_candidates.workflow_ids import ClientOptInChecker
from dex_examples.products.signup.user_signup_flow import UserSignupFlow
from dex_examples.products.subscription.subscription_flow import SubscriptionFlow
from dex_examples.shared.my_dependency_service import MyDependencyService


class ExampleApp:
    def __init__(self, config: ExamplesConfig) -> None:
        self.config = config
        self._client: AsyncClient | None = None
        client_provider: Callable[[], AsyncClient] = self.require_client

        service = MyDependencyService()
        pattern_service = ServiceDependency()

        self.money_transfer = MoneyTransferFlow(service)
        self.order_processing = OrderProcessingFlow(service)
        self.orchestration = OrchestrationFlow(service)
        self.engagement = EngagementFlow(service)
        self.subscription = SubscriptionFlow(service)
        self.polling = PollingFlow(service)
        self.signup = UserSignupFlow(service)
        self.job_post = JobPostFlow(service)
        self.employer_opt_in = EmployerOptInFlow()
        self.shortlist = ShortlistFlow(
            service,
            ClientOptInChecker(client_provider, self.employer_opt_in),
        )

        self.cron_schedule = CronScheduleFlow()
        self.drain_internal = DrainInternalChannelFlow(pattern_service)
        self.drain_external = DrainingExternalChannelFlow()
        self.interruptible = InterruptibleFlow()
        self.manual_recovery = ManualRecoveryFlow()
        self.static_parallel = StaticParallelStepsFlow()
        self.dynamic_parallel = DynamicParallelStepsFlow()
        self.await_parallel = AwaitParallelStepsFlow()
        self.first_win_parallel = FirstWinParallelStepsFlow()
        self.simple_polling = SimplePollingFlow()
        self.backoff_polling = BackoffPollingFlow(pattern_service)
        self.failure_recovery = FailureRecoveryFlow()
        self.reminder = ReminderFlow(pattern_service)
        self.resettable_timer = ResettableTimerFlow()
        self.user_profile = UserProfileFlow()
        self.timeout = FlowGracefulTimeout()
        self.wait_for_state_completion = WaitForStateCompletionFlow(pattern_service)

        self.parent_flow: ParentFlow
        self.child_flow = ChildFlow(client_provider, lambda: self.parent_flow)
        self.parent_flow = ParentFlow(client_provider, self.child_flow)
        self.request_receiver = RequestReceiverFlow(client_provider, self.parent_flow)
        self.parent_flow_v2 = ParentFlowV2(client_provider, self.child_flow)

        self.example_flow = ExampleFlow()
        self.step = StepFlow()
        self.step_retry = RetryFlow()
        self.custom_retry = CustomRetryFlow()
        self.durability = DurabilityFlow()
        self.heartbeat = HeartbeatFlow()
        self.options_override = OptionsOverrideFlow()
        self.proceed_on_wait_failure = ProceedOnWaitFailureFlow()
        self.step_execution_local = StepExecutionLocalFlow()
        self.step_decision = StepDecisionFlow()
        self.wait_types = WaitTypesFlow()
        self.attribute = AttributeFlow()
        self.channel = ChannelFlow()
        self.stream = StreamFlow()
        self.timer = TimerFlow()
        self.rpc = RpcFlow()
        self.subflow_child = SubFlowChildFlow()
        self.subflow_parent = SubFlowParentFlow(self.subflow_child)
        self.client_apis = ClientApisFlow()

        self.controller = ControllerFlow(client_provider, lambda: self.processing)
        self.processing = ProcessingFlow(client_provider, lambda: self.controller)
        self.email_agent = EmailAgentFlow(client_provider)

        flows: list[Flow[Any]] = [
            self.money_transfer,
            self.order_processing,
            self.orchestration,
            self.engagement,
            self.subscription,
            self.polling,
            self.signup,
            self.job_post,
            self.employer_opt_in,
            self.shortlist,
            self.cron_schedule,
            self.drain_internal,
            self.drain_external,
            self.interruptible,
            self.manual_recovery,
            self.static_parallel,
            self.dynamic_parallel,
            self.await_parallel,
            self.first_win_parallel,
            self.simple_polling,
            self.backoff_polling,
            self.failure_recovery,
            self.reminder,
            self.resettable_timer,
            self.user_profile,
            self.timeout,
            self.wait_for_state_completion,
            self.child_flow,
            self.parent_flow,
            self.request_receiver,
            self.parent_flow_v2,
            self.example_flow,
            self.step,
            self.step_retry,
            self.custom_retry,
            self.durability,
            self.heartbeat,
            self.options_override,
            self.proceed_on_wait_failure,
            self.step_execution_local,
            self.step_decision,
            self.wait_types,
            self.attribute,
            self.channel,
            self.stream,
            self.timer,
            self.rpc,
            self.subflow_child,
            self.subflow_parent,
            self.client_apis,
            self.controller,
            self.processing,
            self.email_agent,
        ]
        self.registry = Registry(tuple(flows), allow_async_handlers=True)
        config.blob_cache_dir.mkdir(parents=True, exist_ok=True)
        self.blob_cache = open_blob_cache(
            BlobCacheConfig(str(config.blob_cache_dir), 1 << 30)
        )
        worker_options = WorkerOptions(
            bind_address=config.worker_bind_address,
            server_address=config.server_address,
            worker_target=(
                WorkerTarget(config.worker_target)
                if config.worker_target
                else None
            ),
        )
        self.worker = AsyncWorker(self.registry, self.blob_cache, worker_options)
        self._client = AsyncClient(
            self.registry,
            self.blob_cache,
            ClientOptions(
                server_address=config.server_address,
                worker_target=self.worker.worker_target,
            ),
        )
        self._worker_task: asyncio.Task[None] | None = None

    @property
    def client(self) -> AsyncClient:
        return self.require_client()

    def require_client(self) -> AsyncClient:
        if self._client is None:
            raise RuntimeError("client is not ready")
        return self._client

    async def start_worker(self) -> None:
        if self._worker_task is not None:
            return
        self._worker_task = asyncio.create_task(self.worker.start())
        await _await_worker(self.worker.worker_target.address, self._worker_task)

    async def close(self) -> None:
        if self._client is not None:
            await self._client.close()
            self._client = None
        await self.worker.close()
        if self._worker_task is not None:
            try:
                await asyncio.wait_for(self._worker_task, timeout=10)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                self._worker_task.cancel()
            self._worker_task = None
        self.blob_cache.close()


async def _await_worker(address: str, worker_task: asyncio.Task[None]) -> None:
    host, _, port_text = address.rpartition(":")
    port = int(port_text)
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if worker_task.done():
            error = worker_task.exception()
            if error is not None:
                raise RuntimeError("AsyncWorker failed") from error
            raise RuntimeError("AsyncWorker stopped before becoming ready")
        try:
            with socket.create_connection((host or "127.0.0.1", port), timeout=0.1):
                return
        except OSError:
            await asyncio.sleep(0.01)
    raise RuntimeError("AsyncWorker did not become ready")
