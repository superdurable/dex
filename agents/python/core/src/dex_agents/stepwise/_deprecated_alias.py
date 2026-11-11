# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Forwards the deprecated ``dex_agents.stepwise`` modules to ``dex_agents.builtin_loop``."""

import warnings
from collections.abc import Mapping
from types import ModuleType
from typing import Any


def forward_deprecated_attribute(
    deprecated_module_name: str,
    replacement_module: ModuleType,
    renamed_attributes: Mapping[str, str],
    attribute_name: str,
) -> Any:
    """Return an attribute of the replacement module and warn that the old path is deprecated.

    Args:
        deprecated_module_name: The deprecated module's name, such as ``dex_agents.stepwise``.
        replacement_module: The module that now defines the attribute.
        renamed_attributes: Old attribute names mapped to their new names.
        attribute_name: The attribute the caller asked the deprecated module for.

    Returns:
        The replacement module's attribute.

    Raises:
        AttributeError: If the replacement module has no such attribute.
    """
    replacement_name = renamed_attributes.get(attribute_name, attribute_name)
    try:
        value = getattr(replacement_module, replacement_name)
    except AttributeError:
        raise AttributeError(
            f"module {deprecated_module_name!r} has no attribute {attribute_name!r}"
        ) from None
    warnings.warn(
        f"{deprecated_module_name}.{attribute_name} is deprecated; use "
        f"{replacement_module.__name__}.{replacement_name} instead.",
        DeprecationWarning,
        # Points at the caller of the deprecated module's __getattr__.
        stacklevel=3,
    )
    return value
