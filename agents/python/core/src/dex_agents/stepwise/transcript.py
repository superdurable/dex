# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deprecated alias of ``dex_agents.builtin_loop.transcript``.

.. deprecated:: 0.5.0
   Import from ``dex_agents.builtin_loop`` instead.
"""

from typing import TYPE_CHECKING, Any

import dex_agents.builtin_loop.transcript as _replacement_module
from dex_agents.stepwise._deprecated_alias import forward_deprecated_attribute

if TYPE_CHECKING:
    from dex_agents.builtin_loop.transcript import *  # noqa: F403

_RENAMED_ATTRIBUTES = {
    "encode_stepwise_message": "encode_builtin_loop_message",
    "decode_stepwise_message": "decode_builtin_loop_message",
}


def __getattr__(name: str) -> Any:
    return forward_deprecated_attribute(__name__, _replacement_module, _RENAMED_ATTRIBUTES, name)
