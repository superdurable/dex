#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Test-only peer for the Dex Agents terminal UI contract v1.

It plays one side of a channel transcript from ``agents/conformance/terminal_ui/transcripts``
and checks that the other side sends what the transcript says:

- ``launcher`` role: stands in for an agents CLI launcher. It spawns a terminal UI
  command (such as ``dexcli agents-tui``) with a channel socket, optionally on a
  pseudo-terminal, then replays the launcher side.
- ``tui`` role: stands in for ``dexcli agents-tui``. Point ``DEXCLI`` at a wrapper
  script that runs this file with ``tui --transcript ... agents-tui "$@"``. It answers
  ``--describe`` and, with ``--channel-fd N``, replays the terminal UI side and
  records what it received.

It needs Python 3.11 or later and only the standard library, so Go, TypeScript, and
Python tests can all run it. The rules in ``agents/cli/TERMINAL_UI_CONTRACT.md`` are
implemented here once, as reference functions that the conformance tests also check.
"""

from __future__ import annotations

import argparse
import contextlib
import fcntl
import hashlib
import json
import os
import pty
import re
import signal
import socket
import struct
import subprocess
import sys
import termios
import threading
import time
from collections.abc import Iterator, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, BinaryIO, Final
from urllib.parse import SplitResult, urlsplit

SUPPORTED_CONTRACTS: Final = (1,)
MAX_LINE_BYTES: Final = 1_048_576
READY_TIMEOUT_SECONDS: Final = 5.0
MAX_IN_FLIGHT_REQUESTS: Final = 4
NOTICE_MAX_CODE_POINTS: Final = 500
NOTICE_REPEAT_WINDOW_MS: Final = 10_000
TERMINAL_ENVIRONMENT_NAMES: Final = (
    "HOME",
    "TERM",
    "COLORTERM",
    "TERM_PROGRAM",
    "NO_COLOR",
    "LANG",
    "LC_ALL",
    "LC_CTYPE",
    "TZ",
    "DEX_AGENTS_TUI_DEBUG",
)
AUTH_ENVIRONMENT_NAMES: Final = ("DEX_CREDENTIAL", "DEX_CREDENTIAL_FILE", "DEX_API_KEY")
ALLOWED_ENVIRONMENT_NAMES: Final = TERMINAL_ENVIRONMENT_NAMES + AUTH_ENVIRONMENT_NAMES
# Keys carry a UUIDv7 time between 2000-01-01T00:00:00Z and 9999-12-31T23:59:59.999Z.
MIN_KEY_TIME_MS: Final = 946_684_800_000
MAX_KEY_TIME_MS: Final = 253_402_300_799_999
DEFAULT_DESCRIBE: Final[dict[str, Any]] = {
    "dexcli_version": "0.0.0-contract-host",
    "contracts": {
        "1": {
            "modes": {
                "chat": {
                    "uses_methods": [
                        "put_image",
                        "start_session",
                        "list_connectors",
                        "open_url",
                        "get_request_result",
                    ]
                },
                "sessions": {"uses_methods": []},
                "connectors": {"uses_methods": ["list_connectors", "open_url"]},
            },
            "capabilities": ["images", "plan_mode", "turn_control"],
        }
    },
}

HOST_FAILURE_EXIT_CODE: Final = 70
_UUID_V7 = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}")
_DEFAULT_PORTS: Final = {"http": 80, "https": 443}


# Reference rules of the contract.


class IdempotencyKeyError(ValueError):
    """The idempotency key is not a canonical lowercase UUIDv7 with a time in range."""


def idempotency_key_time_ms(idempotency_key: str) -> int:
    """Return the Unix time in milliseconds that a valid key carries.

    Raises IdempotencyKeyError when the key is not a canonical lowercase UUIDv7 or its
    time is outside 2000-01-01T00:00:00Z to 9999-12-31T23:59:59.999Z.
    """
    if _UUID_V7.fullmatch(idempotency_key) is None:
        raise IdempotencyKeyError(f"{idempotency_key!r} is not a lowercase UUIDv7")
    time_ms = int(idempotency_key[0:8] + idempotency_key[9:13], 16)
    if not MIN_KEY_TIME_MS <= time_ms <= MAX_KEY_TIME_MS:
        raise IdempotencyKeyError(f"{idempotency_key!r} carries a time outside the allowed years")
    return time_ms


def derive_session_id(harness: str, idempotency_key: str) -> str:
    """Return the session ID that start_session derives from a key.

    The result is ``<harness>-<YYYYMMDD>-<HHMMSS>-<hex6>``: the key's UTC time floored to
    the second, then the first 6 lowercase hex digits of sha256 over the key's ASCII text.
    Raises IdempotencyKeyError for an invalid key.
    """
    time_ms = idempotency_key_time_ms(idempotency_key)
    started = datetime.fromtimestamp(time_ms // 1000, tz=UTC)
    digest = hashlib.sha256(idempotency_key.encode("ascii")).hexdigest()
    return f"{harness}-{started:%Y%m%d-%H%M%S}-{digest[:6]}"


@dataclass(frozen=True)
class ContractChoice:
    """The contract a launcher hands off with, or why it falls back to the plain chat."""

    contract: int | None
    plain_reason: str | None


def choose_contract(
    launcher_contracts: Sequence[int], describe: Mapping[str, Any], mode: str
) -> ContractChoice:
    """Return the highest contract both sides support that has ``mode``.

    ``describe`` is a ``--describe`` document that already passed its schema. The plain
    reason is ``no_common_contract`` or ``mode_missing``.
    """
    described: Mapping[str, Any] = describe["contracts"]
    common = sorted({int(version) for version in described} & set(launcher_contracts), reverse=True)
    if not common:
        return ContractChoice(None, "no_common_contract")
    for version in common:
        if mode in described[str(version)]["modes"]:
            return ContractChoice(version, None)
    return ContractChoice(None, "mode_missing")


def required_auth_variable(auth: Mapping[str, Any]) -> str | None:
    """Return the one environment variable that holds the secret of ``dex.auth``, if any."""
    match auth.get("mode"):
        case "bearer":
            return "DEX_CREDENTIAL_FILE" if auth.get("source") == "file" else "DEX_CREDENTIAL"
        case "api_key":
            return "DEX_API_KEY"
        case _:
            return None


def build_terminal_ui_environment(
    launcher_environment: Mapping[str, str], auth: Mapping[str, Any]
) -> dict[str, str]:
    """Return the environment a launcher gives the terminal UI: the allow-list only."""
    names = list(TERMINAL_ENVIRONMENT_NAMES)
    auth_variable = required_auth_variable(auth)
    if auth_variable is not None:
        names.append(auth_variable)
    return {name: launcher_environment[name] for name in names if name in launcher_environment}


def missing_auth_variable(environment: Mapping[str, str], auth: Mapping[str, Any]) -> str | None:
    """Return the auth variable the terminal UI needs but lacks or has empty, or None."""
    auth_variable = required_auth_variable(auth)
    if auth_variable is None or environment.get(auth_variable, ""):
        return None
    return auth_variable


def is_open_url_allowed(web_url: str | None, url: str) -> bool:
    """Return whether open_url may open ``url`` for a launch doc whose web_url is ``web_url``."""
    if web_url is None or any(ord(character) <= 0x20 or character in "\x7f\\" for character in url):
        return False
    try:
        base = urlsplit(web_url)
        target = urlsplit(url)
        base_origin = _url_origin(base)
        target_origin = _url_origin(target)
    except ValueError:
        return False
    if base_origin is None or target_origin != base_origin or "@" in target.netloc:
        return False
    if any(_is_dot_segment(segment) for segment in target.path.split("/")):
        return False
    base_path = base.path.rstrip("/")
    return target.path == base_path or target.path.startswith(base_path + "/") or not base_path


def _url_origin(parts: SplitResult) -> tuple[str, str, int] | None:
    scheme = parts.scheme.lower()
    if scheme not in _DEFAULT_PORTS or not parts.hostname:
        return None
    return scheme, parts.hostname.lower(), parts.port or _DEFAULT_PORTS[scheme]


def _is_dot_segment(segment: str) -> bool:
    return segment.lower().replace("%2e", ".") in (".", "..")


def displayed_notice_text(text: str) -> str:
    """Return a notice text as the terminal UI shows it.

    Every control character (U+0000 to U+001F and U+007F to U+009F) becomes a space. A
    text longer than 500 code points keeps its first 499 and ends with U+2026.
    """
    cleaned = "".join(" " if _is_control_character(character) else character for character in text)
    if len(cleaned) > NOTICE_MAX_CODE_POINTS:
        return cleaned[: NOTICE_MAX_CODE_POINTS - 1] + "…"
    return cleaned


def _is_control_character(character: str) -> bool:
    code_point = ord(character)
    return code_point < 0x20 or 0x7F <= code_point <= 0x9F


class NoticeRepeatFilter:
    """Hides a notice whose level and shown text were shown less than 10 s earlier."""

    def __init__(self) -> None:
        self._last_shown_ms: dict[tuple[str, str], int] = {}

    def should_show(self, at_ms: int, level: str, shown_text: str) -> bool:
        """Return whether to show a notice that arrives at ``at_ms``, and remember it if so."""
        last_shown_ms = self._last_shown_ms.get((level, shown_text))
        if last_shown_ms is not None and at_ms - last_shown_ms < NOTICE_REPEAT_WINDOW_MS:
            return False
        self._last_shown_ms[(level, shown_text)] = at_ms
        return True


# Channel framing.


class ChannelProtocolError(Exception):
    """A line on the channel breaks the framing rules."""


class ChannelLineTooLongError(ChannelProtocolError):
    """A line is longer than 1 MiB; the receiver read and discarded all of it."""


class ChannelConnection:
    """Newline-delimited JSON over one end of a Unix stream socket."""

    def __init__(self, channel_socket: socket.socket) -> None:
        self._socket = channel_socket
        self._buffer = bytearray()
        self._is_at_eof = False

    def send_message(self, message: Mapping[str, Any]) -> None:
        """Send one message as compact UTF-8 JSON on one line."""
        line = json.dumps(message, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        if len(line) > MAX_LINE_BYTES:
            raise ChannelProtocolError(f"a {len(line)}-byte line is over the limit")
        self.send_line(line)

    def send_line(self, line: bytes) -> None:
        """Send raw bytes followed by a newline; a peer that already closed is ignored."""
        with contextlib.suppress(BrokenPipeError, ConnectionResetError):
            self._socket.sendall(line + b"\n")

    def receive_line(self, timeout_seconds: float) -> bytes | None:
        """Return the next line without its newline, or None at EOF.

        Raises TimeoutError when no full line arrives in time, ChannelLineTooLongError
        for a line over 1 MiB, and ChannelProtocolError for bytes after the last newline.
        """
        deadline = time.monotonic() + timeout_seconds
        is_discarding = False
        while True:
            newline_index = self._buffer.find(b"\n")
            if newline_index >= 0:
                line = bytes(self._buffer[:newline_index])
                del self._buffer[: newline_index + 1]
                if is_discarding or len(line) > MAX_LINE_BYTES:
                    raise ChannelLineTooLongError("a line is longer than 1 MiB")
                return line
            if len(self._buffer) > MAX_LINE_BYTES:
                is_discarding = True
                self._buffer.clear()
            if self._is_at_eof:
                if self._buffer or is_discarding:
                    raise ChannelProtocolError("the channel ended inside a line")
                return None
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("no line arrived in time")
            self._socket.settimeout(remaining)
            try:
                chunk = self._socket.recv(65536)
            except TimeoutError:
                continue
            except ConnectionResetError:
                chunk = b""
            if not chunk:
                self._is_at_eof = True
            self._buffer.extend(chunk)

    def receive_message(self, timeout_seconds: float) -> dict[str, Any] | None:
        """Return the next message, or None at EOF; see receive_line for the errors."""
        line = self.receive_line(timeout_seconds)
        return None if line is None else parse_channel_line(line)

    def close(self) -> None:
        """Close this end; the peer reads EOF."""
        with contextlib.suppress(OSError):
            self._socket.shutdown(socket.SHUT_RDWR)
        self._socket.close()


def parse_channel_line(line: bytes) -> dict[str, Any]:
    """Parse one line into a message: a UTF-8 JSON object with a string ``type``."""
    try:
        message = json.loads(line.decode("utf-8"))
    except (UnicodeDecodeError, ValueError) as error:
        raise ChannelProtocolError(f"the line is not UTF-8 JSON: {error}") from error
    if not isinstance(message, dict) or not isinstance(message.get("type"), str):
        raise ChannelProtocolError("the line is not a JSON object with a string type")
    return message


# Transcript replay.


class ReplayMismatchError(Exception):
    """The peer did something the transcript does not say."""


@dataclass
class ReplayRecorder:
    """Writes what the host saw to a JSON Lines file, if one was named."""

    path: Path | None

    def record(self, event: str, **fields: Any) -> None:
        """Append one event."""
        if self.path is None:
            return
        with self.path.open("a", encoding="utf-8") as record_file:
            record_file.write(json.dumps({"event": event, **fields}, ensure_ascii=False) + "\n")


class TranscriptReplay:
    """Plays one role of a transcript over a channel and checks the other role's messages."""

    def __init__(
        self,
        role: str,
        transcript: Mapping[str, Any],
        channel: ChannelConnection,
        recorder: ReplayRecorder,
        step_timeout_seconds: float,
        started_at: float,
    ) -> None:
        self._role = role
        self._transcript = transcript
        self._channel = channel
        self._recorder = recorder
        self._step_timeout_seconds = step_timeout_seconds
        self._started_at = started_at
        self._aliases: dict[str, str] = {}
        self._fixture_harness = _launch_harness(transcript)
        self._actual_harness = self._fixture_harness
        self._is_channel_closed = False

    def run(self) -> None:
        """Play every step; raise ReplayMismatchError on the first difference."""
        for index, step in enumerate(self._transcript["steps"]):
            try:
                self._play_step(step)
            except ReplayMismatchError as error:
                raise ReplayMismatchError(f"step {index}: {error}") from error

    def finish_channel(self) -> None:
        """Close this role's end, after reading the peer's EOF if this role is the launcher."""
        if self._is_channel_closed:
            return
        if self._role == "launcher":
            self._expect_end_of_channel()
        self._channel.close()
        self._is_channel_closed = True

    def wait_for_peer_close(self) -> None:
        """Block until the peer closes the channel or a signal ends the process."""
        with contextlib.suppress(ChannelProtocolError):
            while self._channel.receive_line(3600.0) is not None:
                pass

    def _play_step(self, step: Mapping[str, Any]) -> None:
        if "pause_ms" in step:
            if step["role"] == self._role:
                time.sleep(step["pause_ms"] / 1000)
            return
        if "close" in step:
            self._play_close(step["close"])
            return
        if "silence_ms" in step:
            if step["from"] != self._role:
                self._expect_silence(step["silence_ms"])
            return
        if step["from"] == self._role:
            self._send_step(step)
        else:
            self._receive_step(step)

    def _play_close(self, closing_role: str) -> None:
        if closing_role == self._role:
            self._channel.close()
            self._is_channel_closed = True
        else:
            self._expect_end_of_channel()

    def _send_step(self, step: Mapping[str, Any]) -> None:
        if "raw_line" in step:
            line = step["raw_line"].encode("utf-8")
            line += b" " * max(0, step.get("pad_to_bytes", 0) - len(line))
            self._channel.send_line(line)
            self._recorder.record("sent_raw_line", bytes=len(line))
            return
        message = _substitute(step["message"], self._aliases)
        self._channel.send_message(message)
        self._recorder.record("sent", message=message)

    def _receive_step(self, step: Mapping[str, Any]) -> None:
        timeout_seconds = self._step_timeout_seconds
        if step.get("message", {}).get("type") == "ready":
            elapsed = time.monotonic() - self._started_at
            timeout_seconds = max(0.0, READY_TIMEOUT_SECONDS - elapsed)
        if "raw_line" in step:
            self._receive_raw_step(step, timeout_seconds)
            return
        try:
            received = self._channel.receive_message(timeout_seconds)
        except TimeoutError as error:
            raise ReplayMismatchError(f"expected {step['message']} but nothing arrived") from error
        except ChannelProtocolError as error:
            raise ReplayMismatchError(f"expected {step['message']}: {error}") from error
        if received is None:
            raise ReplayMismatchError(f"expected {step['message']} but the channel ended")
        self._recorder.record("received", message=received)
        self._compare(step["message"], received, step.get("ignore", []))

    def _receive_raw_step(self, step: Mapping[str, Any], timeout_seconds: float) -> None:
        expected = step["raw_line"].encode("utf-8")
        is_oversize = step.get("pad_to_bytes", 0) > MAX_LINE_BYTES
        try:
            line = self._channel.receive_line(timeout_seconds)
        except ChannelLineTooLongError:
            if is_oversize:
                self._recorder.record("received_oversize_line")
                return
            raise ReplayMismatchError("received a line over 1 MiB") from None
        except (TimeoutError, ChannelProtocolError) as error:
            raise ReplayMismatchError(f"expected a raw line: {error}") from error
        if is_oversize or line is None or line.rstrip(b" ") != expected:
            raise ReplayMismatchError(f"expected the raw line {expected!r}, received {line!r}")
        self._recorder.record("received_raw_line", bytes=len(line))

    def _expect_silence(self, silence_ms: int) -> None:
        try:
            line = self._channel.receive_line(silence_ms / 1000)
        except TimeoutError:
            return
        raise ReplayMismatchError(f"expected silence for {silence_ms} ms, received {line!r}")

    def _expect_end_of_channel(self) -> None:
        try:
            line = self._channel.receive_line(self._step_timeout_seconds)
        except TimeoutError as error:
            raise ReplayMismatchError("expected the channel to end") from error
        except ChannelProtocolError as error:
            raise ReplayMismatchError(f"expected the channel to end: {error}") from error
        if line is not None:
            raise ReplayMismatchError(f"expected the channel to end, received {line!r}")

    def _compare(
        self, fixture: Mapping[str, Any], received: Mapping[str, Any], ignore: Any
    ) -> None:
        expected = _substitute(fixture, self._aliases)
        if expected.get("type") == "launch" and "command.harness" in ignore:
            self._actual_harness = str(_read_path(received, "command.harness"))
        for path in ignore:
            self._bind_alias(path, _read_path(fixture, path), _read_path(received, path))
        if _without_paths(expected, ignore) != _without_paths(received, ignore):
            raise ReplayMismatchError(
                f"expected {json.dumps(expected, sort_keys=True)}, "
                f"received {json.dumps(received, sort_keys=True)}"
            )

    def _bind_alias(self, path: str, fixture_value: object, received_value: object) -> None:
        if not isinstance(fixture_value, str) or not isinstance(received_value, str):
            return
        self._aliases[fixture_value] = received_value
        if path.endswith("idempotency_key"):
            with contextlib.suppress(IdempotencyKeyError):
                fixture_session_id = derive_session_id(self._fixture_harness, fixture_value)
                received_session_id = derive_session_id(self._actual_harness, received_value)
                self._aliases[fixture_session_id] = received_session_id


