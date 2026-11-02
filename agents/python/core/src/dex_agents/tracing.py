# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""OpenTelemetry GenAI tracing options for agent sessions.

The session emits spans through the OpenTelemetry API, which the optional
``dex-agents[otel]`` extra installs. The application brings the OpenTelemetry
SDK and exporter. Without the API, or with tracing disabled, the session never
imports OpenTelemetry and emits nothing.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from opentelemetry.trace import TracerProvider


class GenAISemanticConventionsVersion(StrEnum):
    """Name the OpenTelemetry GenAI semantic conventions version the spans follow.

    The GenAI conventions are still marked Development, so attribute names can
    change between versions. The session pins one version and sets its schema
    URL on the tracer. Pick an older version when a tracing backend still reads
    the older names.

    Attributes:
        V1_42_0_DEV: The conventions in ``open-telemetry/semantic-conventions-genai``
            at commit ``dc3571562603552dc555c667dfa327a638d94ab2``
            (2026-11-01), whose schema URL is
            ``https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev``. That
            repository has no release tag yet. The default. Chat spans carry
            ``gen_ai.provider.name`` and the cache and reasoning token counts,
            and content capture is available.
        V1_36_0: OpenTelemetry semantic conventions 1.36.0, schema URL
            ``https://opentelemetry.io/schemas/1.36.0``, the last release before
            ``gen_ai.system`` became ``gen_ai.provider.name``. Chat spans carry
            ``gen_ai.system`` and only the input and output token counts. Content
            capture is not available, because 1.36.0 recorded content as events.
    """

    V1_42_0_DEV = "1.42.0-dev"
    V1_36_0 = "1.36.0"

    @property
    def schema_url(self) -> str:
        """Return the OpenTelemetry schema URL of this version.

        Returns:
            The schema URL the session sets on its tracer.
        """
        if self is GenAISemanticConventionsVersion.V1_36_0:
            return "https://opentelemetry.io/schemas/1.36.0"
        return "https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev"


@dataclass(frozen=True, kw_only=True)
class AgentTracingOptions:
    """Configure the OpenTelemetry GenAI spans of one agent session Flow.

    Pass it as ``AgentSessionOptions.tracing``. Tracing is on by default and
    uses the global tracer provider, so an application that configures the
    OpenTelemetry SDK gets spans without further setup. When the OpenTelemetry
    API is not installed, or no SDK is configured, the session emits nothing
    and stores nothing extra.

    The session emits one trace per activation: a burst of Steps that starts
    when the session consumes a user message or an interrupt answer, and ends
    when the session waits again. The activation's root span is
    ``invoke_agent {agent name}``. Each ``RunModel`` attempt adds a ``chat``
    span, named ``chat {model}`` when the runner returns ``ModelCallMetadata``,
    and each tool Step attempt adds an ``execute_tool {tool name}`` span. Every
    span carries the Flow ID as ``gen_ai.conversation.id`` and ``session.id``.
    Tracing never fails a Step: an error from OpenTelemetry, such as a
    raising span processor, is logged on the ``dex_agents.tracing`` logger and
    the span is skipped. The package README lists every span and attribute.

    Example:
        >>> from opentelemetry.sdk.trace import TracerProvider
        >>> provider = TracerProvider()
        >>> options = AgentSessionOptions(
        ...     tracing=AgentTracingOptions(
        ...         tracer_provider=provider,
        ...         agent_name="Triage",
        ...         should_capture_message_content=True,
        ...     ),
        ... )

    Attributes:
        is_enabled: Whether the session emits spans. Defaults to ``True``.
            ``False`` never imports OpenTelemetry.
        tracer_provider: The OpenTelemetry ``TracerProvider`` to emit with.
            ``None``, the default, uses the global provider at the first Step
            that emits a span.
        agent_name: ``gen_ai.agent.name`` and the ``invoke_agent`` span name.
            ``None``, the default, uses the Flow type.
        should_capture_message_content: Whether spans carry message text, tool
            arguments, and tool results. Defaults to ``False``: spans then
            carry only Dex references, such as ``AgentMessages`` instance keys
            and tool call IDs, because the content is already durable in Dex.
            Content can be sensitive and large; enable it only where the
            tracing backend may store it.
        semantic_conventions_version: The GenAI semantic conventions version.
            Defaults to ``GenAISemanticConventionsVersion.V1_42_0_DEV``.

    Raises:
        ValueError: If ``agent_name`` is empty or whitespace-only.
    """

    is_enabled: bool = True
    tracer_provider: TracerProvider | None = None
    agent_name: str | None = None
    should_capture_message_content: bool = False
    semantic_conventions_version: GenAISemanticConventionsVersion = (
        GenAISemanticConventionsVersion.V1_42_0_DEV
    )

    def __post_init__(self) -> None:
        if self.agent_name is not None and not self.agent_name.strip():
            raise ValueError("agent_name must not be empty")
