# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The mock model API request log keeps no credential from headers, queries, or bodies."""

from __future__ import annotations

import json
import urllib.parse
from pathlib import Path

from dex_agents_cli_testing.mock_model_api import REDACTED, MockModelApi
from dex_agents_cli_testing.model_api import ModelApiRequest
from dex_agents_cli_testing.model_scenario import ScenarioFile

SECRET_MARKER = "SECRET_MARKER"


def test_request_log_redacts_nested_credentials(tmp_path: Path) -> None:
    request_log_path = tmp_path / "requests.jsonl"
    mock_model_api = MockModelApi(ScenarioFile(None), request_log_path)
    request = ModelApiRequest(
        method="POST",
        path=f"/v1/chat/completions?key={SECRET_MARKER}-query&alt=sse",
        headers={
            "authorization": f"Bearer {SECRET_MARKER}-authorization",
            "x-goog-api-key": f"{SECRET_MARKER}-goog",
            "cookie": f"session={SECRET_MARKER}-cookie",
            "content-type": "application/json",
        },
        body={
            "model": "mock-model",
            "messages": [{"role": "user", "content": "hello"}],
            "max_tokens": 64,
            "metadata": {
                "oauth": {
                    "client_secret": f"{SECRET_MARKER}-client-secret",
                    "clientSecret": f"{SECRET_MARKER}-camel-case",
                    "Refresh_Token": f"{SECRET_MARKER}-refresh",
                },
                "connections": [
                    {"name": "github", "github_token": f"{SECRET_MARKER}-github"},
                    {"name": "slack", "Webhook-Secret": f"{SECRET_MARKER}-webhook"},
                ],
            },
        },
    )

    mock_model_api.respond(request)

    log_text = request_log_path.read_text(encoding="utf-8")
    assert SECRET_MARKER not in log_text
    record = json.loads(log_text)
    assert record["credential_headers"] == ["authorization", "cookie", "x-goog-api-key"]
    assert record["headers"] == {"content-type": "application/json"}
    expected_query = urllib.parse.urlencode([("key", REDACTED), ("alt", "sse")])
    assert record["path"] == f"/v1/chat/completions?{expected_query}"
    assert record["body"]["metadata"] == {
        "oauth": {"client_secret": REDACTED, "clientSecret": REDACTED, "Refresh_Token": REDACTED},
        "connections": [
            {"name": "github", "github_token": REDACTED},
            {"name": "slack", "Webhook-Secret": REDACTED},
        ],
    }
    assert record["body"]["max_tokens"] == 64
    assert record["body"]["messages"] == [{"role": "user", "content": "hello"}]
