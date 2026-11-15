# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector Trigger bindings that a session Flow Definition Graph declares."""

from __future__ import annotations

import importlib.metadata
import json
import re
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_CONNECTOR_ID_PATTERN = re.compile(r"[a-z][a-z0-9-]{1,62}")
_RELEASE_TAG_PATTERN = re.compile(
    r"connectors/[a-z0-9][a-z0-9-]*(?:/[a-z0-9][a-z0-9-]*)*/v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?"
)
_PACKAGE_VERSION_PATTERN = re.compile(r"v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+.a-zA-Z0-9]*)")
_JSON_POINTER_PATTERN = re.compile(r"(?:/(?:[^~/]|~[01])*)*")
_MAX_LIST_ITEMS = 256
_MANIFEST_NAME = "dex-connector.json"
_FILE_FIELDS = {
    "connectionName": "connection_name",
    "packageName": "package_name",
    "packageVersion": "package_version",
    "triggerName": "trigger_name",
    "bindingName": "binding_name",
    "factoryName": "factory_name",
    "packageDirectory": "package_directory",
    "configurationUI": "configuration_ui",
}
_UNIT_FIELDS = ("id", "unitId", "label", "description", "required", "bindings")
_FLAG_SHAPE = (
    "<connectionName>=<packageName>@<packageVersion>:<triggerName>[#<factoryName>]:<bindingName>"
)


@dataclass(frozen=True)
class SessionTriggerBindingSpec:
    """One Connector Trigger binding that a session graph declares, before its manifest is read.

    A hosting platform reads the bindings of a Flow Definition Graph to offer
    their configuration. The session Flow has no application code that
    declares bindings, so you name them here instead. Pass the spec to
    ``resolve_session_trigger_binding``, or write it as one entry of the file
    that ``dex-agents-session-graph --trigger-bindings`` reads. That JSON entry
    uses the camelCase names ``connectionName``, ``packageName``,
    ``packageVersion``, ``triggerName``, ``bindingName``, ``factoryName``,
    ``packageDirectory``, and ``configurationUI``. A field set to ``null``
    is refused; leave it out instead.

    The binding only declares that the Trigger delivers into the session Flow.
    Your Trigger target still does the delivery: for user messages it calls the
    session's ``send_user_message`` RPC.

    Example:
        >>> spec = SessionTriggerBindingSpec(
        ...     connection_name="slack-workspace",
        ...     package_name="superdurable-connector-slack",
        ...     package_version="0.6.0",
        ...     trigger_name="threadReplyCreated",
        ...     binding_name="support-thread-replies",
        ... )

    .. versionadded:: 0.6.12

    Attributes:
        connection_name: The Connector connection name that the hosting
            platform configures, such as ``"slack-workspace"``.
        package_name: The published Connector package name. It must equal
            ``package.name`` of the package's ``dex-connector.json``.
        package_version: The exact package version. It must equal
            ``package.version`` of the manifest.
        trigger_name: The Trigger name in the manifest, such as
            ``"threadReplyCreated"``.
        binding_name: The binding name, unique for this Connector, connection,
            and Trigger.
        package_directory: The directory that holds the package's
            ``dex-connector.json``. ``None`` looks the manifest up among the
            installed files of the distribution named ``package_name``.
        configuration_ui: The binding's configuration UI, as the JSON object
            ``{"units": [...]}``. ``None`` uses the Trigger's
            ``configurationUI`` from the manifest, or no units when the
            manifest has none.
        factory_name: The name of the manifest's Trigger factory, such as
            ``"defineThreadReplyCreatedTriggerBinding"``. Needed only when
            several Trigger factories of the package have this Trigger with
            different ``configurationUI``; otherwise the Trigger name selects
            the factory.
    """

    connection_name: str
    package_name: str
    package_version: str
    trigger_name: str
    binding_name: str
    package_directory: Path | None = None
    configuration_ui: Mapping[str, Any] | None = None
    factory_name: str | None = None