def _launch_harness(transcript: Mapping[str, Any]) -> str:
    for step in transcript["steps"]:
        message = step.get("message")
        if isinstance(message, dict) and message.get("type") == "launch":
            return str(message.get("command", {}).get("harness", ""))
    return ""


def _substitute(value: Any, aliases: Mapping[str, str]) -> Any:
    if isinstance(value, str):
        return aliases.get(value, value)
    if isinstance(value, list):
        return [_substitute(item, aliases) for item in value]
    if isinstance(value, dict):
        return {key: _substitute(item, aliases) for key, item in value.items()}
    return value


def _read_path(message: Mapping[str, Any], path: str) -> object:
    value: object = message
    for part in path.split("."):
        if not isinstance(value, dict) or part not in value:
            return None
        value = value[part]
    return value


def _without_paths(message: Mapping[str, Any], paths: Sequence[str]) -> dict[str, Any]:
    copied: dict[str, Any] = json.loads(json.dumps(message))
    for path in paths:
        *parents, leaf = path.split(".")
        container: object = copied
        for part in parents:
            container = container.get(part) if isinstance(container, dict) else None
        if isinstance(container, dict):
            container.pop(leaf, None)
    return copied


# Roles.


def run_launcher_role(arguments: argparse.Namespace) -> int:
    """Spawn the terminal UI command, replay the launcher side, and check its exit."""
    transcript = load_transcript(Path(arguments.transcript))
    recorder = ReplayRecorder(Path(arguments.record) if arguments.record else None)
    auth = _launch_auth(transcript)
    environment = build_terminal_ui_environment(os.environ, auth)
    for assignment in arguments.extra_env:
        name, _, value = assignment.partition("=")
        environment[name] = value
    launcher_socket, terminal_ui_socket = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
    # A high fd number catches a terminal UI that assumes fd 3.
    child_fd = fcntl.fcntl(terminal_ui_socket.fileno(), fcntl.F_DUPFD, 10)
    terminal_ui_socket.close()
    command = [*arguments.command, "--channel-fd", str(child_fd)]
    recorder.record("spawn", argv=command, environment_names=sorted(environment))
    started_at = time.monotonic()
    with _spawned_terminal_ui(command, environment, child_fd, arguments) as process:
        os.close(child_fd)
        channel = ChannelConnection(launcher_socket)
        replay = TranscriptReplay(
            "launcher", transcript, channel, recorder, arguments.step_timeout, started_at
        )
        try:
            replay.run()
            if transcript["tui_exit"] == "terminated":
                return _terminate_and_check(process, recorder)
            replay.finish_channel()
        except ReplayMismatchError as error:
            print(f"terminal UI contract host: {error}", file=sys.stderr)
            recorder.record("mismatch", detail=str(error))
            return 1
        return _check_exit_code(process, transcript["tui_exit"], arguments.step_timeout, recorder)


