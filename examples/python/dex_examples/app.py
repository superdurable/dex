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

from dex_examples.basic.basic_flow import BasicFlow
from dex_examples.config import ExamplesConfig
from dex_examples.my_dependency_service import MyDependencyService
from dex_examples.patterns.services.service_dependency import ServiceDependency
from dex_examples.patterns.workflow.cron.cron_schedule_flow import CronScheduleFlow
from dex_examples.patterns.workflow.drainchannels.internal.drain_internal_channels_flow import (
    DrainInternalChannelsFlow,
)
from dex_examples.patterns.workflow.drainchannels.signal.drain_signal_channels_flow import (
    DrainSignalChannelsFlow,
)
from dex_examples.patterns.workflow.entitystore.user_profile_flow import UserProfileFlow
from dex_examples.patterns.workflow.interruptible.interruptible_execution_flow import (
    InterruptibleExecutionFlow,
)
from dex_examples.patterns.workflow.intervention.manual_intervention_flow import (
    ManualInterventionFlow,
)
from dex_examples.patterns.workflow.parallel.parallel_states_with_await_flow import (
    ParallelStatesWithAwaitFlow,
)
from dex_examples.patterns.workflow.parallel.simple_parallel_states_flow import (
    SimpleParallelStatesFlow,
)
from dex_examples.patterns.workflow.parentchild.parent_flow_v2 import ParentFlowV2
from dex_examples.patterns.workflow.polling.backoff_polling_flow import BackoffPollingFlow
from dex_examples.patterns.workflow.polling.simple_polling_flow import SimplePollingFlow
from dex_examples.patterns.workflow.recovery.failure_recovery_flow import FailureRecoveryFlow
from dex_examples.patterns.workflow.reminders.reminder_flow import ReminderFlow
from dex_examples.patterns.workflow.resettabletimer.resettable_timer_flow import (
    ResettableTimerFlow,
)
from dex_examples.patterns.workflow.scalableparallel.child_flow import ChildFlow
from dex_examples.patterns.workflow.scalableparallel.parent_flow import ParentFlow
from dex_examples.patterns.workflow.scalableparallel.request_receiver_flow import (
    RequestReceiverFlow,
)
from dex_examples.patterns.workflow.timeout.flow_graceful_timeout import FlowGracefulTimeout
from dex_examples.patterns.workflow.waitforstatecompletion.wait_for_state_completion_flow import (
    WaitForStateCompletionFlow,
)
from dex_examples.resourcecontrol.controller_flow import ControllerFlow
from dex_examples.resourcecontrol.processing_flow import ProcessingFlow
from dex_examples.ai_agent_email.ai_agent_flow import EmailAgentFlow
from dex_examples.workflow.engagement.engagement_flow import EngagementFlow
from dex_examples.workflow.jobpost.job_post_flow import JobPostFlow
from dex_examples.workflow.microservices.orchestration_flow import OrchestrationFlow
from dex_examples.workflow.money.transfer.money_transfer_flow import MoneyTransferFlow
from dex_examples.workflow.polling.polling_flow import PollingFlow
from dex_examples.workflow.shortlistcandidates.employer_opt_in_flow import EmployerOptInFlow
from dex_examples.workflow.shortlistcandidates.shortlist_flow import ShortlistFlow
from dex_examples.workflow.shortlistcandidates.workflow_ids import ClientOptInChecker
from dex_examples.workflow.signup.user_signup_flow import UserSignupFlow
from dex_examples.workflow.subscription.subscription_flow import SubscriptionFlow


class ExampleApp:
    def __init__(self, config: ExamplesConfig) -> None:
        self.config = config
        self._client: AsyncClient | None = None
        client_provider: Callable[[], AsyncClient] = self.require_client

        service = MyDependencyService()
        pattern_service = ServiceDependency()

        self.money_transfer = MoneyTransferFlow(service)
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
        self.drain_internal = DrainInternalChannelsFlow(pattern_service)
        self.drain_signal = DrainSignalChannelsFlow()
        self.interruptible = InterruptibleExecutionFlow()
        self.manual_intervention = ManualInterventionFlow()
        self.simple_parallel = SimpleParallelStatesFlow()
        self.parallel_with_await = ParallelStatesWithAwaitFlow()
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

        self.basic = BasicFlow()
        self.controller = ControllerFlow(client_provider, lambda: self.processing)
        self.processing = ProcessingFlow(client_provider, lambda: self.controller)
        self.email_agent = EmailAgentFlow()

        flows: list[Flow[Any]] = [
            self.money_transfer,
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
            self.drain_signal,
            self.interruptible,
            self.manual_intervention,
            self.simple_parallel,
            self.parallel_with_await,
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
            self.basic,
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
        self._client: AsyncClient | None = None
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
        await _await_worker(self.worker, self._worker_task)
        self._client = AsyncClient(
            self.registry,
            self.blob_cache,
            ClientOptions(
                server_address=self.config.server_address,
                worker_target=self.worker.worker_target,
            ),
        )

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


async def _await_worker(
    worker: AsyncWorker, worker_task: asyncio.Task[None]
) -> None:
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if worker_task.done():
            error = worker_task.exception()
            if error is not None:
                raise RuntimeError("AsyncWorker failed") from error
            raise RuntimeError("AsyncWorker stopped before becoming ready")
        host, _, port_text = worker.worker_target.address.rpartition(":")
        port = int(port_text)
        try:
            with socket.create_connection((host or "127.0.0.1", port), timeout=0.1):
                return
        except OSError:
            await asyncio.sleep(0.01)
    raise RuntimeError("AsyncWorker did not become ready")