def resolve_session_trigger_binding(
    spec: SessionTriggerBindingSpec, search_directory: Path | None = None
) -> dict[str, Any]:
    """Read a Connector package manifest and return the session graph entry of one binding.

    The entry has the same fields, order, and values as the entry that
    ``dexcli visualize`` derives from application code that declares the same
    binding. Its configuration UI is the spec's, or else the Trigger's from
    the manifest.

    Example:
        >>> binding = resolve_session_trigger_binding(spec)
        >>> Path("definitions/support-agent.json").write_text(
        ...     render_session_flow_graph(
        ...         "SupportAgent", "support-agent.operate", connector_trigger_bindings=[binding]
        ...     ),
        ...     encoding="utf-8",
        ... )

    .. versionadded:: 0.6.12

    Args:
        spec: The binding to resolve.
        search_directory: Where a relative ``package_directory`` starts.
            ``None`` means the current working directory.

    Returns:
        The ``v2.connectorTriggerBindings`` entry as a JSON-ready dictionary.

    Raises:
        ValueError: If the package is not installed, has no
            ``dex-connector.json``, has another name or version, has no
            Trigger named ``trigger_name`` (or no such factory named
            ``factory_name``), or has several Trigger factories for it with
            different ``configurationUI`` and the spec names none, or if a
            spec field or the configuration UI is invalid.
    """
    label = f"Trigger binding {spec.binding_name}"
    for field in (
        "connection_name",
        "package_name",
        "package_version",
        "trigger_name",
        "binding_name",
    ):
        value = getattr(spec, field)
        if not isinstance(value, str) or not value.strip():
            raise ValueError(f"{label}: {field} must be a non-empty string")
    manifest_path = _connector_manifest_path(spec, search_directory or Path.cwd())
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ValueError(
            f"{label}: cannot read Connector manifest {manifest_path}: {error}"
        ) from error
    connector_id, release_tag, package, triggers = _connector_manifest_identity(
        manifest, manifest_path
    )
    if package["name"] != spec.package_name or package["version"] != spec.package_version:
        raise ValueError(
            f"{label}: Connector manifest {manifest_path} is for "
            f"{package['name']}@{package['version']}, "
            f"not {spec.package_name}@{spec.package_version}"
        )
    if spec.factory_name is not None and (
        not isinstance(spec.factory_name, str) or not spec.factory_name
    ):
        raise ValueError(f"{label}: factoryName must be a non-empty string")
    package_text = f"{spec.package_name}@{spec.package_version}"
    same_trigger = [trigger for trigger in triggers if trigger[1] == spec.trigger_name]
    if not same_trigger:
        known = ", ".join(dict.fromkeys(trigger[1] for trigger in triggers)) or "none"
        raise ValueError(
            f"{label}: {package_text} has no Trigger {spec.trigger_name}; its Triggers: {known}"
        )
    candidates = [
        trigger
        for trigger in same_trigger
        if spec.factory_name is None or trigger[0] == spec.factory_name
    ]
    if not candidates:
        known = ", ".join(trigger[0] for trigger in same_trigger)
        raise ValueError(
            f"{label}: {package_text} has no Trigger factory {spec.factory_name} for Trigger "
            f"{spec.trigger_name}; its factories: {known}"
        )
    manifest_uis = {
        json.dumps(_canonical_configuration_ui(trigger[2] or {"units": []}, label))
        for trigger in candidates
    }
    if len(manifest_uis) > 1:
        names = ", ".join(trigger[0] for trigger in candidates)
        raise ValueError(
            f"{label}: {package_text} has several Trigger factories for {spec.trigger_name} "
            f"with different configurationUI: {names}; name one with factoryName"
        )
    configuration_ui = spec.configuration_ui
    if configuration_ui is None:
        configuration_ui = candidates[0][2] or {"units": []}
    return canonical_session_trigger_binding(
        {
            "connectorId": connector_id,
            "triggerName": spec.trigger_name,
            "connectionName": spec.connection_name,
            "bindingName": spec.binding_name,
            "releaseTag": release_tag,
            "package": package,
            "configurationUI": configuration_ui,
        }
    )


def parse_session_trigger_binding_flag(text: str) -> SessionTriggerBindingSpec:
    """Parse one ``--trigger-binding`` value.

    The value is
    ``<connectionName>=<packageName>@<packageVersion>:<triggerName>[#<factoryName>]:<bindingName>``.

    Args:
        text: The flag value.

    Returns:
        The spec, without a package directory or configuration UI.

    Raises:
        ValueError: If the value does not have that shape.
    """
    connection_name, separator, rest = text.partition("=")
    parts = rest.split(":")
    package_text = ":".join(parts[:-2])
    version_separator = package_text.rfind("@")
    trigger_name, factory_separator, factory_name = (
        parts[-2] if len(parts) >= 3 else ""
    ).partition("#")
    values = (
        connection_name,
        package_text[:version_separator] if version_separator > 0 else "",
        package_text[version_separator + 1 :],
        trigger_name,
        parts[-1] if len(parts) >= 3 else "",
    )
    if not separator or not all(values) or (factory_separator and not factory_name):
        raise ValueError(f"--trigger-binding {json.dumps(text)} must be {_FLAG_SHAPE}")
    return SessionTriggerBindingSpec(*values, factory_name=factory_name or None)