@contextlib.contextmanager
def _spawned_terminal_ui(
    command: Sequence[str],
    environment: Mapping[str, str],
    child_fd: int,
    arguments: argparse.Namespace,
) -> Iterator[subprocess.Popen[bytes]]:
    if arguments.pty is None:
        process = subprocess.Popen(
            command, env=environment, pass_fds=(child_fd,), stdin=subprocess.DEVNULL
        )
        try:
            yield process
        finally:
            _stop_process(process)
        return
    columns, rows = (int(part) for part in arguments.pty.split("x"))
    master_fd, slave_fd = pty.openpty()
    fcntl.ioctl(slave_fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))
    process = subprocess.Popen(
        command,
        env=environment,
        pass_fds=(child_fd,),
        stdin=slave_fd,
        stdout=slave_fd,
        stderr=slave_fd,
        preexec_fn=_take_controlling_terminal,
    )
    os.close(slave_fd)
    screen_path = Path(arguments.screen_log) if arguments.screen_log else None
    drain = threading.Thread(target=_drain_terminal, args=(master_fd, screen_path), daemon=True)
    drain.start()
    try:
        yield process
    finally:
        _stop_process(process)
        drain.join(timeout=2)
        with contextlib.suppress(OSError):
            os.close(master_fd)


def _take_controlling_terminal() -> None:
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)


