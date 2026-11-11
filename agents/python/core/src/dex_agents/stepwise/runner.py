# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop.runner``.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead.
"""

from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop.runner as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.builtin_loop.runner import *  # noqa: F403

_RENAMED_ATTRIBUTES = {
    "StepwiseAgentRunner": "BuiltinLoopAgentRunner",
    "stepwise_agent_session_flow": "builtin_agent_session_flow",
    "stepwise_agent_session_options": "builtin_agent_session_options",
}


def __getattr__(name: str) -> Any:
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)
