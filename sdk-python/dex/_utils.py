# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0


def require_name(name: str) -> str:
    if not name.strip():
        raise ValueError("durable name is required")
    return name


def require_persistence_definition_name(name: str) -> str:
    require_name(name)
    if "/" in name:
        raise ValueError("persistence definition names must not contain '/'")
    return name


def require_map_instance(instance: str) -> str:
    require_name(instance)
    if "/" in instance:
        raise ValueError("map instances must not contain '/'")
    return instance


def validate_condition_id(condition_id: str | None) -> None:
    if condition_id is not None and not condition_id:
        raise ValueError("condition ID must not be empty")