def _drain_terminal(master_fd: int, screen_path: Path | None) -> None:
    screen_file: BinaryIO | None = screen_path.open("wb") if screen_path else None
    try:
        while True:
            try:
                chunk = os.read(master_fd, 65536)
            except OSError:
                return
            if not chunk:
                return
            if screen_file is not None:
                screen_file.write(chunk)
                screen_file.flush()
    finally:
        if screen_file is not None:
            screen_file.close()


def _stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


def _terminate_and_check(process: subprocess.Popen[bytes], recorder: ReplayRecorder) -> int:
    process.send_signal(signal.SIGTERM)
    try:
        exit_code = process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        print("terminal UI contract host: the terminal UI ignored SIGTERM", file=sys.stderr)
        return 1
    recorder.record("exit", exit_code=exit_code)
    if exit_code not in (143, -signal.SIGTERM):
        print(f"terminal UI contract host: exit {exit_code} after SIGTERM", file=sys.stderr)
        return 1
    return 0


def _check_exit_code(
    process: subprocess.Popen[bytes],
    expected_exit_code: int,
    timeout_seconds: float,
    recorder: ReplayRecorder,
) -> int:
    try:
        exit_code = process.wait(timeout=timeout_seconds)
    except subprocess.TimeoutExpired:
        print("terminal UI contract host: the terminal UI did not exit", file=sys.stderr)
        return 1
    recorder.record("exit", exit_code=exit_code)
    if exit_code != expected_exit_code:
        print(
            f"terminal UI contract host: exit {exit_code}, expected {expected_exit_code}",
            file=sys.stderr,
        )
        return 1
    return 0


