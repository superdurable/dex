#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Summarize `go test -json` output as one Markdown row per top-level test."""

import argparse
import json
import os
import re
import sys
from collections import Counter

STATUS_LINE_PATTERN = re.compile(r"^\s*(=== (RUN|PAUSE|CONT|NAME)|--- (PASS|FAIL|SKIP)):?\s")
ASSERTION_LINE_PATTERN = re.compile(r"^\s*(Error:|Messages:|panic:|.*_test\.go:\d+:|.*\.go:\d+: )")
SKIP_LOCATION_PATTERN = re.compile(r"^([A-Za-z0-9_]+\.go):(\d+):$")
PACKAGE_FAILURE_PATTERN = re.compile(r"panic:|test timed out|^FAIL\b")
MAX_DETAIL_CHARACTERS = 300
INTEG_DIRECTORY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")

def read_test_events(json_paths):
    """Returns test results, test outputs, package failure lines, and why each JSON file lacks a package pass."""
    results = {}
    outputs = {}
    package_failures = []
    unpassed_package_errors = []
    for json_path in json_paths:
        package_results = set()
        with open(json_path, encoding="utf-8") as json_file:
            for line in json_file:
                if not line.startswith("{"):
                    continue
                event = json.loads(line)
                test_name = event.get("Test")
                if not test_name:
                    if event.get("Action") in ("pass", "fail"):
                        package_results.add(event["Action"])
                    package_output = event.get("Output", "")
                    if PACKAGE_FAILURE_PATTERN.search(package_output):
                        package_failures.append(f"{json_path}: {package_output.strip()}")
                    continue
                action = event.get("Action")
                if action == "output":
                    outputs.setdefault(test_name, []).append(event.get("Output", ""))
                elif action == "run":
                    results.setdefault(test_name, "incomplete")
                elif action in ("pass", "fail", "skip"):
                    results[test_name] = action
        # A package fails after every test passes when TestMain or teardown fails.
        if "fail" in package_results:
            unpassed_package_errors.append(f"{json_path}: package fail event; go test failed outside a test")
        elif "pass" not in package_results:
            unpassed_package_errors.append(f"{json_path}: no package pass event; go test did not complete")
    return results, outputs, package_failures, unpassed_package_errors


def read_expected_tests(expected_test_paths):
    expected_tests = set()
    for expected_test_path in expected_test_paths:
        with open(expected_test_path, encoding="utf-8") as expected_test_file:
            expected_tests.update(line.strip() for line in expected_test_file if line.strip())
    return expected_tests


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
    if not message_lines:
        return ""
    return explain_empty_skip(message_lines[-1])


def explain_empty_skip(message_line):
    """Replaces a bare `file.go:N:` skip location with the guard condition above it."""
    location = SKIP_LOCATION_PATTERN.match(message_line)
    if not location:
        return message_line
    source_path = os.path.join(INTEG_DIRECTORY, location.group(1))
    try:
        with open(source_path, encoding="utf-8") as source_file:
            source_lines = source_file.readlines()
    except OSError:
        return message_line
    for line_number in range(int(location.group(2)) - 2, -1, -1):
        guard_line = source_lines[line_number].strip()
        if guard_line.startswith("if "):
            return f"{message_line} {guard_line.rstrip('{').strip()}"
    return message_line

def assertion_lines(output_lines):
    return [line.strip() for line in output_lines if ASSERTION_LINE_PATTERN.match(line)][:6]

def escape_markdown_cell(text):
    text = text.replace("|", "\\|").replace("\n", " ")
    if len(text) > MAX_DETAIL_CHARACTERS:
        text = text[:MAX_DETAIL_CHARACTERS] + "..."
    return text

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--expected-tests", action="append", default=[],
        help="file listing one expected top-level test per line; required; unreported tests count as not-run and fail the summary",
    )
    parser.add_argument("json_paths", nargs="+", help="go test -json output files")
    arguments = parser.parse_args()
    results, outputs, package_failures, unpassed_package_errors = read_test_events(arguments.json_paths)
    expected_tests = read_expected_tests(arguments.expected_tests)
    top_level_tests = sorted(name for name in results if "/" not in name)
    classified_tests = {name: classify_test(name, results, outputs) for name in top_level_tests}
    for test_name in expected_tests - set(top_level_tests):
        classified_tests[test_name] = ("not-run", "no test events; the partition ended before this test started")
    top_level_tests = sorted(classified_tests)
    counts = Counter(result for result, _ in classified_tests.values())
    print("| Result | Count |")
    print("| --- | --- |")
    for result in ("pass", "skip", "fail", "incomplete", "not-run"):
        print(f"| {result} | {counts.get(result, 0)} |")
    print()
    if package_failures:
        print("Package-level failures:")
        print()
        for package_failure in package_failures:
            print(f"- {escape_markdown_cell(package_failure)}")
        print()
    print("| Test | Result | Detail |")
    print("| --- | --- | --- |")
    for test_name in top_level_tests:
        result, detail = classified_tests[test_name]
        print(f"| {test_name} | {result} | {escape_markdown_cell(detail)} |")
    summary_errors = find_summary_errors(expected_tests, counts, unpassed_package_errors)
    for summary_error in summary_errors:
        print(f"error: {summary_error}", file=sys.stderr)
    return 1 if summary_errors else 0


def find_summary_errors(expected_tests, counts, unpassed_package_errors):
    """Returns why the run is not a complete, passing run; an empty list means it is."""
    summary_errors = []
    if not expected_tests:
        summary_errors.append("no expected tests; pass --expected-tests with a non-empty test list")
    if not counts:
        summary_errors.append("no test events")
    for result in ("fail", "incomplete", "not-run"):
        if counts.get(result, 0):
            summary_errors.append(f"{result}: {counts[result]} tests")
    summary_errors.extend(unpassed_package_errors)
    return summary_errors

if __name__ == "__main__":
    sys.exit(main())
