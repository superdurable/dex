#!/usr/bin/env python3
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Runs summarize-integ-test-json.py against synthetic `go test -json` partitions."""

import json
import os
import subprocess
import sys
import tempfile
import unittest

SUMMARIZER_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "summarize-integ-test-json.py")


class SummarizeIntegTestJSONTest(unittest.TestCase):
    def test_passing_package_succeeds(self):
        completed = self.run_summarizer(test_events("TestAlpha", "pass") + [package_event("pass")])
        self.assertEqual(0, completed.returncode, completed.stderr)

    def test_package_failure_after_passing_tests_fails(self):
        teardown_failure = [package_output("TestMain teardown failed\n"), package_event("fail")]
        completed = self.run_summarizer(test_events("TestAlpha", "pass") + teardown_failure)
        self.assertEqual(1, completed.returncode)
        self.assertIn("package fail event", completed.stderr)

    def test_missing_package_result_fails(self):
        completed = self.run_summarizer(test_events("TestAlpha", "pass"))
        self.assertEqual(1, completed.returncode)
        self.assertIn("no package pass event", completed.stderr)

    def test_failing_test_fails(self):
        completed = self.run_summarizer(test_events("TestAlpha", "fail") + [package_event("fail")])
        self.assertEqual(1, completed.returncode)
        self.assertIn("fail: 1 tests", completed.stderr)

    def run_summarizer(self, events):
        with tempfile.TemporaryDirectory() as directory:
            json_path = os.path.join(directory, "partition.json")
            expected_tests_path = os.path.join(directory, "partition.tests.txt")
            with open(json_path, "w", encoding="utf-8") as json_file:
                json_file.writelines(json.dumps(event) + "\n" for event in events)
            with open(expected_tests_path, "w", encoding="utf-8") as expected_tests_file:
                expected_tests_file.write("TestAlpha\n")
            return subprocess.run(
                [sys.executable, SUMMARIZER_PATH, "--expected-tests", expected_tests_path, json_path],
                capture_output=True,
                text=True,
                check=False,
            )


def test_events(test_name, result):
    return [
        {"Action": "run", "Package": "integ", "Test": test_name},
        {"Action": result, "Package": "integ", "Test": test_name},
    ]


def package_event(result):
    return {"Action": result, "Package": "integ"}


def package_output(output):
    return {"Action": "output", "Package": "integ", "Output": output}


if __name__ == "__main__":
    unittest.main()
