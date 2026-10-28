#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Summarize `go test -json` output as one Markdown row per top-level test."""

import json
import re
import sys
from collections import Counter

STATUS_LINE_PATTERN = re.compile(r"^\s*(=== (RUN|PAUSE|CONT|NAME)|--- (PASS|FAIL|SKIP)):?\s")
ASSERTION_LINE_PATTERN = re.compile(r"^\s*(Error:|Messages:|panic:|.*_test\.go:\d+:|.*\.go:\d+: )")
MAX_DETAIL_CHARACTERS = 300

def read_test_events(json_paths):
    results = {}
    outputs = {}
    for json_path in json_paths:
        with open(json_path, encoding="utf-8") as json_file:
            for line in json_file:
                if not line.startswith("{"):
                    continue
                event = json.loads(line)
                test_name = event.get("Test")
                if not test_name:
                    continue
                action = event.get("Action")
                if action == "output":
                    outputs.setdefault(test_name, []).append(event.get("Output", ""))
                elif action == "run":
                    results.setdefault(test_name, "incomplete")
                elif action in ("pass", "fail", "skip"):
                    results[test_name] = action
    return results, outputs

def classify_test(test_name, results, outputs):
    """Returns the test's effective result; a pass whose leaf subtests all skipped counts as a skip."""
    result = results[test_name]
    if result != "pass":
        return result, describe_test(test_name, result, results, outputs)
    leaf_subtests = leaf_subtest_names(test_name, results)
    skipped_subtests = [name for name in leaf_subtests if results[name] == "skip"]
    if not skipped_subtests:
        return "pass", ""
    skip_reason = last_message_line(outputs.get(skipped_subtests[0], []))
    if len(skipped_subtests) == len(leaf_subtests):
        return "skip", f"all {len(leaf_subtests)} subtests skipped: {skip_reason}"
    return "pass", f"{len(skipped_subtests)} of {len(leaf_subtests)} subtests skipped: {skip_reason}"

def leaf_subtest_names(test_name, results):
    subtests = [name for name in results if name.startswith(test_name + "/")]
    return [
        name for name in subtests
        if not any(other.startswith(name + "/") for other in subtests)
    ]

def describe_test(test_name, result, results, outputs):
    if result == "skip":
        return last_message_line(outputs.get(test_name, []))
    failed_subtests = [
        name for name, subtest_result in results.items()
        if name.startswith(test_name + "/") and subtest_result in ("fail", "incomplete")
    ]
    detail_lines = assertion_lines(outputs.get(test_name, []))
    for subtest_name in failed_subtests:
        detail_lines.extend(assertion_lines(outputs.get(subtest_name, [])))
    detail = " ".join(detail_lines)
    if failed_subtests:
        detail = "failed subtests: " + ", ".join(failed_subtests) + "; " + detail
    return detail

def last_message_line(output_lines):
    message_lines = [
        line.strip() for line in output_lines
        if line.strip() and not STATUS_LINE_PATTERN.match(line)
    ]
    return message_lines[-1] if message_lines else ""

def assertion_lines(output_lines):
    return [line.strip() for line in output_lines if ASSERTION_LINE_PATTERN.match(line)][:6]

def escape_markdown_cell(text):
    text = text.replace("|", "\\|").replace("\n", " ")
    if len(text) > MAX_DETAIL_CHARACTERS:
        text = text[:MAX_DETAIL_CHARACTERS] + "..."
    return text

def main():
    if len(sys.argv) < 2:
        print(f"usage: {sys.argv[0]} <go-test-json>...", file=sys.stderr)
        return 2
    results, outputs = read_test_events(sys.argv[1:])
    top_level_tests = sorted(name for name in results if "/" not in name)
    classified_tests = {name: classify_test(name, results, outputs) for name in top_level_tests}
    counts = Counter(result for result, _ in classified_tests.values())
    print("| Result | Count |")
    print("| --- | --- |")
    for result in ("pass", "skip", "fail", "incomplete"):
        print(f"| {result} | {counts.get(result, 0)} |")
    print()
    print("| Test | Result | Detail |")
    print("| --- | --- | --- |")
    for test_name in top_level_tests:
        result, detail = classified_tests[test_name]
        print(f"| {test_name} | {result} | {escape_markdown_cell(detail)} |")
    return 0

if __name__ == "__main__":
    sys.exit(main())
