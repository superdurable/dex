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

"""Track activity and act when its durable timer expires."""

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

TRACKER_DURATION = timedelta(minutes=5)


class ProcessInactivenessStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        print("No activity arrived before the timer fired")
        return graceful_complete()


class TrackerStep(Step[None]):
    def __init__(
        self,
        process_inactiveness: ProcessInactivenessStep,
        active_channel: Channel[None],
    ) -> None:
        self.process_inactiveness = process_inactiveness
        self.active_channel = active_channel

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.any_of(
            Timer.by_duration(TRACKER_DURATION),
            self.active_channel.for_one(),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        if context.has_timer_fired():
            return go_to(ProcessInactivenessStep, None)
        return go_to(TrackerStep, None)


class InactivenessTrackerFlow(Flow[None]):
    ACTIVE_CHANNEL = "Active"

    active_channel = Channel(ACTIVE_CHANNEL, type(None))

    def __init__(self) -> None:
        self.process_inactiveness = ProcessInactivenessStep()
        self.tracker_step = TrackerStep(
            self.process_inactiveness,
            self.active_channel,
        )

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.tracker_step).other_steps(
            self.process_inactiveness
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.active_channel)

    @rpc
    def record_activity(self, context: Context) -> None:
        self.active_channel.publish(context, None)
