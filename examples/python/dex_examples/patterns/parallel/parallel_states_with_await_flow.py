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
import random
from typing import Any

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    dead_end,
    go_to_multi,
    graceful_complete,
)

from dex_examples.patterns.parallel.job_seeker import JobSeeker


class NotifyUser(Step[JobSeeker]):
    def __init__(self, notify_channel: Channel[str]) -> None:
        self.notify_channel = notify_channel

    async def execute(  # type: ignore[override]
        self, context: Context, input: JobSeeker
    ) -> StepDecision:
        await asyncio.sleep(random.random() * 5)

        message = f"[FAKE] Notifying user of something: {input.id}"
        print(message)
        context.record_event("notification", message)
        self.notify_channel.publish(context, "I sent something")
        return dead_end()


class AwaitAllUsersNotified(Step[int]):
    def __init__(self, notify_channel: Channel[str]) -> None:
        self.notify_channel = notify_channel

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.until(self.notify_channel.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        context.record_event(
            "sent-notifications",
            f"[FAKE] Sent all {input} notifications",
        )
        return graceful_complete()


class Starting(Step[int]):
    def __init__(
        self,
        notify_user: NotifyUser,
        await_all_users_notified: AwaitAllUsersNotified,
    ) -> None:
        self.notify_user = notify_user
        self.await_all_users_notified = await_all_users_notified

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        movements: list[StepMovement[Any]] = [
            StepMovement.of(AwaitAllUsersNotified, input)
        ]
        for index in range(1, input + 1):
            movements.append(
                StepMovement.of(
                    NotifyUser,
                    JobSeeker(str(index), "jobseeker@indeed.com", "0987654321"),
                )
            )
        return go_to_multi(*movements)


class ParallelStatesWithAwaitFlow(Flow[int]):
    NOTIFY_CHANNEL = "test_notify_channel"

    notify_channel = Channel(NOTIFY_CHANNEL, str)

    def __init__(self) -> None:
        self.notify_user = NotifyUser(self.notify_channel)
        self.await_all_users_notified = AwaitAllUsersNotified(self.notify_channel)
        self.starting = Starting(self.notify_user, self.await_all_users_notified)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.starting).other_steps(
            self.notify_user,
            self.await_all_users_notified,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.notify_channel)
