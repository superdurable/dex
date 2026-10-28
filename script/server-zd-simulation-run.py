# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Bound nightly commands and publish reproducible timeout diagnostics."""

import argparse
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import threading
import time


def main():
    signal.signal(signal.SIGTERM, lambda signal_number, frame: sys.exit(128 + signal_number))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prepare-progress", action="store_true")
    phase = parser.add_mutually_exclusive_group()
    phase.add_argument("--acceptance", action="store_true")
    phase.add_argument("--build", action="store_true")
    parser.add_argument("--timeout", type=int, default=3000)
    parser.add_argument("--log", type=Path)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    arguments = parser.parse_args()
    if arguments.prepare_progress:
        prepare_progress()
        return 0
    if not arguments.log or not arguments.command or arguments.timeout <= 0:
        parser.error("provide a positive --timeout, --log, and command after --")
    command = arguments.command
    if command[0] == "--":
        command = command[1:]
    return run_command(command, arguments.timeout, arguments.log, arguments.acceptance, arguments.build)


def prepare_progress():
    # Older releases lack progress output; instrument only their successful test-loop branch.
    for filename in ("apply_invariants.rs", "engine_simulation.rs"):
        path = Path("crates/dexzd-simulation/tests") / filename
        source = path.read_text()
        if "simulation completed seed:" in source:
            continue
        original = "Ok(summary) => total.merge(&summary),"
        if source.count(original) != 1:
            raise ValueError(f"cannot instrument seed progress in {path}")
        source = source.replace(
            original,
            'Ok(summary) => {\n'
            '                total.merge(&summary);\n'
            '                println!("simulation completed seed: {seed}");\n'
            '            },',
        )
        path.write_text(source)


def run_command(command, timeout_seconds, log_path, is_acceptance, is_build):
    started = time.monotonic()
    base_seed = os.environ.get("DEXZD_SIMULATION_BASE_SEED", "not selected")
    print(f"Base seed: {base_seed}; command: {command}", flush=True)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    has_timed_out = False
    is_interrupted = False
    with log_path.open("wb") as output:
        process = subprocess.Popen(
            command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True
        )
        log_stream = CommandLogStream(process.stdout, output)
        reader = threading.Thread(target=log_stream.run)
        reader.start()
        try:
            status = process.wait(timeout=timeout_seconds)
            reader.join(timeout=max(0, timeout_seconds - (time.monotonic() - started)))
            if reader.is_alive():
                raise subprocess.TimeoutExpired(command, timeout_seconds)
        except subprocess.TimeoutExpired:
            has_timed_out = True
            stop_process_group(process)
            status = 124
        except (KeyboardInterrupt, SystemExit) as interruption:
            is_interrupted = True
            stop_process_group(process)
            status = interruption.code if isinstance(interruption, SystemExit) else 130
        except BaseException:
            stop_process_group(process)
            raise
        finally:
            reader.join()
            process.stdout.close()
        if log_stream.error is not None:
            raise log_stream.error
    elapsed_seconds = time.monotonic() - started
    log_text = log_path.read_text(errors="replace")
    completed_seeds = re.findall(r"^simulation completed seed: (\d+)$", log_text, re.MULTILINE)
    last_completed_seed = completed_seeds[-1] if completed_seeds else "none"
    has_invariant_failure = not is_acceptance and re.search(
        r"seed \d+ broke an invariant", log_text
    )
    has_nested_timeout = is_acceptance and re.search(r"^Timed out after \d", log_text, re.MULTILINE)
    if has_invariant_failure:
        result = "invariant failure"
        if has_timed_out:
            result += " (timed out while shrinking)"
        status = status or 1
    elif has_timed_out or (status != 0 and has_nested_timeout):
        result = "timeout"
    elif is_interrupted:
        result = "interrupted"
    elif status == 0 and not (is_acceptance or is_build or completed_seeds):
        result = "no completed seeds"
        status = 1
    elif status == 0:
        result = "success"
    elif is_acceptance:
        result = "acceptance failure"
    else:
        result = "command failure"
    phase_prefix = "build_" if is_build else ""
    print(
        f"Result: {'build ' if is_build else ''}{result}; elapsed: {elapsed_seconds:.1f}s; base seed: {base_seed}; "
        f"last completed seed: {last_completed_seed}",
        flush=True,
    )
    if has_timed_out and has_invariant_failure:
        print("An invariant failure was recorded before timeout; inspect the failure artifact.")
    if github_output := os.environ.get("GITHUB_OUTPUT"):
        with Path(github_output).open("a") as output:
            output.write(
                f"{phase_prefix}result={result}\n{phase_prefix}elapsed_seconds={elapsed_seconds:.1f}\n"
                f"{phase_prefix}last_completed_seed={last_completed_seed}\n"
            )
    return status if status >= 0 else 128 - status


class CommandLogStream:
    def __init__(self, source, destination):
        self.source = source
        self.destination = destination
        self.error = None

    def run(self):
        try:
            while chunk := self.source.read1(65536):
                self.destination.write(chunk)
                self.destination.flush()
                sys.stdout.buffer.write(chunk)
                sys.stdout.buffer.flush()
        except Exception as error:
            self.error = error


def stop_process_group(process):
    # Nested acceptance runners need SIGTERM to stop their separately owned cargo sessions.
    previous_handlers = {signal_number: signal.signal(signal_number, signal.SIG_IGN)
                         for signal_number in (signal.SIGTERM, signal.SIGINT)}
    try:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            process.wait()
            return
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            pass
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
    finally:
        for signal_number, handler in previous_handlers.items():
            signal.signal(signal_number, handler)


if __name__ == "__main__":
    sys.exit(main())
