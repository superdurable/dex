# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Describe the public API of modules as JSON, so two releases can be compared.

It uses the standard library only, so a released package's interpreter can run
it with ``python -I public_api_shape.py <module>...``.
"""

from __future__ import annotations

import dataclasses
import enum
import importlib
import inspect
import json
import sys
from typing import Any


def describe_public_api(module_names: list[str]) -> dict[str, Any]:
    """Return every public name of the modules with its constructor or signature shape."""
    shapes: dict[str, Any] = {}
    for module_name in module_names:
        module = importlib.import_module(module_name)
        for name in getattr(module, "__all__", ()):
            shapes[f"{module_name}.{name}"] = _describe(getattr(module, name))
    return shapes


def _describe(value: Any) -> dict[str, Any]:
    if inspect.isclass(value) and issubclass(value, enum.Enum):
        return {"kind": "enum", "members": {member.name: member.value for member in value}}
    if inspect.isclass(value) and dataclasses.is_dataclass(value):
        fields = [
            {
                "name": field.name,
                "has_default": field.default is not dataclasses.MISSING
                or field.default_factory is not dataclasses.MISSING,
                "kw_only": bool(field.kw_only),
            }
            for field in dataclasses.fields(value)
            if field.init
        ]
        return {"kind": "dataclass", "fields": fields, "methods": _methods(value)}
    if inspect.isclass(value):
        return {"kind": "class", "methods": _methods(value)}
    if callable(value):
        return {"kind": "function", "parameters": _parameters(value)}
    return {"kind": "value"}


def _methods(cls: type) -> dict[str, Any]:
    methods: dict[str, Any] = {}
    for name, member in vars(cls).items():
        if name.startswith("_") and name != "__init__":
            continue
        if isinstance(member, property):
            methods[name] = {"kind": "property"}
        elif callable(member) or isinstance(member, staticmethod | classmethod):
            function = member.__func__ if isinstance(member, staticmethod | classmethod) else member
            methods[name] = {"kind": "method", "parameters": _parameters(function)}
    return methods


def _parameters(function: Any) -> list[dict[str, Any]]:
    try:
        signature = inspect.signature(function)
    except (TypeError, ValueError):
        return []
    return [
        {
            "name": parameter.name,
            "kind": parameter.kind.name,
            "has_default": parameter.default is not inspect.Parameter.empty,
        }
        for parameter in signature.parameters.values()
    ]


if __name__ == "__main__":
    print(json.dumps(describe_public_api(sys.argv[1:]), sort_keys=True))
