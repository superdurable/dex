#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import importlib.util
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "check_package", Path(__file__).resolve().parents[1] / "script/check-package.py",
)
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class ExternalUrlTest(unittest.TestCase):
    def test_accepts_allowed_destinations(self):
        for url in (
            "https://docs.superdurable.io/reference/",
            "https://docs.superdurable.io/reference/go/",
            "https://docs.superdurable.io/reference/server/configuration#limits",
            "https://github.com/superdurable/dex/tree/main/examples/go",
            "https://github.com/superdurable/dex/blob/main/examples/python/polling/flow.py",
            "https://github.com/superdurable/dex-connectors-library",
            "https://github.com/superdurable/dex-connectors-library/blob/main/README.md",
            "https://github.com/superdurable/dex-connectors-library/blob/main/sdkgo/projectconfig/README.md",
            "https://github.com/superdurable/dex-connectors-library/tree/main/connectors/slack/go/examples/thread-approval",
            "https://connectors.superdurable.io/catalog.yaml",
            "https://github.com/superdurable/dex-skills",
            "https://api.github.com/repos/superdurable/dex-skills/releases/latest",
            "http://127.0.0.1:8802",
            "http://localhost:8080/api",
            "https://auth.example.com/oauth/token",
        ):
            with self.subTest(url=url):
                self.assertIsNone(CHECKER.external_url_problem(url))

    def test_rejects_pins_and_other_destinations(self):
        for url in (
            "https://docs.superdurable.io/primitives/flow",
            "https://docs.superdurable.io/design-patterns/polling",
            "https://docs.superdurable.io/build-with-ai/dex-developer-skill",
            "https://docs.superdurable.io/reference/go/1.35/",
            "https://github.com/superdurable/dex/blob/sdk-go/v1.35.0/sdk-go/dex/flow.go",
            "https://github.com/superdurable/dex/tree/3f2a9c1/examples",
            "https://github.com/superdurable/dex/releases/tag/cli-v1.36.0",
            "https://github.com/superdurable/dex-connectors-library/blob/sdkgo/v0.25.0/README.md",
            "https://github.com/superdurable/dex/commit/abc1234",
            "https://raw.githubusercontent.com/superdurable/dex/main/README.md",
            "https://pkg.go.dev/github.com/superdurable/dex/sdk-go",
            "https://github.com/superdurable/dex/blob/main/sdk-go/dex/client.go",
            "https://github.com/superdurable/dex/releases",
            "https://github.com/superdurable/dex/releases/latest",
            "https://github.com/superdurable/dex",
            "https://github.com/superdurable/dex/issues/1",
            "https://github.com/superdurable/dex/blob/main/examples/../sdk-go/dex/client.go",
            "https://github.com/superdurable/dex-connectors-library/blob/main/sdkgo/projectconfig/config.go",
            "https://github.com/superdurable/dex-skills/releases",
            "https://api.github.com/repos/superdurable/dex/releases/latest",
            "https://connectors.superdurable.io/",
            "https://connectors.superdurable.io/connectors/slack",
        ):
            with self.subTest(url=url):
                self.assertIsNotNone(CHECKER.external_url_problem(url))


class DexVersionTest(unittest.TestCase):
    def test_finds_dex_versions(self):
        for line in (
            "Go SDK v1.35.0 adds the option.",
            "server-zd v0.1.18 changed the default.",
            "Pinned at sdk-rust/v1.35.0.",
            "The CLI release cli-v1.36.0 bundles it.",
            "Dex 1.26 removed the Go Server.",
            "Requires 1.31.0 or later.",
            "Newer than v0.1.16, the queue is durable.",
            "pip install dex-python-sdk==1.30.0",
            "go get github.com/superdurable/dex/sdk-go@v1.35.0",
            "Use the Dex SDK release 1.30.0.",
            "Since 1.35, this works.",
            "Changed in 1.35: the default is 30 seconds.",
            "Added in v1.36.",
            "Deprecated in 1.20.",
        ):
            with self.subTest(line=line):
                self.assertTrue(CHECKER.find_dex_versions(line))

    def test_ignores_standards_and_toolchains(self):
        for line in (
            "Render with dexcli visualize --schema-version 2.0 --json.",
            "FDG 2.0 requires one explanation per Step.",
            "TLS requires version 1.2 or newer.",
            "Use OAuth 2.0 client credentials.",
            "Rust 1.97 or newer builds the SDK.",
            "Node 22.18 and later strip types.",
            "n8n 2.36.0 added the field.",
            "Dex Web v2 shows the Run.",
            "Bind to 0.0.0.0:8080.",
            "Retry with a backoff coefficient of 2.0.",
            "Set index: 2.0 in the codex = 1.2 table.",
            "The Schedule Trigger `typeVersion` 1.4 runs.",
        ):
            with self.subTest(line=line):
                self.assertEqual(CHECKER.find_dex_versions(line), [])

    def test_checks_only_dex_releases_in_n8n_documents(self):
        self.assertEqual(CHECKER.find_dex_versions("Before v4.4 the HTTP Request node resent credentials.", True), [])
        self.assertTrue(CHECKER.find_dex_versions("Pin sdk-go/v1.35.0 for the backend.", True))
        self.assertTrue(CHECKER.find_dex_versions("Before v4.4 the HTTP Request node resent credentials."))


