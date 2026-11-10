# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Each turn's configuration: the file's, narrowed by the session settings."""

from __future__ import annotations

import json
import logging
from collections.abc import Callable, Mapping
from contextvars import ContextVar
from typing import Any

from dex_agents import AgentSessionFlow, AgentTurnContext, AgentTurnOutcome
from dex_agents_cli.config import (
    ConfigError,
    EffectiveConfig,
    TurnConfigResolution,
    resolve_turn_config,
)

#: Prefix of the error a turn fails with when its session settings are rejected.
SESSION_SETTINGS_REJECTED_PREFIX = "session settings rejected"

_LOGGER = logging.getLogger(__name__)
_CURRENT_TURN_CONFIG: ContextVar[EffectiveConfig] = ContextVar("dex_agents_cli_turn_config")
# Optional runner members the session finds with isinstance checks; a wrapper must keep them.
_OPTIONAL_RUNNER_METHODS = (
    "build_unfinished_turn_messages",
    "tool_call_step_options",
    "poll_tool_task",
    "cancel_tool_task",
    "estimate_input_tokens",
    "select_compaction_cutoff",
    "summarize_context",
)
_RESOLUTION_CACHE_SIZE = 64


class SessionSettingsRejectedError(Exception):
    """Fail a turn whose session settings fail the schema or widen the file.

    Attributes:
        code: The configuration error code, such as ``widens_connectors``.
        path: JSON Pointer of the first problem.
    """

    def __init__(self, error: ConfigError) -> None:
        """Wrap the configuration error.

        Args:
            error: The rejection.
        """
        super().__init__(f"{SESSION_SETTINGS_REJECTED_PREFIX}: {error.code} at {error.path or '/'}")
        self.code = error.code
        self.path = error.path


class TurnConfigResolver:
    """Resolve each turn's configuration from the file's and the turn's session settings.

    Resolutions are cached by the settings' JSON text, so resolving the same
    settings again in a turn is cheap.
    """

    def __init__(self, file_config: EffectiveConfig) -> None:
        """Bound every turn by the file's configuration.

        Args:
            file_config: The file's effective configuration.
        """
        self._file_config = file_config
        self._cache: dict[str, TurnConfigResolution] = {}

    @property
    def file_config(self) -> EffectiveConfig:
        """Return the file's configuration, which bounds every turn."""
        return self._file_config

    def resolve(self, session_settings: Mapping[str, Any] | None) -> TurnConfigResolution:
        """Return the turn configuration for some session settings.

        Args:
            session_settings: The turn's settings; ``None`` or ``{}`` for none.

        Returns:
            The turn's configuration and its warnings.

        Raises:
            SessionSettingsRejectedError: If the settings fail the schema or widen the file.
        """
        settings = dict(session_settings) if session_settings else None
        key = json.dumps(settings, sort_keys=True)
        cached = self._cache.get(key)
        if cached is not None:
            return cached
        try:
            resolution = resolve_turn_config(self._file_config, settings)
        except ConfigError as error:
            raise SessionSettingsRejectedError(error) from error
        if len(self._cache) >= _RESOLUTION_CACHE_SIZE:
            self._cache.clear()
        self._cache[key] = resolution
        return resolution

    def resolve_turn(self, turn: AgentTurnContext) -> EffectiveConfig:
        """Return the configuration of the turn a ``RunModel`` Step runs.

        Args:
            turn: The model call's context.

        Returns:
            The turn's configuration.

        Raises:
            SessionSettingsRejectedError: If the turn's settings are rejected.
        """
        return self.resolve(turn.session_settings).config


def current_turn_config() -> EffectiveConfig | None:
    """Return the configuration of the turn whose model call is running in this task.

    The runtime sets it around every ``run_model_step``, and tasks that the
    runner starts inherit it, such as tool source listings. A harness reads it
    to choose the turn's model and instructions.

    Returns:
        The turn's configuration, or ``None`` outside a model call.
    """
    return _CURRENT_TURN_CONFIG.get(None)


def with_turn_config(flow: AgentSessionFlow, resolver: TurnConfigResolver) -> AgentSessionFlow:
    """Return the same agent session Flow, with each turn's configuration resolved first.

    Every ``run_model_step`` resolves the turn's session settings, which fails
    the model call with ``SessionSettingsRejectedError`` when they are
    rejected, and runs the harness's runner with ``current_turn_config`` set.

    Args:
        flow: The Flow a harness built.
        resolver: The resolver of the command's configuration file.

    Returns:
        A new Flow with the same type and options around the wrapped runner.
    """
    runner = _wrap_runner(flow.runner, resolver)
    return AgentSessionFlow(runner, flow_type=flow.get_flow_type(), options=flow.options)


class _TurnConfiguredRunner:
    def __init__(self, inner: Any, resolver: TurnConfigResolver) -> None:
        self._inner = inner
        self._resolver = resolver

    def build_user_message(self, text: str) -> Any:
        return self._inner.build_user_message(text)

    async def run_model_step(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        resolution = self._resolver.resolve(turn.session_settings)
        if resolution.warnings and turn.attempt == 1:
            _LOGGER.warning(
                "session %s ignores settings keys %s", turn.flow_id, ", ".join(resolution.warnings)
            )
        token = _CURRENT_TURN_CONFIG.set(resolution.config)
        try:
            outcome: AgentTurnOutcome = await self._inner.run_model_step(turn)
        finally:
            _CURRENT_TURN_CONFIG.reset(token)
        return outcome

    async def execute_deferred_tool_call(self, call: Any) -> Any:
        return await self._inner.execute_deferred_tool_call(call)


def _wrap_runner(inner: Any, resolver: TurnConfigResolver) -> Any:
    """Build a wrapper class with exactly the optional members the inner runner has."""
    namespace: dict[str, Any] = {
        name: _forwarding_method(name)
        for name in _OPTIONAL_RUNNER_METHODS
        if callable(getattr(inner, name, None))
    }
    if hasattr(inner, "supports_plan_mode"):
        namespace["supports_plan_mode"] = property(lambda self: self._inner.supports_plan_mode)
    wrapper_type = type("TurnConfiguredRunner", (_TurnConfiguredRunner,), namespace)
    return wrapper_type(inner, resolver)


def _forwarding_method(name: str) -> Callable[..., Any]:
    def forward(self: _TurnConfiguredRunner, *args: Any, **kwargs: Any) -> Any:
        return getattr(self._inner, name)(*args, **kwargs)

    forward.__name__ = name
    return forward
