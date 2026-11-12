# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A fake ``dexcli``: the terminal UI contract host's ``tui`` role, replaying one transcript.

A test writes a transcript, points ``DEXCLI`` at the wrapper this module
writes, and reads the host's record (message summaries, never values) to
time its fault injection, such as stopping the Worker host mid-request.
"""

from __future__ import annotations

import asyncio
import copy
import json
import os
import signal
import sys
import time
import uuid
from collections.abc import Callable, Iterable, Mapping
from pathlib import Path
from typing import Any

from dex_agents_cli_core.terminal_ui.contract import candidate_session_ids

AGENTS_DIR = Path(__file__).resolve().parents[4]
CONTRACT_HOST = AGENTS_DIR / "cli" / "tests" / "terminal_ui_contract_host.py"
TRANSCRIPTS_DIR = AGENTS_DIR / "conformance" / "terminal_ui" / "transcripts"
#: The harness name the conformance transcripts use.
FIXTURE_HARNESS = "openai-sdk"
#: The ``ignore`` list of a launch step: what a real launcher fills in itself.
LAUNCH_IGNORE = [
    "command.name",
    "command.harness",
    "command.language",
    "command.version",
    "dex.server_address",
    "dex.web_url",
    "session.flow_type",
    "session.session_id",
    "display",
    "log_path",
]


def load_transcript(name: str) -> dict[str, Any]:
    """Return a conformance transcript, as a copy a test may change."""
    loaded: dict[str, Any] = json.loads((TRANSCRIPTS_DIR / f"{name}.json").read_text())
    return loaded


def chat_transcript(steps: Iterable[Mapping[str, Any]], tui_exit: int | str = 0) -> dict[str, Any]:
    """Return a chat transcript: the conformance launch and ``ready``, then ``steps``."""
    base = load_transcript("chat_ready_then_exit")
    return {**base, "name": "test", "steps": [*base["steps"], *steps], "tui_exit": tui_exit}


def new_key() -> str:
    """Return a fresh lowercase UUIDv7, so each test starts its own sessions."""
    time_ms = time.time_ns() // 1_000_000
    random_bits = uuid.uuid4().int
    value = (time_ms << 80) | (0x7 << 76) | ((random_bits >> 64) & 0xFFF) << 64
    value |= (0b10 << 62) | (random_bits & ((1 << 62) - 1))
    return str(uuid.UUID(int=value))


def with_fresh_keys(transcript: Mapping[str, Any], harness: str) -> dict[str, Any]:
    """Give every key a fresh one, and every session ID derived from a key this harness's ID.

    A real launcher derives session IDs from its own harness name, and the
    Dex server keeps start request IDs, so tests never share keys.
    """
    replacements: dict[str, str] = {}
    for step in transcript["steps"]:
        message = step.get("message", {})
        keys = [message.get("idempotency_key"), message.get("params", {}).get("idempotency_key")]
        for key in keys:
            if isinstance(key, str) and key not in replacements:
                fresh = new_key()
                replacements[key] = fresh
                pairs = zip(
                    candidate_session_ids(FIXTURE_HARNESS, key),
                    candidate_session_ids(harness, fresh),
                    strict=True,
                )
                replacements.update(pairs)
    replaced: dict[str, Any] = replace_strings(transcript, replacements)
    return replaced


def replace_strings(value: Any, replacements: Mapping[str, str]) -> Any:
    """Return a copy of a JSON value with whole strings replaced."""
    if isinstance(value, str):
        return replacements.get(value, value)
    if isinstance(value, list):
        return [replace_strings(item, replacements) for item in value]
    if isinstance(value, dict):
        return {key: replace_strings(item, replacements) for key, item in value.items()}
    return copy.copy(value)


def insert_pause(
    transcript: dict[str, Any], after: Callable[[Mapping[str, Any]], bool], pause_ms: int
) -> dict[str, Any]:
    """Return the transcript with a terminal UI pause after the first step ``after`` matches."""
    steps = list(transcript["steps"])
    index = next(index for index, step in enumerate(steps) if after(step))
    steps.insert(index + 1, {"pause_ms": pause_ms, "role": "tui"})
    return {**transcript, "steps": steps}


def is_ready_step(step: Mapping[str, Any]) -> bool:
    """Return whether a step is the terminal UI's ``ready``."""
    return bool(step.get("message", {}).get("type") == "ready")