def _launch_auth(transcript: Mapping[str, Any]) -> Mapping[str, Any]:
    for step in transcript["steps"]:
        message = step.get("message")
        if isinstance(message, dict) and message.get("type") == "launch":
            auth: Mapping[str, Any] = message.get("dex", {}).get("auth", {})
            return auth
    return {}


def run_terminal_ui_role(arguments: argparse.Namespace) -> int:
    """Answer --describe, or replay the terminal UI side over --channel-fd."""
    if arguments.describe:
        describe = DEFAULT_DESCRIBE
        if arguments.describe_file:
            describe = json.loads(Path(arguments.describe_file).read_text(encoding="utf-8"))
        print(json.dumps(describe, separators=(",", ":")))
        return 0
    if arguments.channel_fd is None or arguments.transcript is None:
        print("agents-tui needs --describe, or --channel-fd with --transcript", file=sys.stderr)
        return 2
    started_at = time.monotonic()
    recorder = ReplayRecorder(Path(arguments.record) if arguments.record else None)
    names = sorted(os.environ)
    recorder.record(
        "start",
        argv=sys.argv[1:],
        environment_names=names,
        allowed_environment={
            name: os.environ[name] for name in names if name in ALLOWED_ENVIRONMENT_NAMES
        },
        terminal=_describe_terminal(),
    )
    try:
        os.set_inheritable(arguments.channel_fd, False)
        channel_socket = socket.socket(fileno=arguments.channel_fd)
    except OSError as error:
        print(f"--channel-fd {arguments.channel_fd} is not a socket: {error}", file=sys.stderr)
        return 2
    transcript = load_transcript(Path(arguments.transcript))
    replay = TranscriptReplay(
        "tui",
        transcript,
        ChannelConnection(channel_socket),
        recorder,
        arguments.step_timeout,
        started_at,
    )
    try:
        replay.run()
    except ReplayMismatchError as error:
        print(f"terminal UI contract host: {error}", file=sys.stderr)
        recorder.record("mismatch", detail=str(error))
        return HOST_FAILURE_EXIT_CODE
    if transcript["tui_exit"] == "terminated":
        replay.wait_for_peer_close()
        return 143
    replay.finish_channel()
    exit_code: int = transcript["tui_exit"]
    return exit_code


