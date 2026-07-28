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

from dataclasses import dataclass

from dex.object_encoder import ObjectEncoder


@dataclass
class ClientOptions:
    server_url: str
    worker_url: str
    object_encoder: ObjectEncoder
    api_timeout: int = 60
    long_poll_api_max_wait_time_seconds: int = 10

    @classmethod
    def local_default(cls):
        return ClientOptions(
            server_url="http://localhost:8801",
            worker_url="http://localhost:8802",
            object_encoder=ObjectEncoder.default,
        )
