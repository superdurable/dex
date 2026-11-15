# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Session graph Connector Trigger bindings match the Flow analyzer and render like every core."""

from __future__ import annotations

import dataclasses
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    SessionTriggerBindingSpec,
    render_session_flow_graph,
    resolve_session_trigger_binding,
)
from dex_agents.session_graph import main
from dex_agents.session_trigger_bindings import (
    parse_session_trigger_binding_flag,
    read_session_trigger_bindings_file,
)
from tests.test_conformance import SUITE_DIR

SUITE = json.loads((SUITE_DIR / "session_graph_trigger_bindings.json").read_text(encoding="utf-8"))
PACKAGE_DIRECTORY = SUITE_DIR / SUITE["package_directory"]
FLOW_TYPE = SUITE["flow_type"]
ACTION_PERMISSION = SUITE["action_permission"]


def spec_of(entry: dict[str, Any], **changes: Any) -> SessionTriggerBindingSpec:
    spec = SessionTriggerBindingSpec(
        connection_name=entry["connectionName"],
        package_name=entry["packageName"],
        package_version=entry["packageVersion"],
        trigger_name=entry["triggerName"],
        binding_name=entry["bindingName"],
        package_directory=PACKAGE_DIRECTORY,
        configuration_ui=entry.get("configurationUI"),
    )
    return dataclasses.replace(spec, **changes)


def resolve_case(index: int) -> dict[str, Any]:
    return resolve_session_trigger_binding(spec_of(SUITE["cases"][index]["entry"]))