def _describe_terminal() -> dict[str, Any] | None:
    if not os.isatty(0):
        return None
    size = os.get_terminal_size(0)
    return {
        "columns": size.columns,
        "rows": size.lines,
        "is_foreground": os.tcgetpgrp(0) == os.getpgrp(),
    }


def load_transcript(path: Path) -> dict[str, Any]:
    """Read a transcript file."""
    transcript: dict[str, Any] = json.loads(path.read_text(encoding="utf-8"))
    return transcript


def parse_arguments(argv: Sequence[str]) -> argparse.Namespace:
    """Parse the command line of either role."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0] if __doc__ else None)
    roles = parser.add_subparsers(dest="role", required=True)
    launcher = roles.add_parser("launcher", help="spawn a terminal UI and play the launcher")
    launcher.add_argument("--transcript", required=True)
    launcher.add_argument("--record", help="JSON Lines file of what the host saw")
    launcher.add_argument("--pty", metavar="COLUMNSxROWS", help="run the terminal UI on a PTY")
    launcher.add_argument("--screen-log", help="file that receives the PTY output")
    launcher.add_argument("--step-timeout", type=float, default=10.0)
    launcher.add_argument(
        "--extra-env",
        action="append",
        default=[],
        metavar="NAME=VALUE",
        help="add a variable beyond the allow-list, for tests only",
    )
    launcher.add_argument("command", nargs="+", help="the terminal UI command, after --")
    terminal_ui = roles.add_parser("tui", help="act as dexcli agents-tui")
    terminal_ui.add_argument("--transcript")
    terminal_ui.add_argument("--record", help="JSON Lines file of what the host saw")
    terminal_ui.add_argument("--describe-file", help="the --describe document to print")
    terminal_ui.add_argument("--step-timeout", type=float, default=10.0)
    terminal_ui.add_argument("subcommand", choices=["agents-tui"])
    terminal_ui.add_argument("--describe", action="store_true")
    terminal_ui.add_argument("--channel-fd", type=int)
    return parser.parse_args(argv)


def main(argv: Sequence[str]) -> int:
    """Run the role the command line names."""
    arguments = parse_arguments(argv)
    if arguments.role == "launcher":
        return run_launcher_role(arguments)
    return run_terminal_ui_role(arguments)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
