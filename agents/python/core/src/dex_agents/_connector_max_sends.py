# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector send cap bounds, kept apart so the session never imports the optional SDK."""

from __future__ import annotations

#: The largest send cap a manifest or a tool policy may set, as the SDK's ``MAXIMUM_MAX_SENDS``.
CONNECTOR_MAX_SENDS_CEILING = 10

#: The smallest ``agent.maxSends`` a manifest may declare; a tool policy may also set 1.
CONNECTOR_MANIFEST_MINIMUM_MAX_SENDS = 2


def is_connector_max_sends(value: object) -> bool:
    """Report whether a value is an integer send cap from 1 to ``CONNECTOR_MAX_SENDS_CEILING``."""
    return (
        isinstance(value, int)
        and not isinstance(value, bool)
        and 1 <= value <= CONNECTOR_MAX_SENDS_CEILING
    )
