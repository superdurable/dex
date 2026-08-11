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

from dataclasses import dataclass


@dataclass
class UserProfile:
    display_name: str
    email: str
    marketing_opt_in: bool

    def __post_init__(self) -> None:
        self.validate()

    def validate(self) -> None:
        if not self.display_name:
            raise ValueError("display_name is required")
        if not self.email:
            raise ValueError("email is required")


@dataclass
class UserProfileRequest:
    user_id: str
    display_name: str
    email: str
    marketing_opt_in: bool

    def __post_init__(self) -> None:
        if not self.user_id:
            raise ValueError("user_id is required")

    def profile(self) -> UserProfile:
        return UserProfile(
            self.display_name,
            self.email,
            self.marketing_opt_in,
        )
