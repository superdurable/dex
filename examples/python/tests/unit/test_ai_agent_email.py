# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Tests the OpenAI and SMTP boundaries used by the email agent example."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from types import SimpleNamespace
from typing import Any

import pytest

from dex_examples.products.ai_agent_email import ai_agent_flow
from dex_examples.products.ai_agent_email import agent


async def test_request_email_fields_uses_aggregated_output_text(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv(agent.OPENAI_KEY_VARIABLE, "test-key")

    async def create_response(
        request: str,
        previous_response_id: str | None,
        write_progress: Callable[[str], Awaitable[None]],
    ) -> Any:
        return SimpleNamespace(
            id="response-123",
            output=[SimpleNamespace()],
            output_text=(
                '{"email_recipient":"demo@example.com",'
                '"email_subject":"Hello","email_body":"Body",'
                '"email_send_time_unix_seconds":123,"cancel_operation":false}'
            ),
        )

    monkeypatch.setattr(agent, "_create_response", create_response)

    reply = await agent.request_email_fields("Send an email", None, _write_progress)

    assert reply.response_id == "response-123"
    assert reply.response.email_recipient == "demo@example.com"
    assert reply.response.email_subject == "Hello"


async def test_request_email_fields_falls_back_when_output_cannot_be_parsed(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv(agent.OPENAI_KEY_VARIABLE, "test-key")

    async def create_response(
        request: str,
        previous_response_id: str | None,
        write_progress: Callable[[str], Awaitable[None]],
    ) -> Any:
        return SimpleNamespace(id="response-123", output_text="not valid JSON")

    monkeypatch.setattr(agent, "_create_response", create_response)

    reply = await agent.request_email_fields("Send an email", None, _write_progress)

    assert reply.response_id is None
    assert reply.response.email_recipient == agent.LOCAL_DRAFT_RECIPIENT
    assert reply.response.email_body == "Send an email"


def test_send_email_supports_unicode_subject_and_body(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    smtp_server = _FakeSMTP()

    def create_smtp_server(host: str, port: int) -> _FakeSMTP:
        assert host == "smtp.gmail.com"
        assert port == 465
        return smtp_server

    monkeypatch.setattr(ai_agent_flow.smtplib, "SMTP_SSL", create_smtp_server)

    ai_agent_flow._send_email(
        "sender@example.com",
        "app-password",
        "recipient@example.com",
        "中文主题",
        "这是一封中文正文。",
    )

    message = smtp_server.message
    assert message is not None
    assert message["Subject"] == "中文主题"
    assert message.get_content().strip() == "这是一封中文正文。"
    assert message.as_bytes()
    assert smtp_server.from_addr == "sender@example.com"
    assert smtp_server.to_addrs == ["recipient@example.com"]
    assert smtp_server.has_quit is True


async def _write_progress(progress: str) -> None:
    pass


class _FakeSMTP:
    def __init__(self) -> None:
        self.message: Any | None = None
        self.from_addr: str | None = None
        self.to_addrs: list[str] | None = None
        self.has_quit = False

    def ehlo(self) -> None:
        pass

    def login(self, sender: str, app_password: str) -> None:
        assert sender == "sender@example.com"
        assert app_password == "app-password"

    def send_message(
        self,
        message: Any,
        from_addr: str,
        to_addrs: list[str],
    ) -> None:
        self.message = message
        self.from_addr = from_addr
        self.to_addrs = to_addrs

    def quit(self) -> None:
        self.has_quit = True
