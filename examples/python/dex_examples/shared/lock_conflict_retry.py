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

"""Retry for RPCs that lose a registered lock race."""

from __future__ import annotations

import asyncio
import random
from collections.abc import Awaitable, Callable
from typing import TypeVar

from dex import RpcLockConflictError

LOCK_CONFLICT_ATTEMPTS = 5
LOCK_CONFLICT_BACKOFF_SECONDS = 0.2

ResultT = TypeVar("ResultT")


async def invoke_rpc_retrying_lock_conflict(
    invoke: Callable[[], Awaitable[ResultT]],
) -> ResultT:
    """Invokes an RPC and retries a lock conflict with bounded backoff.

    Retrying is safe: a conflicting call committed nothing.
    Returns the RPC result, or raises the last ``RpcLockConflictError`` when every
    attempt conflicts. The HTTP app maps that error to 409.
    """
    for attempt in range(1, LOCK_CONFLICT_ATTEMPTS):
        try:
            return await invoke()
        except RpcLockConflictError:
            # Jitter spreads out callers that conflicted at the same time.
            jitter = random.uniform(0, LOCK_CONFLICT_BACKOFF_SECONDS)
            await asyncio.sleep(attempt * LOCK_CONFLICT_BACKOFF_SECONDS + jitter)
    return await invoke()