def read_session_trigger_bindings_file(path: Path) -> list[SessionTriggerBindingSpec]:
    """Read a ``--trigger-bindings`` file: a JSON array of binding objects.

    Args:
        path: The file. A relative ``packageDirectory`` in it is relative to
            the file's directory.

    Returns:
        The specs, in file order.

    Raises:
        ValueError: If the file cannot be read, is not a JSON array of
            objects, or has an unknown field.
    """
    try:
        entries = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ValueError(f"cannot read Trigger bindings file {path}: {error}") from error
    if not isinstance(entries, list):
        raise ValueError(f"Trigger bindings file {path} must contain a JSON array")
    specs = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            raise ValueError(f"Trigger bindings file {path}: entry {index} must be an object")
        unknown = [name for name in entry if name not in _FILE_FIELDS]
        if unknown:
            raise ValueError(
                f"Trigger bindings file {path}: entry {index} has unknown fields "
                f"{', '.join(unknown)}"
            )
        null_fields = [name for name, value in entry.items() if value is None]
        if null_fields:
            raise ValueError(
                f"Trigger bindings file {path}: entry {index} field {null_fields[0]} "
                "must not be null"
            )
        arguments = {_FILE_FIELDS[name]: value for name, value in entry.items()}
        package_directory = arguments.get("package_directory")
        if package_directory is not None:
            if not isinstance(package_directory, str):
                raise ValueError(
                    f"Trigger bindings file {path}: entry {index} packageDirectory must be a string"
                )
            arguments["package_directory"] = (path.parent / package_directory).resolve()
        try:
            specs.append(SessionTriggerBindingSpec(**arguments))
        except TypeError as error:
            raise ValueError(f"Trigger bindings file {path}: entry {index}: {error}") from error
    return specs


def canonical_session_trigger_binding(binding: Mapping[str, Any]) -> dict[str, Any]:
    """Return a binding entry with its fields in graph order, and check it.

    Args:
        binding: An entry from ``resolve_session_trigger_binding``.

    Returns:
        A new dictionary in the order ``dexcli visualize`` writes.

    Raises:
        ValueError: If a field is missing or the configuration UI is invalid.
    """
    label = f"Trigger binding {binding.get('bindingName')}"
    for field in ("connectorId", "triggerName", "connectionName", "bindingName", "releaseTag"):
        value = binding.get(field)
        if not isinstance(value, str) or not value:
            raise ValueError(f"{label}: {field} must be a non-empty string")
    package = binding.get("package")
    if not isinstance(package, Mapping) or not all(
        isinstance(package.get(key), str) for key in ("ecosystem", "name", "version")
    ):
        raise ValueError(f"{label}: package must have ecosystem, name, and version")
    return {
        "connectorId": binding["connectorId"],
        "triggerName": binding["triggerName"],
        "connectionName": binding["connectionName"],
        "bindingName": binding["bindingName"],
        "modulePath": "",
        "moduleVersion": "",
        "releaseTag": binding["releaseTag"],
        "package": {
            "ecosystem": package["ecosystem"],
            "name": package["name"],
            "version": package["version"],
        },
        "configurationEnabled": True,
        "configurationUI": _canonical_configuration_ui(binding.get("configurationUI"), label),
    }


def _connector_manifest_path(spec: SessionTriggerBindingSpec, search_directory: Path) -> Path:
    label = f"Trigger binding {spec.binding_name}"
    if spec.package_directory is not None:
        manifest_path = search_directory / spec.package_directory / _MANIFEST_NAME
        if not manifest_path.is_file():
            raise ValueError(
                f"{label}: package {spec.package_name} has no Connector manifest at {manifest_path}"
            )
        return manifest_path
    try:
        distribution = importlib.metadata.distribution(spec.package_name)
    except importlib.metadata.PackageNotFoundError as error:
        raise ValueError(
            f"{label}: package {spec.package_name} is not installed for the Python "
            f"interpreter {sys.executable}"
        ) from error
    manifests = [
        file
        for file in distribution.files or []
        if file.name == _MANIFEST_NAME and len(file.parts) == 2
    ]
    if len(manifests) != 1:
        raise ValueError(
            f"{label}: package {spec.package_name} must install one Connector manifest "
            f"<module>/{_MANIFEST_NAME}, found {len(manifests)}"
        )
    return Path(str(distribution.locate_file(manifests[0])))


