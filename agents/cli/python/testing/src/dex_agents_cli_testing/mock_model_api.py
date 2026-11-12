# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""One local mock of every model API the agent commands call: ``dex-agents-mock-model-api``.

It serves, on one port of 127.0.0.1:

* the OpenAI Responses API, ``POST /v1/responses``, and Chat Completions,
  ``POST /v1/chat/completions``: set ``OPENAI_BASE_URL=<url>/v1``;
* the Anthropic Messages API, ``POST /v1/messages`` and
  ``/v1/messages/count_tokens``: set ``ANTHROPIC_BASE_URL=<url>``, or
  ``pi.provider_base_urls.anthropic`` for Pi;
* the Gemini API, ``POST /v1beta/models/<model>:generateContent`` and
  ``:streamGenerateContent``: set ``GOOGLE_GEMINI_BASE_URL=<url>``.

Replies come from a scenario file (see ``model_scenario``) that reloads when
it changes, and fall back to the ``say`` / ``call`` / ``recall`` script. Any
model name is accepted. Every request is appended to a JSONL log, with
credential headers reduced to their names, query values redacted, and the
values of credential-like body keys redacted at any depth.
"""

from __future__ import annotations

import argparse
import json
import re
import signal
import threading
import time
import urllib.parse
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from dex_agents_cli_testing.chat_completions_api import ChatCompletionsApiStub
from dex_agents_cli_testing.gemini_api import GeminiApiStub
from dex_agents_cli_testing.messages_api import MessagesApiStub
from dex_agents_cli_testing.model_api import ModelApiRequest, ModelApiResponse, ModelApiStub
from dex_agents_cli_testing.model_scenario import ScenarioFile, ScenarioReply
from dex_agents_cli_testing.responses_api import ResponsesApiStub
from dex_agents_cli_testing.scripted_model import ScriptedConversation, ScriptedReply

READY_LINE_PREFIX = "Mock model API listening on "
CREDENTIAL_NAMES = frozenset(
    (
        "api_key",
        "apikey",
        "key",
        "token",
        "access_token",
        "refresh_token",
        "id_token",
        "secret",
        "client_secret",
        "password",
        "authorization",
        "proxy_authorization",
        "cookie",
        "set_cookie",
    )
)
CREDENTIAL_NAME_SUFFIXES = ("_token", "_secret", "_key", "_password")
_CAMEL_CASE_BOUNDARY = re.compile(r"(?<=[a-z0-9])(?=[A-Z])")
REDACTED = "<redacted>"
_LOGGED_QUERY_PARAMETERS = frozenset(("alt", "beta"))
_LOGGED_OUTPUT_CHARACTERS = 4000


class MockModelApi(ModelApiStub):
    """Serve every provider API from one scenario, and log each request."""

    def __init__(self, scenario: ScenarioFile, request_log_path: Path | None) -> None:
        """Create the server without starting it."""
        super().__init__()
        self._scenario = scenario
        self._request_log_path = request_log_path
        self._log_lock = threading.Lock()
        self._choice = threading.local()
        self._apis: dict[str, ModelApiStub] = {
            "responses": ResponsesApiStub(
                self._choose_reply,
                None
                if request_log_path is None
                else request_log_path.with_name(request_log_path.name + ".responses-items"),
            ),
            "chat_completions": ChatCompletionsApiStub(self._choose_reply),
            "messages": MessagesApiStub(self._choose_reply),
            "gemini": GeminiApiStub(self._choose_reply),
        }

    def is_model_request(self, request: ModelApiRequest) -> bool:
        """Report whether any provider API takes the request as a model call."""
        return any(api.is_model_request(request) for api in self._apis.values())

    def respond(self, request: ModelApiRequest) -> ModelApiResponse:
        """Route a request to its provider API and log it."""
        self._choice.value = None
        api_name = self._api_name(request)
        error: str | None = None
        try:
            if api_name is None:
                response = self._answer_other(request)
            else:
                response = self._apis[api_name].respond(request)
        except Exception as exception:  # A bad request must not kill the server.
            error = f"{type(exception).__name__}: {exception}"
            response = ModelApiResponse(500, body={"error": {"message": error}})
        self._log(request, api_name, response.status, error)
        return response

    def _api_name(self, request: ModelApiRequest) -> str | None:
        path = request.path.split("?", 1)[0]
        if request.method == "POST" and path.endswith("/messages/count_tokens"):
            return "messages"
        for name, api in self._apis.items():
            if api.is_model_request(request):
                return name
        return None

    def _answer_other(self, request: ModelApiRequest) -> ModelApiResponse:
        path = request.path.split("?", 1)[0]
        if request.method == "GET" and path.endswith("/models"):
            return ModelApiResponse(body={"object": "list", "data": []})
        return ModelApiResponse(404, body={"error": {"message": f"no mock for {path}"}})

    def _choose_reply(self, conversation: ScriptedConversation) -> ScriptedReply:
        choice = self._scenario.reply(conversation)
        self._choice.value = (conversation, choice)
        return choice.reply

    def _log(
        self, request: ModelApiRequest, api_name: str | None, status: int, error: str | None
    ) -> None:
        if self._request_log_path is None:
            return
        record: dict[str, Any] = {
            "time": time.time(),
            "api": api_name,
            "method": request.method,
            "path": _redacted_path(request.path),
            "status": status,
            "credential_headers": sorted(
                name for name in request.headers if is_credential_name(name)
            ),
            "headers": {
                name: value
                for name, value in request.headers.items()
                if not is_credential_name(name)
            },
        }
        if error is not None:
            record["error"] = error
        choice: tuple[ScriptedConversation, ScenarioReply] | None = getattr(
            self._choice, "value", None
        )
        if choice is not None:
            record.update(_conversation_record(*choice))
        record["body"] = _redacted_value(request.body)
        with self._log_lock, self._request_log_path.open("a", encoding="utf-8") as log:
            log.write(json.dumps(record) + "\n")


def is_credential_name(name: str) -> bool:
    """Report whether a header, query, or body key name looks like it holds a credential.

    The check ignores case and treats ``-`` and camelCase boundaries as ``_``, so
    ``X-Api-Key``, ``clientSecret``, and ``client_secret`` all match.
    """
    words = _CAMEL_CASE_BOUNDARY.sub("_", name).lower().replace("-", "_")
    return words in CREDENTIAL_NAMES or words.endswith(CREDENTIAL_NAME_SUFFIXES)


def _redacted_path(path: str) -> str:
    """Return the path with every query value except ``alt`` replaced, as ``key=`` can be a key."""
    route, separator, query = path.partition("?")
    if not separator:
        return route
    parameters = urllib.parse.parse_qsl(query, keep_blank_values=True)
    redacted = [
        (name, value if name in _LOGGED_QUERY_PARAMETERS else REDACTED)
        for name, value in parameters
    ]
    return f"{route}?{urllib.parse.urlencode(redacted)}"


def _redacted_value(value: object) -> object:
    """Return a JSON value with the values of credential-like keys replaced."""
    if isinstance(value, Mapping):
        return {
            key: REDACTED if is_credential_name(str(key)) else _redacted_value(item)
            for key, item in value.items()
        }
    if isinstance(value, list):
        return [_redacted_value(item) for item in value]
    return value


def _conversation_record(
    conversation: ScriptedConversation, choice: ScenarioReply
) -> Mapping[str, Any]:
    return {
        "model": conversation.model,
        "prompt": conversation.prompts[-1] if conversation.prompts else None,
        "reply_count": conversation.reply_count,
        "tool_names": list(conversation.tool_names),
        "tool_results": [
            {"tool_name": result.tool_name, "output": result.output[:_LOGGED_OUTPUT_CHARACTERS]}
            for result in conversation.tool_results
        ],
        "scenario_turn": choice.turn_label,
        "scenario_step": choice.step_index,
        "reply": {
            "text": choice.reply.text,
            "tool_calls": [
                {"name": call.tool_name, "arguments": call.arguments}
                for call in choice.reply.tool_calls
            ],
        },
    }


def main(argv: list[str] | None = None) -> None:
    """Run the mock model API until SIGINT or SIGTERM."""
    parser = argparse.ArgumentParser(
        prog="dex-agents-mock-model-api",
        description="Serve the OpenAI, Anthropic, and Gemini model APIs from a scenario file.",
    )
    parser.add_argument("--port", type=int, default=0, help="port on 127.0.0.1; 0 picks one")
    parser.add_argument("--scenario", type=Path, help="JSON or YAML scenario file")
    parser.add_argument("--request-log", type=Path, help="JSONL file that gets every request")
    arguments = parser.parse_args(argv)
    server = MockModelApi(ScenarioFile(arguments.scenario), arguments.request_log)
    server.start(arguments.port)
    stop_requested = threading.Event()
    for signal_number in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signal_number, lambda *_: stop_requested.set())
    print(f"{READY_LINE_PREFIX}{server.base_url}", flush=True)
    print(
        f"  OPENAI_BASE_URL={server.base_url}/v1 ANTHROPIC_BASE_URL={server.base_url} "
        f"GOOGLE_GEMINI_BASE_URL={server.base_url}",
        flush=True,
    )
    stop_requested.wait()
    server.stop()


if __name__ == "__main__":
    main()