def compact(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def run_command(
    cwd: Path, *options: str, python_path: Path | None = None
) -> subprocess.CompletedProcess[str]:
    environment = dict(os.environ)
    if python_path is not None:
        environment["PYTHONPATH"] = os.pathsep.join(
            [str(python_path), environment.get("PYTHONPATH", "")]
        )
    return subprocess.run(
        [
            sys.executable,
            "-m",
            "dex_agents.session_graph",
            "--flow-type",
            FLOW_TYPE,
            "--permission",
            ACTION_PERMISSION,
            *options,
        ],
        capture_output=True,
        cwd=cwd,
        env=environment,
        text=True,
        encoding="utf-8",
    )


def install_python_connector(site: Path) -> None:
    """Install the suite's Connector as the distribution dex-test-messages under site."""
    manifest = json.loads((PACKAGE_DIRECTORY / "dex-connector.json").read_text(encoding="utf-8"))
    manifest["package"] = {"ecosystem": "pypi", "name": "dex-test-messages", "version": "2.3.4"}
    module = site / "dex_test_messages"
    module.mkdir(parents=True)
    (module / "__init__.py").write_text("", encoding="utf-8")
    (module / "dex-connector.json").write_text(json.dumps(manifest), encoding="utf-8")
    metadata = site / "dex_test_messages-2.3.4.dist-info"
    metadata.mkdir()
    (metadata / "METADATA").write_text(
        "Metadata-Version: 2.1\nName: dex-test-messages\nVersion: 2.3.4\n", encoding="utf-8"
    )
    (metadata / "RECORD").write_text(
        "dex_test_messages/__init__.py,,\ndex_test_messages/dex-connector.json,,\n"
        "dex_test_messages-2.3.4.dist-info/METADATA,,\n",
        encoding="utf-8",
    )


def test_each_resolved_binding_is_the_entry_the_flow_analyzer_derives_from_code() -> None:
    for index, case in enumerate(SUITE["cases"]):
        assert compact(resolve_case(index)) == compact(case["binding"]), index


def test_the_flag_form_parses_to_the_suite_s_entry() -> None:
    for case in SUITE["cases"]:
        if "flag" in case:
            parsed = parse_session_trigger_binding_flag(case["flag"])
            assert parsed == spec_of(case["entry"], package_directory=None)
    for invalid in [
        "=p@1.0.0:t:b",
        "c=p:t:b",
        "c=@scope/p:t:b",
        "c=p@1.0.0:t:",
        "c=p@1.0.0:t",
        "c",
    ]:
        with pytest.raises(ValueError, match="--trigger-binding"):
            parse_session_trigger_binding_flag(invalid)


def test_a_graph_with_two_bindings_renders_the_same_bytes_as_every_core() -> None:
    text = render_session_flow_graph(
        FLOW_TYPE, ACTION_PERMISSION, connector_trigger_bindings=[resolve_case(0), resolve_case(1)]
    )
    assert hashlib.sha256(text.encode()).hexdigest() == SUITE["rendered_sha256"]
    graph = json.loads(text)
    assert list(graph["v2"])[-1] == "connectorTriggerBindings"
    assert graph["v2"]["connectorTriggerBindings"] == [case["binding"] for case in SUITE["cases"]]


def test_a_graph_with_one_binding_adds_only_that_binding() -> None:
    with_binding = json.loads(
        render_session_flow_graph(
            FLOW_TYPE, ACTION_PERMISSION, connector_trigger_bindings=[resolve_case(0)]
        )
    )
    without = json.loads(render_session_flow_graph(FLOW_TYPE, ACTION_PERMISSION))
    assert with_binding["v2"].pop("connectorTriggerBindings") == [SUITE["cases"][0]["binding"]]
    assert with_binding == without


def test_without_bindings_the_graph_text_is_unchanged(tmp_path: Path) -> None:
    without = render_session_flow_graph(FLOW_TYPE, ACTION_PERMISSION)
    assert (
        render_session_flow_graph(FLOW_TYPE, ACTION_PERMISSION, connector_trigger_bindings=[])
        == without
    )
    assert run_command(tmp_path).stdout == without


def test_an_unknown_trigger_a_missing_package_and_a_mismatched_version_fail_with_the_reason(
    tmp_path: Path,
) -> None:
    entry = SUITE["cases"][0]["entry"]
    with pytest.raises(
        ValueError, match="has no Trigger messageDeleted; its Triggers: replyCreated, threadCreated"
    ):
        resolve_session_trigger_binding(spec_of(entry, trigger_name="messageDeleted"))
    with pytest.raises(ValueError, match="package dex-test-missing is not installed"):
        resolve_session_trigger_binding(
            spec_of(entry, package_name="dex-test-missing", package_directory=None)
        )
    with pytest.raises(ValueError, match="has no Connector manifest at"):
        resolve_session_trigger_binding(spec_of(entry, package_directory=tmp_path))
    with pytest.raises(
        ValueError, match="is for @dex-test/messages@2.3.4, not @dex-test/messages@2.3.5"
    ):
        resolve_session_trigger_binding(spec_of(entry, package_version="2.3.5"))
    bad_pointer = {
        "units": [
            {
                "id": "a",
                "unitId": "textInput",
                "label": "A",
                "bindings": [{"port": "p", "jsonPointer": "x"}],
            }
        ]
    }
    with pytest.raises(ValueError, match="invalid JSON pointer"):
        resolve_session_trigger_binding(spec_of(entry, configuration_ui=bad_pointer))


@pytest.mark.parametrize("case", SUITE["file_cases"], ids=lambda case: case["name"])
def test_every_file_case_resolves_to_its_entry_or_fails_with_its_error(
    case: dict[str, Any], tmp_path: Path
) -> None:
    entry = dict(case["entry"])
    if "packageDirectory" not in entry:
        entry["packageDirectory"] = str(SUITE_DIR / case["package_directory"])
    bindings_file = tmp_path / "trigger-bindings.json"
    bindings_file.write_text(json.dumps([entry]), encoding="utf-8")

    def resolve() -> list[dict[str, Any]]:
        specs = read_session_trigger_bindings_file(bindings_file)
        return [resolve_session_trigger_binding(spec) for spec in specs]

    if "error" in case:
        with pytest.raises(ValueError) as raised:
            resolve()
        assert case["error"] in str(raised.value)
    else:
        assert compact(resolve()) == compact([case["binding"]])
    if "flag" in case:
        parsed = parse_session_trigger_binding_flag(case["flag"])
        assert parsed == dataclasses.replace(
            spec_of(case["entry"], package_directory=None),
            factory_name=case["entry"].get("factoryName"),
        )


def test_two_bindings_with_one_identity_are_refused() -> None:
    with pytest.raises(ValueError, match="declared twice"):
        render_session_flow_graph(
            FLOW_TYPE,
            ACTION_PERMISSION,
            connector_trigger_bindings=[resolve_case(0), resolve_case(0)],
        )


def test_an_installed_distribution_supplies_its_manifest(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    install_python_connector(tmp_path)
    monkeypatch.syspath_prepend(str(tmp_path))
    flag = "workspace=dex-test-messages@2.3.4:replyCreated:session-replies"
    binding = resolve_session_trigger_binding(parse_session_trigger_binding_flag(flag))
    expected = dict(SUITE["cases"][0]["binding"])
    expected["package"] = {"ecosystem": "pypi", "name": "dex-test-messages", "version": "2.3.4"}
    assert compact(binding) == compact(expected)
    run = run_command(tmp_path, "--trigger-binding", flag, python_path=tmp_path)
    assert run.returncode == 0, run.stderr
    assert json.loads(run.stdout)["v2"]["connectorTriggerBindings"] == [expected]


def test_the_command_reads_flags_and_a_bindings_file(tmp_path: Path) -> None:
    expected = render_session_flow_graph(
        FLOW_TYPE, ACTION_PERMISSION, connector_trigger_bindings=[resolve_case(0), resolve_case(1)]
    )
    first = dict(
        SUITE["cases"][0]["entry"], packageDirectory=os.path.relpath(PACKAGE_DIRECTORY, tmp_path)
    )
    second = dict(
        SUITE["cases"][1]["entry"], packageDirectory=os.path.relpath(PACKAGE_DIRECTORY, tmp_path)
    )
    bindings_file = tmp_path / "trigger-bindings.json"
    bindings_file.write_text(json.dumps([first, second]), encoding="utf-8")
    out = tmp_path / "graph.json"
    assert (
        main(
            [
                "--flow-type",
                FLOW_TYPE,
                "--permission",
                ACTION_PERMISSION,
                "--trigger-bindings",
                str(bindings_file),
                "--out",
                str(out),
            ]
        )
        == 0
    )
    assert out.read_text(encoding="utf-8") == expected

    unknown = run_command(
        tmp_path, "--trigger-binding", "workspace=dex-test-missing@1.0.0:replyCreated:replies"
    )
    assert unknown.returncode == 2
    assert "package dex-test-missing is not installed" in unknown.stderr
    malformed = run_command(tmp_path, "--trigger-binding", "workspace")
    assert malformed.returncode == 2
    assert (
        "must be <connectionName>=<packageName>@<packageVersion>:"
        "<triggerName>[#<factoryName>]:<bindingName>" in malformed.stderr
    )
    bindings_file.write_text(
        json.dumps([dict(first, triggerName="messageDeleted")]), encoding="utf-8"
    )
    unknown_trigger = run_command(tmp_path, "--trigger-bindings", str(bindings_file))
    assert unknown_trigger.returncode == 2
    assert "has no Trigger messageDeleted" in unknown_trigger.stderr
