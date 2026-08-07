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

import json
from dataclasses import asdict, is_dataclass


class ServiceDependency:
    """Stand-in for the external systems the design-pattern flows talk to."""

    def __init__(self) -> None:
        self._read_external_counter = 0

    def attempt_external_api_call(self, message: str) -> str:
        print(f"Try external system call: ({self._read_external_counter})")
        if self._read_external_counter < 2:
            self._read_external_counter += 1
            raise RuntimeError(
                "There is an error when calling external system, retry it"
            )

        self._read_external_counter = 0
        print(f"Data read from external system: ({message})")
        return "External data result"

    def external_api_call(self, message: str) -> str:
        print(f"Data read from external system: ({message})")
        return "External data result"

    def update_external_system(self, message: str) -> None:
        print(
            "update external system(like sending Kafka message or upsert to "
            f"database): {message}"
        )

    def send_email(self, subject: str, content: str) -> None:
        print(f"send an email to job seeker, title: {subject}, content: {content}")

    def upsert(self, document: object) -> None:
        print(f"upsert: {json.dumps(self._serialize(document), sort_keys=True)}")

    @staticmethod
    def _serialize(document: object) -> object:
        if is_dataclass(document) and not isinstance(document, type):
            return asdict(document)
        return document