class BoundaryTest(unittest.TestCase):
    def test_finds_backend_terms(self):
        for text in (
            "The Flow uses Continue-As-New.",
            "Call ContinueAsNew.",
            "Temporal stores the history.",
            "Set the StartToClose timeout.",
            "Cadence is not supported.",
            "Use temporal signals, queries and updates.",
            "Deploy a temporal cluster.",
            "The cadence server stores it.",
            "Send signals, queries, or updates to the Flow.",
            "Replay the workflow history.",
            "The decision task timed out.",
            "Set the activity timeout.",
            "Use UpdateWithStart.",
        ):
            with self.subTest(text=text):
                self.assertTrue(CHECKER.find_boundary_terms(text))

    def test_accepts_dex_terms(self):
        for text in (
            "A Step method timeout bounds one attempt; temporal order is not guaranteed.",
            "Test the heartbeat cadence with a fake clock.",
            "Read the Flow history for diagnosis.",
            "Have the fake signal that the Step started.",
            "A query-only RPC reads a retained run.",
            "The n8n workflow imports into one Flow.",
        ):
            with self.subTest(text=text):
                self.assertEqual(CHECKER.find_boundary_terms(text), [])


class MarkdownAnchorTest(unittest.TestCase):
    def test_uses_github_heading_slugs(self):
        content = "# Read `Attributes` from outside a Flow\n## SYNC vs. ASYNC\n## Notes\n## Notes\n```\n# not a heading\n```\n"
        self.assertEqual(
            CHECKER.markdown_anchors(content),
            {"read-attributes-from-outside-a-flow", "sync-vs-async", "notes", "notes-1"},
        )


class MarkdownLinkTest(unittest.TestCase):
    def test_skips_code(self):
        content = "See [Step](step.md#time-bounds) and `DefineChannel[T](name)`.\n```go\nx := [T](other.md)\n```\n"
        self.assertEqual(CHECKER.find_markdown_links(content), ["step.md#time-bounds"])


class FrontMatterTest(unittest.TestCase):
    def test_joins_block_scalar_descriptions(self):
        with tempfile.TemporaryDirectory() as directory:
            skill = Path(directory) / "SKILL.md"
            skill.write_text("---\nname: dex-sdk\ndescription: >-\n  Build Dex apps.\n  Use for SDK work.\n---\n# Dex SDK\n")
            self.assertEqual(
                CHECKER.parse_front_matter(skill),
                {"name": "dex-sdk", "description": "Build Dex apps. Use for SDK work."},
            )


class PullRequestBodyTest(unittest.TestCase):
    def test_prefers_the_fetched_body_file(self):
        with tempfile.TemporaryDirectory() as directory:
            body_file = Path(directory) / "body.txt"
            body_file.write_text("skills-release: no bump needed: typo only\n")
            self.assertEqual(CHECKER.read_pr_body("stale body", str(body_file)), body_file.read_text())
        self.assertEqual(CHECKER.read_pr_body("inline body", None), "inline body")


class ReleaseOptOutTest(unittest.TestCase):
    def test_requires_a_reason(self):
        self.assertTrue(CHECKER.RELEASE_OPT_OUT.search("Summary\n\nskills-release: no bump needed: typo only\n"))
        self.assertTrue(CHECKER.RELEASE_OPT_OUT.search("- skills-release: no bump needed: link fix\n"))
        self.assertFalse(CHECKER.RELEASE_OPT_OUT.search("skills-release: no bump needed:\n"))


if __name__ == "__main__":
    unittest.main()
