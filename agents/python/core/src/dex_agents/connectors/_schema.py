# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The JSON Schema subset of Connector ``inputSchema`` values, checked before decoding."""

from __future__ import annotations

import json
import math
from collections.abc import Callable, Mapping
from typing import Any

# Keywords that constrain a value; an unlisted keyword refuses the tool, so nothing is unchecked.
_CHECKED_KEYWORDS = frozenset(
    {
        "type",
        "properties",
        "required",
        "additionalProperties",
        "items",
        "enum",
        "anyOf",
        "$ref",
        "$defs",
        "minimum",
        "maximum",
    }
)
# Annotations: format is an annotation in draft 2020-12, and the operation's codec checks it.
_ANNOTATION_KEYWORDS = frozenset(
    {"description", "title", "format", "contentEncoding", "default", "$schema", "$id", "examples"}
)
_DEFINITIONS_PREFIX = "#/$defs/"

_JSON_TYPE_CHECKS: Mapping[str, Callable[[object], bool]] = {
    "string": lambda value: isinstance(value, str),
    "boolean": lambda value: isinstance(value, bool),
    "null": lambda value: value is None,
    "array": lambda value: isinstance(value, list),
    "object": lambda value: isinstance(value, dict),
    "number": lambda value: isinstance(value, int | float) and not isinstance(value, bool),
    "integer": lambda value: (
        isinstance(value, int | float)
        and not isinstance(value, bool)
        and math.isfinite(value)
        and value == int(value)
    ),
}


def check_input_schema(schema: Mapping[str, Any]) -> None:
    """Refuse a schema that uses a keyword this checker cannot enforce.

    Raises:
        ValueError: The schema is not an object schema, uses an unknown
            keyword or type, or has a ``$ref`` outside ``#/$defs/``.
    """
    if schema.get("type") != "object":
        raise ValueError("inputSchema must have type object")
    definitions = schema.get("$defs", {})
    if not isinstance(definitions, Mapping):
        raise ValueError("inputSchema $defs must be an object")
    _check_node(schema, definitions, "inputSchema")


def _check_node(node: object, definitions: Mapping[str, Any], path: str) -> None:
    if not isinstance(node, Mapping):
        raise ValueError(f"{path} must be a schema object")
    for keyword, value in node.items():
        if keyword in _ANNOTATION_KEYWORDS:
            continue
        if keyword not in _CHECKED_KEYWORDS:
            raise ValueError(f"{path} uses {keyword}, which agent tools do not check")
        if keyword == "type":
            names = [value] if isinstance(value, str) else value
            if not isinstance(names, list) or any(name not in _JSON_TYPE_CHECKS for name in names):
                raise ValueError(f"{path}.type is not a JSON type")
        elif keyword in ("properties", "$defs"):
            if not isinstance(value, Mapping):
                raise ValueError(f"{path}.{keyword} must be an object")
            for name, child in value.items():
                _check_node(child, definitions, f"{path}.{keyword}.{name}")
        elif keyword in ("items",) or (
            keyword == "additionalProperties" and not isinstance(value, bool)
        ):
            _check_node(value, definitions, f"{path}.{keyword}")
        elif keyword == "anyOf":
            if not isinstance(value, list) or not value:
                raise ValueError(f"{path}.anyOf must be a non-empty array")
            for index, child in enumerate(value):
                _check_node(child, definitions, f"{path}.anyOf[{index}]")
        elif keyword == "$ref":
            if not isinstance(value, str) or not value.startswith(_DEFINITIONS_PREFIX):
                raise ValueError(f"{path}.$ref must point into #/$defs/")
            if value.removeprefix(_DEFINITIONS_PREFIX) not in definitions:
                raise ValueError(f"{path}.$ref names a missing definition")
        elif keyword == "required" and (
            not isinstance(value, list) or not all(isinstance(name, str) for name in value)
        ):
            raise ValueError(f"{path}.required must be an array of names")
        elif keyword == "enum" and not isinstance(value, list):
            raise ValueError(f"{path}.enum must be an array")


def find_input_violation(value: object, schema: Mapping[str, Any]) -> str | None:
    """Return the first way the arguments break the schema, or ``None``.

    The texts match the TypeScript core, such as ``arguments.amount must be integer``.
    """
    return _violation(value, schema, schema.get("$defs", {}), "arguments")


def _violation(
    value: object, schema: Mapping[str, Any], definitions: Mapping[str, Any], path: str
) -> str | None:
    reference = schema.get("$ref")
    if isinstance(reference, str):
        problem = _violation(
            value, definitions[reference.removeprefix(_DEFINITIONS_PREFIX)], definitions, path
        )
        if problem is not None:
            return problem
    if "anyOf" in schema and all(
        _violation(value, option, definitions, path) for option in schema["anyOf"]
    ):
        return f"{path} matches none of the allowed types"
    expected = schema.get("type")
    if expected is not None:
        expected_types = [expected] if isinstance(expected, str) else list(expected)
        if not any(_JSON_TYPE_CHECKS[name](value) for name in expected_types):
            return f"{path} must be {' or '.join(expected_types)}"
    if "enum" in schema and not any(_json_equal(value, allowed) for allowed in schema["enum"]):
        return f"{path} must be one of {json.dumps(schema['enum'], ensure_ascii=False)}"
    if isinstance(value, dict):
        return _object_violation(value, schema, definitions, path)
    if isinstance(value, list) and isinstance(schema.get("items"), Mapping):
        for index, item in enumerate(value):
            problem = _violation(item, schema["items"], definitions, f"{path}[{index}]")
            if problem is not None:
                return problem
    if isinstance(value, int | float) and not isinstance(value, bool):
        if "minimum" in schema and value < schema["minimum"]:
            return f"{path} must be at least {schema['minimum']}"
        if "maximum" in schema and value > schema["maximum"]:
            return f"{path} must be at most {schema['maximum']}"
    return None


def _object_violation(
    value: dict[str, Any], schema: Mapping[str, Any], definitions: Mapping[str, Any], path: str
) -> str | None:
    properties: Mapping[str, Any] = schema.get("properties", {})
    for key in schema.get("required", []):
        if key not in value:
            return f"{path}.{key} is required"
    additional = schema.get("additionalProperties", True)
    for key in sorted(value):
        item = value[key]
        if key in properties:
            problem = _violation(item, properties[key], definitions, f"{path}.{key}")
        elif additional is False:
            problem = f"{path}.{key} is not allowed"
        elif isinstance(additional, Mapping):
            problem = _violation(item, additional, definitions, f"{path}.{key}")
        else:
            problem = None
        if problem is not None:
            return problem
    return None


def _json_equal(left: object, right: object) -> bool:
    """JSON equality: booleans never equal numbers, and 1 equals 1.0."""
    if isinstance(left, bool) or isinstance(right, bool):
        return isinstance(left, bool) and isinstance(right, bool) and left == right
    if isinstance(left, int | float) and isinstance(right, int | float):
        return left == right
    if isinstance(left, list) and isinstance(right, list):
        return len(left) == len(right) and all(map(_json_equal, left, right))
    if isinstance(left, dict) and isinstance(right, dict):
        return left.keys() == right.keys() and all(_json_equal(left[k], right[k]) for k in left)
    return type(left) is type(right) and left == right
