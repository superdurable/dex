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

from typing import Callable, Protocol

from dex import AsyncClient, FlowNotActiveError

from dex_examples.products.shortlist_candidates.employer_opt_in_flow import (
    EmployerOptInFlow,
)


def employer_opt_in(employer_id: str) -> str:
    return f"shortlist_candidates_opt_in_{employer_id}"


def shortlist(employer_id: str, candidate_id: str) -> str:
    return f"shortlist_candidates_shortlist_{employer_id}_{candidate_id}"


class OptInChecker(Protocol):
    async def is_opted_in(self, employer_id: str) -> bool: ...


class ClientOptInChecker:
    """Reads opt-in state from the other Flow through a Client RPC."""

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        opt_in_flow: EmployerOptInFlow,
    ) -> None:
        # A provider defers Client creation, which needs the Registry of all Flows.
        self.client_provider = client_provider
        self.opt_in_flow = opt_in_flow

    async def is_opted_in(self, employer_id: str) -> bool:
        try:
            return await self.client_provider().invoke_rpc(
                self.opt_in_flow.is_opted_in,
                employer_opt_in(employer_id),
            )
        except FlowNotActiveError:
            return False