def _connector_manifest_identity(
    manifest: Any, manifest_path: Path
) -> tuple[str, str, dict[str, str], list[tuple[str, str, Mapping[str, Any] | None]]]:
    def invalid(reason: str) -> ValueError:
        return ValueError(f"Connector manifest {manifest_path}: {reason}")

    if not isinstance(manifest, dict):
        raise invalid("must be a JSON object")
    connector_id = manifest.get("connectorId")
    release_tag = manifest.get("releaseTag")
    package = manifest.get("package")
    factories = manifest.get("factories")
    if not isinstance(connector_id, str) or _CONNECTOR_ID_PATTERN.fullmatch(connector_id) is None:
        raise invalid(f"connectorId {json.dumps(connector_id)} must be lowercase kebab-case")
    if not isinstance(release_tag, str) or _RELEASE_TAG_PATTERN.fullmatch(release_tag) is None:
        raise invalid(
            f"releaseTag {json.dumps(release_tag)} must be an exact tag "
            "such as connectors/<name>/v1.2.3"
        )
    if (
        not isinstance(package, dict)
        or package.get("ecosystem") not in ("npm", "pypi")
        or not isinstance(package.get("name"), str)
        or not package["name"].strip()
        or not isinstance(package.get("version"), str)
        or _PACKAGE_VERSION_PATTERN.fullmatch(package["version"]) is None
    ):
        raise invalid("package must have an npm or pypi ecosystem, a name, and an exact version")
    if not isinstance(factories, list):
        raise invalid("factories must be an array")
    triggers: list[tuple[str, str, Mapping[str, Any] | None]] = []
    for factory in factories:
        if not isinstance(factory, dict) or factory.get("kind") != "trigger":
            continue
        trigger_name = factory.get("triggerName")
        if not isinstance(trigger_name, str) or not trigger_name:
            raise invalid(f"Trigger factory {factory.get('name')} requires triggerName")
        configuration_ui = factory.get("configurationUI")
        if "configurationUI" in factory and (
            not isinstance(configuration_ui, dict)
            or not isinstance(configuration_ui.get("units"), list)
        ):
            raise invalid(f"Trigger factory {factory.get('name')}: configurationUI requires units")
        for unit in (configuration_ui or {}).get("units", []):
            if (
                not isinstance(unit, dict)
                or not isinstance(unit.get("required"), bool)
                or not isinstance(unit.get("bindings"), list)
            ):
                raise invalid(
                    f"Trigger factory {factory.get('name')}: configurationUI units require "
                    "required and bindings"
                )
        triggers.append((str(factory.get("name")), trigger_name, configuration_ui))
    identity = {
        "ecosystem": package["ecosystem"],
        "name": package["name"],
        "version": package["version"],
    }
    return connector_id, release_tag, identity, triggers


def _canonical_configuration_ui(configuration_ui: Any, label: str) -> dict[str, Any]:
    def invalid(reason: str) -> ValueError:
        return ValueError(f"{label}: configurationUI {reason}")

    if not isinstance(configuration_ui, Mapping) or not isinstance(
        configuration_ui.get("units"), Sequence
    ):
        raise invalid("must be an object with a units array")
    units_in = configuration_ui["units"]
    if isinstance(units_in, str) or len(units_in) > _MAX_LIST_ITEMS:
        raise invalid(f"must have a units array of at most {_MAX_LIST_ITEMS} items")
    unit_ids: set[str] = set()
    units = []
    for unit in units_in:
        if not isinstance(unit, Mapping) or any(name not in _UNIT_FIELDS for name in unit):
            raise invalid(f"units take only {', '.join(_UNIT_FIELDS)}")
        unit_id = unit.get("id")
        ui_unit_id = unit.get("unitId")
        unit_label = unit.get("label")
        description = unit.get("description")
        required = unit.get("required", False)
        bindings_in = unit.get("bindings", [])
        if (
            not isinstance(unit_id, str)
            or not unit_id
            or unit_id in unit_ids
            or not isinstance(ui_unit_id, str)
            or not ui_unit_id
            or not isinstance(unit_label, str)
            or not unit_label.strip()
        ):
            raise invalid("units require unique IDs, unitId, and label")
        unit_ids.add(unit_id)
        if "description" in unit and not isinstance(description, str):
            raise invalid(f"unit {unit_id} description must be a string")
        if not isinstance(required, bool):
            raise invalid(f"unit {unit_id} required must be a boolean")
        if not isinstance(bindings_in, list) or len(bindings_in) > _MAX_LIST_ITEMS:
            raise invalid(
                f"unit {unit_id} bindings must be an array of at most {_MAX_LIST_ITEMS} items"
            )
        ports: set[str] = set()
        bindings = []
        for binding in bindings_in:
            port = binding.get("port") if isinstance(binding, Mapping) else None
            if not isinstance(port, str) or not port or port in ports:
                raise invalid(f"unit {unit_id} bindings require unique non-empty ports")
            ports.add(port)
            pointer = binding.get("jsonPointer")
            if not isinstance(pointer, str) or _JSON_POINTER_PATTERN.fullmatch(pointer) is None:
                raise invalid(
                    f"unit {unit_id} binding {port} has invalid JSON pointer {json.dumps(pointer)}"
                )
            bindings.append({"port": port, "jsonPointer": pointer})
        canonical_unit: dict[str, Any] = {"id": unit_id, "unitId": ui_unit_id, "label": unit_label}
        if description:
            canonical_unit["description"] = description
        canonical_unit["required"] = required
        canonical_unit["bindings"] = bindings
        units.append(canonical_unit)
    return {"units": units}
