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

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    go_to_multi,
    graceful_complete,
)

from dex_examples.patterns.parallel.job_seeker import JobSeeker


class SendTextMessage(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        message = f"[FAKE] Sending text message to: {input}"
        print(message)
        context.record_event("text-message", message)
        return graceful_complete()


class SendEmail(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        message = f"[FAKE] Sending email to: {input}"
        print(message)
        context.record_event("email-notification", message)
        return graceful_complete()


class Init(Step[JobSeeker]):
    def __init__(
        self,
        send_text_message: SendTextMessage,
        send_email: SendEmail,
    ) -> None:
        self.send_text_message = send_text_message
        self.send_email = send_email

    def execute(self, context: Context, input: JobSeeker) -> StepDecision:
        del context
        return go_to_multi(
            StepMovement.of(SendTextMessage, input.phone_number),
            StepMovement.of(SendEmail, input.email),
        )


class SimpleParallelStatesFlow(Flow[JobSeeker]):
    def __init__(self) -> None:
        self.send_text_message = SendTextMessage()
        self.send_email = SendEmail()
        self.init = Init(self.send_text_message, self.send_email)

    def get_steps(self) -> StepList[JobSeeker]:
        return StepList.start_step(self.init).other_steps(
            self.send_text_message,
            self.send_email,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
