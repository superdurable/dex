# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

import server_zd_compatibility as compatibility  # noqa: E402

SCRIPT = Path(__file__).with_name("server_zd_compatibility.py")
FRAGMENTS_SCRIPT = Path(__file__).with_name("changelog_fragments.py")
COMPATIBILITY_FILE = compatibility.COMPATIBILITY_FILE
RULES_FILE = compatibility.RULES_FILE
DOCS_INTRO = "---\nid: changelog\ntitle: Change log\n---\n\n# Change log\n\nUser-visible changes, newest first.\n\n"
URL = "https://github.com/superdurable/dex/releases/tag/"
SDK_SOURCES = {client.name: client.protocol_source for client in compatibility.SDK_CLIENTS}
SERVER_SOURCE = compatibility.SERVER_PROTOCOL_SOURCE
TAG_PREFIXES = {"go": "sdk-go/v", "python": "sdk-python/v", "typescript": "sdk-typescript/v", "java": "sdk-java/v", "rust": "sdk-rust/v"}
RULES = {
    "oldestSupportedServerZd": "0.1.2",
    "serverZd": [{"since": "0.1.0", "protocol": {"min": 1, "max": 4}}, {"since": "0.1.3", "protocol": {"min": 1, "max": 5}}],
    "clients": {
        "go": [{"since": "1.0.0", "protocol": {"min": 4, "max": 4}}, {"since": "1.1.0", "protocol": {"min": 5, "max": 5}}],
        "python": [{"since": "1.0.0", "protocol": {"min": 4, "max": 5}}],
        "typescript": [{"since": "1.0.0", "protocol": {"min": 4, "max": 5}}],
        "java": [{"since": "1.0.0", "protocol": {"min": 4, "max": 5}}],
        "rust": [{"since": "1.0.0", "protocol": {"min": 4, "max": 5}}],
        "dexcli": [{"since": "1.0.0", "protocol": {"min": 4, "max": 4}}, {"since": "1.1.0", "protocol": {"min": 5, "max": 5}}],
        "agents": [{"since": "0.1.0"}, {"since": "0.3.0", "minimumServerZd": "0.1.3"}],
        "agentsCli": [{"since": "0.2.0"}, {"since": "0.3.0", "minimumServerZd": "0.1.3"}],
    },
    "unpublished": [
        {"component": "agents", "version": "0.2.0", "registry": "pypi"},
        {"component": "agentsCli", "version": "0.2.0", "registry": "pypi"},
    ],
}


def released_entry(heading: str, tag_prefix: str, versions: list[str]) -> str:
    """Returns one docs change-log entry whose Released line records versions, the first one first."""
    first, *later = versions
    line = f"Released 2026-11-01 as [{tag_prefix}{first}]({URL}{tag_prefix}{first})"
    for version in later:
        line += f"; {version} released 2026-11-02 as [{tag_prefix}{version}]({URL}{tag_prefix}{version})"
    return f"## {heading} {{#v{heading.replace('.', '-')}}}\n\n{line}.\n\n### Added\n\n- A change.\n\n"


def client_source(minimum: int, maximum: int) -> str:
    return f"minimumSupportedServerProtocolVersion = {minimum}\nmaximumSupportedServerProtocolVersion = {maximum}\n"


def server_source(maximum: int) -> str:
    return f"pub const MINIMUM_SUPPORTED_PROTOCOL_VERSION: u32 = 1;\npub const CURRENT_PROTOCOL_VERSION: u32 = {maximum};\n"