class FakeTerminalUi:
    """The wrapper ``DEXCLI`` a launcher runs, its transcript, and its record."""

    def __init__(
        self,
        directory: Path,
        transcript: Mapping[str, Any] | None = None,
        *,
        describe: Mapping[str, Any] | None = None,
        step_timeout_seconds: float = 30.0,
    ) -> None:
        """Write the wrapper, the transcript, and an optional ``--describe`` document."""
        directory.mkdir(parents=True, exist_ok=True)
        self.directory = directory
        self.record_path = directory / "record.jsonl"
        self.transcript_path = directory / "transcript.json"
        self.transcript_path.write_text(json.dumps(transcript or chat_transcript([])))
        arguments = [
            sys.executable,
            str(CONTRACT_HOST),
            "tui",
            "--transcript",
            str(self.transcript_path),
            "--record",
            str(self.record_path),
            "--step-timeout",
            str(step_timeout_seconds),
        ]
        if describe is not None:
            describe_path = directory / "describe.json"
            describe_path.write_text(json.dumps(describe))
            arguments += ["--describe-file", str(describe_path)]
        self.dexcli = directory / "dexcli"
        self.invocations_path = directory / "invocations.txt"
        quoted = " ".join(f"'{argument}'" for argument in arguments)
        self.dexcli.write_text(
            f'#!/bin/sh\necho "$*" >> \'{self.invocations_path}\'\nexec {quoted} "$@"\n'
        )
        self.dexcli.chmod(0o755)

    def environment(self) -> dict[str, str]:
        """Return the CLI variables that turn the terminal UI on with this fake ``dexcli``."""
        return {
            "DEXCLI": str(self.dexcli),
            "DEX_AGENTS_TERMINAL_UI": "1",
            "TERM": "xterm-256color",
        }

    def events(self) -> list[dict[str, Any]]:
        """Return the record's events so far."""
        if not self.record_path.exists():
            return []
        return [json.loads(line) for line in self.record_path.read_text().splitlines() if line]

    def invocations(self) -> list[str]:
        """Return the argument lists the wrapper was run with, one line each."""
        if not self.invocations_path.exists():
            return []
        return self.invocations_path.read_text().splitlines()

    def mismatches(self) -> list[dict[str, Any]]:
        """Return the host's mismatch events: where the launcher left the transcript."""
        return [event for event in self.events() if event["event"] == "mismatch"]

    def was_run(self) -> bool:
        """Return whether a launcher ran the terminal UI side, not only ``--describe``."""
        return any(event["event"] == "start" for event in self.events())

    async def wait_for(self, event: str, timeout: float = 60.0, **summary: Any) -> dict[str, Any]:
        """Wait for a record event whose message summary has these fields."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for recorded in self.events():
                message = recorded.get("message", {})
                if recorded["event"] == event and all(
                    message.get(name) == value for name, value in summary.items()
                ):
                    return recorded
            await asyncio.sleep(0.05)
        raise AssertionError(f"no {event} {summary} in {self.events()}")


def worker_host_pid(agents_directory: Path, harness: str = "scripted") -> int:
    """Return the pid of the one Worker host that has a run record."""
    (record,) = (agents_directory / "run").glob(f"{harness}-*.json")
    return int(record.stem.rsplit("-", 1)[1])


def run_record(agents_directory: Path, pid: int, harness: str = "scripted") -> dict[str, Any]:
    """Return a Worker host's run record."""
    loaded: dict[str, Any] = json.loads(
        (agents_directory / "run" / f"{harness}-{pid}.json").read_text()
    )
    return loaded


def is_group_alive(pgid: int) -> bool:
    """Return whether a process group still has a process."""
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


async def wait_until_gone(pgids: Iterable[int], timeout: float) -> float:
    """Wait until every process group is gone; return how long it took."""
    started = time.monotonic()
    remaining = list(pgids)
    while remaining:
        remaining = [pgid for pgid in remaining if is_group_alive(pgid)]
        if time.monotonic() - started > timeout:
            raise AssertionError(f"process groups {remaining} still run after {timeout} s")
        await asyncio.sleep(0.1)
    return time.monotonic() - started


def stop_process(pid: int, signal_number: int = signal.SIGSTOP) -> None:
    """Send a signal to one process."""
    os.kill(pid, signal_number)
