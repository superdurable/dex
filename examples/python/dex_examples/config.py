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

import os
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path

from dex import StartFlowOptions

DEFAULT_TIMEOUT = timedelta(hours=1)


@dataclass(frozen=True)
class ExamplesConfig:
    server_address: str
    worker_bind_address: str
    worker_target: str | None
    http_address: str
    blob_cache_dir: Path

    @staticmethod
    def from_env() -> ExamplesConfig:
        return ExamplesConfig(
            server_address=os.environ.get(
                "DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"
            ),
            worker_bind_address=os.environ.get(
                "DEX_WORKER_BIND_ADDRESS", "127.0.0.1:8803"
            ),
            worker_target=os.environ.get("DEX_WORKER_TARGET") or None,
            http_address=os.environ.get("DEX_EXAMPLES_HTTP_ADDRESS", "127.0.0.1:8080"),
            blob_cache_dir=Path(
                os.environ.get(
                    "DEX_BLOB_CACHE_DIR",
                    "/tmp/dex-examples-python-blob-cache",
                )
            ),
        )


def start_options() -> StartFlowOptions:
    return StartFlowOptions(timeout=DEFAULT_TIMEOUT)
