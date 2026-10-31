# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run Google ADK agents as durable Dex agent sessions, in Step mode.

``adk_agent_session_flow`` builds the shared ``dex_agents.AgentSessionFlow``
around an ADK agent or App. Each ``RunModel`` Step lets ADK make one model
call. Every tool call the model asks for, READ included, pauses the ADK
invocation through ADK's tool confirmation and runs in its own Dex Step, in
parallel, so a crash repeats at most the one call that was running. See the
package README.
"""

from dex_agents_google_adk.runner import (
    DEFAULT_ADK_USER_ID,
    AdkAgentRunner,
    adk_agent_session_flow,
)
from dex_agents_google_adk.tools import (
    IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY,
    idempotency_key_for_tool_call,
)

__all__ = [
    "DEFAULT_ADK_USER_ID",
    "IDEMPOTENCY_KEY_CUSTOM_METADATA_KEY",
    "AdkAgentRunner",
    "adk_agent_session_flow",
    "idempotency_key_for_tool_call",
]
