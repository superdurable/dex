# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Each turn's configuration: the file's, narrowed by the session settings."""

from __future__ import annotations

import dataclasses
import json
from collections.abc import Callable, Mapping, Sequence
from contextvars import ContextVar
from dataclasses import dataclass
from typing import Any, cast

from dex_agents import (
    TURN_SESSION_SETTINGS,
    AgentSessionFlow,
    AgentTurnContext,
    AgentTurnOutcome,
    DeferredToolExecution,
)
from dex_agents_cli_core.config import EffectiveConfig, resolve_turn_config

_CURRENT_TURN: ContextVar[ResolvedTurn] = ContextVar("dex_agents_cli_turn")
# Optional runner members the session finds with isinstance checks; a wrapper must keep them.
_OPTIONAL_RUNNER_METHODS = (
    "build_unfinished_turn_messages",
    "build_user_message_with_images",
    "tool_call_step_options",
    "poll_tool_task",
    "cancel_tool_task",
    "estimate_input_tokens",
    "select_compaction_cutoff",
    "summarize_context",
)
_RESOLUTION_CACHE_SIZE = 64


@dataclass(frozen=True)
class ResolvedTurn(EffectiveConfig):
    """One turn's configuration, narrowed by its session settings, with its instructions.

    Attributes:
        instructions: ``system_prompt``, else the text of ``system_prompt_file``,
            else ``None`` for the harness's own default instructions.
        reasoning_effort: The turn's reasoning effort from the session
            settings, one of the harness's ``supported_reasoning_efforts``, or
            ``None`` when the settings set none. With ``None``, pass no
            reasoning effort to the SDK, so the model's own default applies.
            ``to_json`` leaves it out, as it does ``instructions``.

            .. versionadded:: 0.7.0
    """

    instructions: str | None = None
    reasoning_effort: str | None = None


class TurnConfigResolver:
    """Resolve each turn's configuration from the file's and the turn's session settings.

    Resolutions are cached by the settings' JSON text, so the ignored keys of
    one set of settings are reported once.
    """

    def __init__(
        self,
        file_config: EffectiveConfig,
        file_instructions: str | None,
        report_warnings: Callable[[Sequence[str]], None],
        *,
        supported_reasoning_efforts: Sequence[str] = (),
    ) -> None:
        """Bound every turn by the file's configuration.

        .. versionchanged:: 0.7.0
           The ``supported_reasoning_efforts`` argument.

        Args:
            file_config: The file's effective configuration.
            file_instructions: The file's instructions: ``system_prompt``, else
                the text of ``system_prompt_file``, else ``None``.
            report_warnings: Called once per distinct settings with the JSON
                Pointers of their ignored keys.
            supported_reasoning_efforts: The ``reasoning_effort`` values the
                harness command declares. Empty, the default, rejects every
                ``reasoning_effort`` setting with ``unsupported_reasoning_effort``.
        """
        self._file_config = file_config
        self._file_instructions = file_instructions
        self._report_warnings = report_warnings
        self._supported_reasoning_efforts = tuple(supported_reasoning_efforts)
        self._cache: dict[str, ResolvedTurn] = {}

    @property
    def file_config(self) -> EffectiveConfig:
        """Return the file's configuration, which bounds every turn."""
        return self._file_config

    @property
    def file_instructions(self) -> str | None:
        """Return the file's instructions."""
        return self._file_instructions

    def resolve(self, session_settings: Mapping[str, Any] | None) -> ResolvedTurn:
        """Return the turn configuration for some session settings.

        Args:
            session_settings: The turn's settings; ``None`` or ``{}`` for none.

        Returns:
            The turn's configuration and instructions.

        Raises:
            ConfigError: If the settings fail the schema, widen the file, or
                set a ``reasoning_effort`` the harness does not declare. Its
                message is ``<code> at "<pointer>"``.
        """
        settings = dict(session_settings) if session_settings else None
        key = json.dumps(settings, sort_keys=True)
        cached = self._cache.get(key)
        if cached is not None:
            return cached
        resolution = resolve_turn_config(
            self._file_config,
            settings,
            supported_reasoning_efforts=self._supported_reasoning_efforts,
        )
        if resolution.warnings:
            self._report_warnings(resolution.warnings)
        turn = resolution.config
        instructions = turn.system_prompt
        if instructions is None and turn.system_prompt_file is not None:
            instructions = self._file_instructions
        resolved = ResolvedTurn(
            **{field.name: getattr(turn, field.name) for field in dataclasses.fields(turn)},
            instructions=instructions,
            reasoning_effort=resolution.reasoning_effort,
        )
        if len(self._cache) >= _RESOLUTION_CACHE_SIZE:
            self._cache.clear()
        self._cache[key] = resolved
        return resolved

    def resolve_turn(self, turn: AgentTurnContext) -> ResolvedTurn:
        """Return the configuration of the turn a ``RunModel`` Step runs.

        Args:
            turn: The model call's context.

        Returns:
            The turn's configuration.

        Raises:
            ConfigError: If the turn's settings are rejected.
        """
        return self.resolve(turn.session_settings)


def current_turn_config() -> ResolvedTurn | None:
    """Return the configuration of the turn whose model call or tool Step is running in this task.

    The CLI core sets it around every ``run_model_step``, from the turn's
    session settings, and around every ``execute_deferred_tool_call``, from
    the settings the session recorded for the call's turn. Tasks that these
    start inherit it, such as tool source listings.

    Returns:
        The turn's configuration, or ``None`` outside a model call and a tool Step.
    """
    return _CURRENT_TURN.get(None)


def with_turn_config(flow: AgentSessionFlow, resolver: TurnConfigResolver) -> AgentSessionFlow:
    """Return the same agent session Flow, with each turn's configuration resolved first.

    Every ``run_model_step`` resolves the turn's session settings, which fails
    the model call with ``ConfigError`` when they are rejected, and runs the
    harness's runner with ``current_turn_config`` set.

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
        token = _CURRENT_TURN.set(self._resolver.resolve_turn(turn))
        try:
            outcome: AgentTurnOutcome = await self._inner.run_model_step(turn)
        finally:
            _CURRENT_TURN.reset(token)
        return outcome

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> Any:
        # A tool Step runs with its turn's configuration too, so replacement tools see it.
        token = _CURRENT_TURN.set(self._resolver.resolve(recorded_turn_settings(call)))
        try:
            return await self._inner.execute_deferred_tool_call(call)
        finally:
            _CURRENT_TURN.reset(token)


def recorded_turn_settings(call: DeferredToolExecution) -> Mapping[str, Any]:
    """Return the session settings a tool call's turn started with, as the session recorded them.

    Args:
        call: The tool Step's call.

    Returns:
        The turn's settings; ``{}`` when the turn started without settings or
        the Step has no context.
    """
    if call.context is None:
        return {}
    stored = cast(Any, TURN_SESSION_SETTINGS).get(call.context)
    if stored is None or stored.turn_id != call.tool_call.turn_id:
        return {}
    return cast(Mapping[str, Any], stored.session_settings)


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
