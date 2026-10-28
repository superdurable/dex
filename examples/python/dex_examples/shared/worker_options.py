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

"""Configures outbound polling for the example Workers."""

from dex import PullWorkerOptions

PULL_REGISTRATION_TIMEOUT_SECONDS = 30.0


def new_example_pull_worker_options() -> PullWorkerOptions:
    """Returns pull options sized for every sample Flow sharing one Worker."""
    return PullWorkerOptions(max_concurrent_tasks=64)
