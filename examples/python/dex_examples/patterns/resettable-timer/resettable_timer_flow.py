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

"""A timer whose expiry triggers an action; a reset message restarts it."""

from __future__ import annotations

from datetime import timedelta

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

TIMER_DURATION = timedelta(minutes=5)


class TimerExpired(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        print("Timer fired; this is where we would send an email")
        return graceful_complete()


class ResettableTimerStep(Step[None]):
    def __init__(
        self,
        timer_expired: TimerExpired,
        reset_timer_channel: Channel[str],
    ) -> None:
        self.timer_expired = timer_expired
        self.reset_timer_channel = reset_timer_channel

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(
            Timer.by_duration(TIMER_DURATION),
            self.reset_timer_channel.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        if context.has_timer_fired():
            return go_to(self.timer_expired, None)
        return go_to(self, None)


class ResettableTimerFlow(Flow[None]):
    RESET_TIMER_CHANNEL = "RESET_TIMER_CHANNEL"

    reset_timer_channel = Channel(RESET_TIMER_CHANNEL, str)

    def __init__(self) -> None:
        self.timer_expired = TimerExpired()
        self.resettable_timer = ResettableTimerStep(
            self.timer_expired,
            self.reset_timer_channel,
        )

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.resettable_timer).other_steps(
            self.timer_expired
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.reset_timer_channel)

    @rpc
    def send_reset_message(self, context: Context) -> None:
        self.reset_timer_channel.publish(context, "reset")