class ServerZdCompatibilityIntegrationTest(unittest.TestCase):
    """A fixture with server-zd 0.1.1 to 0.1.3, where 0.1.3 adds protocol 5, and clients that need it."""

    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.repository = Path(self.temporary_directory.name) / "dex"
        self.repository.mkdir()
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Release Test")
        self.git("config", "user.email", "release-test@example.com")
        self.write_rules(RULES)
        self.write(SERVER_SOURCE, server_source(4))
        for name, source in SDK_SOURCES.items():
            self.write(source, client_source(4, 5 if name != "go" else 4))
        self.write_pins("1.0.0")
        self.write("changelog.d/go/.gitkeep", "")
        self.write_changelogs(go=[["1.0.0", "1.0.2"]], cli=[["1.0.0"]], agents=["0.1.0", "0.2.0"])
        self.commit("protocol 4")
        for tag in ("server-zd/v0.1.1", "server-zd/v0.1.2", "sdk-go/v1.0.0", "sdk-go/v1.0.2", "cli-v1.0.0",
                    "agents/v0.1.0", "agents/v0.2.0"):
            self.git("tag", tag)
        for name in ("python", "typescript", "java", "rust"):
            self.git("tag", f"{TAG_PREFIXES[name]}1.0.0")
        self.write(SERVER_SOURCE, server_source(5))
        self.write(SDK_SOURCES["go"], client_source(5, 5))
        self.write_changelogs(go=[["1.1.0"], ["1.0.0", "1.0.2"]], cli=[["1.1.0"], ["1.0.0"]], agents=["0.1.0", "0.2.0", "0.3.0"])
        self.commit("protocol 5")
        for tag in ("server-zd/v0.1.3", "sdk-go/v1.1.0", "cli-v1.1.0"):
            self.git("tag", tag)

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_update_lists_the_oldest_and_newest_compatible_release_per_server(self) -> None:
        self.run_script("update", "--date", "2026-11-14")
        document = json.loads(self.read(COMPATIBILITY_FILE))

        self.assertEqual(document["schemaVersion"], 1)
        self.assertEqual(document["generatedAt"], "2026-11-14")
        self.assertEqual([server["serverZd"] for server in document["servers"]], ["0.1.3", "0.1.2"])
        newest, older = document["servers"]
        self.assertEqual(newest["protocolVersion"], {"min": 1, "max": 5})
        self.assertEqual(older["protocolVersion"], {"min": 1, "max": 4})
        # Go 1.1.0 needs protocol 5, and the change log skips 1.0.1, so the older server gets 1.0.2.
        self.assertEqual(newest["sdks"]["go"], {"oldest": "1.0.0", "newest": "1.1.0"})
        self.assertEqual(older["sdks"]["go"], {"oldest": "1.0.0", "newest": "1.0.2"})
        self.assertEqual(older["sdks"]["python"], {"oldest": "1.0.0", "newest": "1.0.0"})
        self.assertEqual(older["dexcli"], {"oldest": "1.0.0", "newest": "1.0.0"})
        self.assertEqual(
            newest["agents"]["npm"],
            {"oldest": {"version": "0.1.0", "pinnedSdk": "1.0.0"}, "newest": {"version": "0.3.0", "pinnedSdk": "1.0.0"}},
        )
        # 0.3.0 needs server-zd 0.1.3, and 0.2.0 has no PyPI release.
        self.assertEqual(older["agents"]["npm"]["newest"]["version"], "0.2.0")
        self.assertEqual(older["agents"]["pypi"]["newest"]["version"], "0.1.0")
        self.assertEqual(older["agentsCli"], {"npm": {"oldest": "0.2.0", "newest": "0.2.0"}, "pypi": None})
        self.assertEqual(newest["agentsCli"]["pypi"], {"oldest": "0.3.0", "newest": "0.3.0"})
        self.assertEqual(self.run_script("check").returncode, 0)

    def test_a_raised_server_minimum_protocol_moves_the_oldest_release_up(self) -> None:
        rules = json.loads(json.dumps(RULES))
        rules["serverZd"][1]["protocol"]["min"] = 5
        self.write_rules(rules)
        self.write(SERVER_SOURCE, server_source(5).replace("= 1;", "= 5;"))
        self.run_script("update", "--date", "2026-11-14")
        newest = json.loads(self.read(COMPATIBILITY_FILE))["servers"][0]

        self.assertEqual(newest["protocolVersion"], {"min": 5, "max": 5})
        self.assertEqual(newest["sdks"]["go"], {"oldest": "1.1.0", "newest": "1.1.0"})
        self.assertEqual(newest["dexcli"], {"oldest": "1.1.0", "newest": "1.1.0"})
        # Python speaks 4..5, so a server that accepts only 5 still serves its first line.
        self.assertEqual(newest["sdks"]["python"], {"oldest": "1.0.0", "newest": "1.0.0"})

    def test_generated_at_changes_only_with_the_content(self) -> None:
        self.run_script("update", "--date", "2026-11-14")
        self.assertIn("unchanged", self.run_script("update", "--date", "2026-11-20").stdout)
        self.assertEqual(json.loads(self.read(COMPATIBILITY_FILE))["generatedAt"], "2026-11-14")

        self.write_changelogs(go=[["1.1.0", "1.1.1"], ["1.0.0", "1.0.2"]], cli=[["1.1.0"], ["1.0.0"]],
                              agents=["0.1.0", "0.2.0", "0.3.0"])
        self.run_script("update", "--date", "2026-11-20")
        document = json.loads(self.read(COMPATIBILITY_FILE))
        self.assertEqual(document["generatedAt"], "2026-11-20")
        self.assertEqual(document["servers"][0]["sdks"]["go"]["newest"], "1.1.1")

    def test_check_rejects_a_stale_file_and_rules_that_disagree_with_tags_or_the_working_tree(self) -> None:
        self.run_script("update", "--date", "2026-11-14")
        self.write(COMPATIBILITY_FILE, self.read(COMPATIBILITY_FILE).replace('"1.0.2"', '"1.0.1"'))
        self.assertIn("differs from the generator output", self.run_script("check", check=False).stderr)
        self.run_script("update", "--date", "2026-11-14")

        self.write(SDK_SOURCES["python"], client_source(4, 6))
        result = self.run_script("check", check=False)
        self.assertIn("Add a line whose since is the next python release", result.stderr)
        self.write(SDK_SOURCES["python"], client_source(4, 5))

        rules = json.loads(json.dumps(RULES))
        rules["serverZd"][1]["since"] = "0.1.2"
        self.write_rules(rules)
        result = self.run_script("check", check=False)
        self.assertIn("serverZd 0.1.2 has protocol 1..4 at tag server-zd/v0.1.2, but its rule line says 1..5", result.stderr)
        self.write_rules(RULES)

        self.git("tag", "server-zd/v0.1.4")
        self.assertIn("tag server-zd/v0.1.4 exists, but the server change log", self.run_script("check", check=False).stderr)

    def test_only_the_newest_agents_release_may_lack_its_tag(self) -> None:
        self.git("tag", "-d", "agents/v0.1.0")
        result = self.run_script("update", check=False)
        self.assertEqual(result.returncode, 1)
        self.assertIn("tag agents/v0.1.0 is missing", result.stderr)

    def test_prepare_rewrites_the_file_and_release_prep_may_change_it(self) -> None:
        self.run_script("update", "--date", "2026-11-14")
        self.write("changelog.d/go/fix-aaaaaa.md", "---\nsection: Fixed\n---\n- Fix a thing.\n")
        self.commit("compatibility file and a fragment")
        self.git("switch", "-c", "release-prep/go")

        result = self.run_fragments("prepare", "go", "--version", "1.1.1", "--date", "2026-11-15")
        self.assertIn(f"{COMPATIBILITY_FILE} updated", result.stdout)
        document = json.loads(self.read(COMPATIBILITY_FILE))
        self.assertEqual(document["generatedAt"], "2026-11-15")
        self.assertEqual(document["servers"][0]["sdks"]["go"]["newest"], "1.1.1")
        self.commit("prepare go")

        body = Path(self.temporary_directory.name) / "body.txt"
        body.write_text("## Summary\n", encoding="utf-8")
        check = self.run_fragments(
            "check-pr", "--base-ref", "main", "--pr-body-file", str(body), "--pr-number", "9000", "--labels", "release-prep",
            check=False,
        )
        self.assertEqual(check.returncode, 0, check.stderr)
        self.assertEqual(self.run_script("check").returncode, 0)

    def test_registry_check_retries_reports_missing_versions_and_stale_exceptions(self) -> None:
        answers = {
            "https://proxy.golang.org/github.com/superdurable/dex/sdk-go/@v/v1.0.2.info": [404, 200],
            "https://pypi.org/pypi/dex-python-sdk/1.0.0/json": [0, 404, 404],
            "https://pypi.org/pypi/dex-agents/0.2.0/json": [200],
        }
        requested = []

        def fetch(url: str, headers: dict[str, str]) -> int:
            requested.append(url)
            self.assertIn("User-Agent", headers)
            return answers[url].pop(0)

        client = compatibility.RegistryClient(attempts=3, delay=0, fetch=fetch)
        errors = compatibility.registry_errors(
            [("go", "goproxy", "1.0.2"), ("python", "pypi", "1.0.0")],
            frozenset({("agents", "pypi", "0.2.0")}),
            client,
        )

        self.assertEqual(
            errors,
            [
                "python 1.0.0 is not published on pypi",
                f"agents 0.2.0 is published on pypi; remove its unpublished entry from {RULES_FILE}",
            ],
        )
        self.assertEqual(requested.count("https://pypi.org/pypi/dex-python-sdk/1.0.0/json"), 3)

        failing = compatibility.RegistryClient(attempts=2, delay=0, fetch=lambda url, headers: 503)
        with self.assertRaisesRegex(compatibility.CompatibilityError, "answered 503"):
            failing.exists("agents", "pypi", "0.2.0", should_wait=False)

    def test_registry_requests_name_each_package(self) -> None:
        self.assertEqual(
            compatibility.registry_request("typescript", "npm", "1.45.4")[0],
            "https://registry.npmjs.org/@superdurable%2Fdex/1.45.4",
        )
        self.assertEqual(
            compatibility.registry_request("java", "maven", "1.45.1")[0],
            "https://repo1.maven.org/maven2/io/superdurable/dex-sdk/1.45.1/dex-sdk-1.45.1.pom",
        )
        self.assertEqual(
            compatibility.registry_request("dexcli", "github", "1.47.7")[0],
            "https://api.github.com/repos/superdurable/dex/releases/tags/cli-v1.47.7",
        )
        self.assertEqual(
            compatibility.registry_request("agentsCli", "pypi", "0.6.8")[0],
            "https://pypi.org/pypi/dex-agents-cli-core/0.6.8/json",
        )

    def write_changelogs(self, go: list[list[str]], cli: list[list[str]], agents: list[str]) -> None:
        def entries(prefix: str, groups: list[list[str]]) -> str:
            return "".join(released_entry(".".join(group[0].split(".")[:2]), prefix, group) for group in groups)

        self.write("docs/reference/go/changelog.mdx", DOCS_INTRO + entries("sdk-go/v", go))
        self.write("docs/reference/cli/changelog.mdx", DOCS_INTRO + entries("cli-v", cli))
        for name in ("python", "typescript", "java", "rust"):
            self.write(f"docs/reference/{name}/changelog.mdx", DOCS_INTRO + entries(TAG_PREFIXES[name], [["1.0.0"]]))
        servers = [["0.1.3"], ["0.1.2"], ["0.1.1"]]
        self.write("docs/reference/server/changelog.mdx", DOCS_INTRO + "".join(
            released_entry(group[0], "server-zd/v", group) for group in servers))
        self.write("docs/reference/agents/changelog.mdx", DOCS_INTRO + "".join(
            released_entry(".".join(version.split(".")[:2]), "agents/v", [version]) for version in reversed(agents)))

    def write_pins(self, sdk_version: str) -> None:
        manifest = json.dumps({"name": "core", "peerDependencies": {"@superdurable/dex": sdk_version}})
        project = f'[project]\ndependencies = [\n    "dex-python-sdk=={sdk_version}",\n]\n'
        for sources in compatibility.AGENTS_PIN_SOURCES.values():
            self.write(sources["npm"], manifest)
            self.write(sources["pypi"], project)

    def write_rules(self, rules: dict) -> None:
        self.write(RULES_FILE, json.dumps(rules, indent=2) + "\n")

    def run_script(self, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        return self.run_python(SCRIPT, arguments, check)

    def run_fragments(self, *arguments: str, check: bool = True) -> subprocess.CompletedProcess[str]:
        return self.run_python(FRAGMENTS_SCRIPT, arguments, check)

    def run_python(self, script: Path, arguments: tuple[str, ...], check: bool) -> subprocess.CompletedProcess[str]:
        result = subprocess.run(
            (sys.executable, str(script), *arguments),
            cwd=self.repository, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        if check:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def read(self, relative_path: str) -> str:
        return (self.repository / relative_path).read_text(encoding="utf-8")

    def write(self, relative_path: str, content: str) -> None:
        path = self.repository / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def commit(self, message: str) -> None:
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", message)

    def git(self, *arguments: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *arguments), cwd=self.repository, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )


if __name__ == "__main__":
    unittest.main()
