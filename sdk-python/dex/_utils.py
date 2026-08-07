# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0


class PhaseNotImplementedError(RuntimeError):
    """Raised when a contract reaches an unimplemented runtime phase."""


def require_name(name: str) -> None:
    if not name.strip():
        raise ValueError("durable name is required")


def validate_condition_id(condition_id: str | None) -> None:
    if condition_id is not None and not condition_id:
        raise ValueError("condition ID must not be empty")
