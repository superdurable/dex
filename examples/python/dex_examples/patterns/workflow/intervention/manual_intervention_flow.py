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

"""Handles API-call failure scenarios with manual retry or skip intervention."""

from __future__ import annotations

from typing import Callable

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    go_to,
    graceful_complete,
)


class Final(Step[None]):
    def __init__(self, number_of_retries: Attribute[int]) -> None:
        self.number_of_retries = number_of_retries

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        retries = self.number_of_retries.get(context)
        return graceful_complete(f"Workflow Completed. Number of retries: {retries}")


class GetData(Step[bool]):
    def __init__(
        self,
        error_provider: Callable[[], Error],
        final: Final,
        data_channel: Channel[str],
        number_of_retries: Attribute[int],
    ) -> None:
        self.error_provider = error_provider
        self.final = final
        self.data_channel = data_channel
        self.number_of_retries = number_of_retries

    def wait_for(self, context: Context, input: bool) -> Wait:
        del context, input
        print("Waiting for incoming data")
        return Wait.all_of(self.data_channel.for_one())

    def execute(self, context: Context, input: bool) -> StepDecision:
        if input:
            self.number_of_retries.set(context, self.number_of_retries.get(context) + 1)
        try:
            self._pretend_api_call(context)
        except ValueError:
            return go_to(self.error_provider(), None)
        return go_to(self.final, None)

    def _pretend_api_call(self, context: Context) -> None:
        results = self.data_channel.results(context)
        if results:
            data = results[0]
            print(f"Received data result: {data}")
            if data == "failed":
                raise ValueError("Non-retryable exception")


class Error(Step[None]):
    def __init__(
        self,
        get_data: GetData,
        final: Final,
        retry_signal: Channel[None],
        skip_signal: Channel[None],
    ) -> None:
        self.get_data = get_data
        self.final = final
        self.retry_signal = retry_signal
        self.skip_signal = skip_signal

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.any_of(self.retry_signal.for_one(), self.skip_signal.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        retry = bool(self.retry_signal.results(context))
        print(
            "signal received: "
            + (self.retry_signal.name if retry else self.skip_signal.name)
        )
        if retry:
            return go_to(self.get_data, True)
        return go_to(self.final, None)


class Init(Step[None]):
    def __init__(self, get_data: GetData, number_of_retries: Attribute[int]) -> None:
        self.get_data = get_data
        self.number_of_retries = number_of_retries

    def execute(self, context: Context, input: None) -> StepDecision:
        del input
        self.number_of_retries.set(context, 0)
        return go_to(self.get_data, False)


class ManualInterventionFlow(Flow[None]):
    INTERNAL_CHANNEL_COMMAND = "internal_channel_command"
    SIGNAL_CHANNEL_COMMAND_RETRY = "signal_channel_command_retry"
    SIGNAL_CHANNEL_COMMAND_SKIP = "signal_channel_command_skip"
    NUMBER_OF_RETRIES = "number_of_retries"

    data_channel = Channel(INTERNAL_CHANNEL_COMMAND, str)
    retry_signal = Channel[None](SIGNAL_CHANNEL_COMMAND_RETRY, type(None))
    skip_signal = Channel[None](SIGNAL_CHANNEL_COMMAND_SKIP, type(None))
    number_of_retries = Attribute(NUMBER_OF_RETRIES, int)

    def __init__(self) -> None:
        self.final = Final(self.number_of_retries)
        self.get_data = GetData(
            lambda: self.error,
            self.final,
            self.data_channel,
            self.number_of_retries,
        )
        self.error = Error(
            self.get_data,
            self.final,
            self.retry_signal,
            self.skip_signal,
        )
        self.init = Init(self.get_data, self.number_of_retries)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.init).other_steps(
            self.get_data,
            self.error,
            self.final,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.data_channel,
            self.retry_signal,
            self.skip_signal,
            self.number_of_retries,
        )
