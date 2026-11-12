# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""``dex-agents-cli-e2e``: run the command suite and the framework suite, then print the tables."""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from dex_agents_cli_core.environment import DEXCLI_VERSION
from dex_agents_cli_testing.e2e.command_cases import COMMAND_CASES
from dex_agents_cli_testing.e2e.command_suite import COMMAND_STEPS, run_command_suite
from dex_agents_cli_testing.e2e.dex_server import DexServer, HackerNewsConnectionSeed
from dex_agents_cli_testing.e2e.framework_suite import (
    FRAMEWORK_CASES,
    FRAMEWORK_STEPS,
    run_framework_suite,
)
from dex_agents_cli_testing.e2e.results import ResultTable
from dex_agents_cli_testing.e2e.toolchain import PackageSource, Toolchain

_SUPPORTED_NODE_MAJORS = ("22", "24")
# A private dexcli dev downloads the Connectors MCP server and the Connector release first.
_CONNECTOR_WAIT_SECONDS = 240.0
_NODE_CANDIDATE_DIRS = ("/opt/homebrew/opt/node@24/bin", "/opt/homebrew/opt/node@22/bin")


def main(argv: list[str] | None = None) -> None:
    """Parse the options, run the chosen suites, and exit 1 when a step failed."""
    arguments = _parser().parse_args(argv)
    work_dir: Path = arguments.work_dir.resolve()
    work_dir.mkdir(parents=True, exist_ok=True)
    toolchain = Toolchain(
        checkout=arguments.checkout.resolve(),
        work_dir=work_dir,
        dexcli=_find_dexcli(arguments.dexcli),
        node_bin_dir=arguments.node_bin_dir or _find_node_bin_dir(),
        python_version=arguments.python,
        source=PackageSource(arguments.source),
        published_version=arguments.published_version,
    )
    only = set(arguments.only.split(",")) if arguments.only else None
    server = (
        DexServer.reused(toolchain.dexcli, arguments.reuse_server, arguments.reuse_dex_port)
        if arguments.reuse_server
        else DexServer.private(toolchain.dexcli)
    )
    seed = (
        HackerNewsConnectionSeed(arguments.hacker_news_version)
        if arguments.seed_hacker_news and server.is_private
        else None
    )
    print(f"source: {toolchain.source.value}; checkout {toolchain.checkout}; work dir {work_dir}")
    tables: list[ResultTable] = []
    try:
        print(server.start(work_dir, seed, arguments.connectors_mcp_binary), flush=True)
        if arguments.suite in ("commands", "all"):
            table = ResultTable(f"Commands ({toolchain.source.value})", COMMAND_STEPS)
            tables.append(table)
            cases = [case for case in COMMAND_CASES if only is None or case.name in only]
            run_command_suite(
                cases, toolchain, server, table, _CONNECTOR_WAIT_SECONDS if seed else 15.0
            )
        if arguments.suite in ("frameworks", "all"):
            table = ResultTable(f"Framework examples ({toolchain.source.value})", FRAMEWORK_STEPS)
            tables.append(table)
            frameworks = [case for case in FRAMEWORK_CASES if only is None or case.name in only]
            run_framework_suite(frameworks, toolchain, server, table)
    finally:
        server.stop_if_started()
    report = "\n".join(table.render() for table in tables)
    run_name = f"{arguments.suite}-{toolchain.source.value}"
    report_path = work_dir / f"report-{run_name}.md"
    report_path.write_text(report, encoding="utf-8")
    for index, table in enumerate(tables):
        table.write_json(work_dir / f"results-{run_name}-{index}.json")
    print("\n" + report)
    print(f"Report: {report_path}")
    sys.exit(1 if any(table.has_failure() for table in tables) else 0)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="dex-agents-cli-e2e",
        description="Run the agent CLI commands and harness examples against the mock model API.",
    )
    parser.add_argument("--suite", choices=("commands", "frameworks", "all"), default="all")
    parser.add_argument(
        "--source",
        choices=[source.value for source in PackageSource],
        default=PackageSource.LOCAL.value,
        help="build from --checkout (default) or install the published --published-version",
    )
    parser.add_argument(
        "--checkout",
        type=Path,
        default=Path(__file__).resolve().parents[7],
        help="repository checkout for local builds and the examples (default: this one)",
    )
    parser.add_argument("--published-version", default="0.5.0")
    parser.add_argument(
        "--work-dir",
        type=Path,
        default=Path(tempfile.gettempdir()) / "dex-agents-cli-e2e",
        help="builds, homes, transcripts, request logs, and the report",
    )
    parser.add_argument("--dexcli", type=Path, help=f"dexcli {DEXCLI_VERSION} or later")
    parser.add_argument(
        "--reuse-server",
        metavar="DEX_WEB_URL",
        help="use this running dexcli dev, such as http://127.0.0.1:8802, not a private one; "
        "its sessions and authorized connections are used",
    )
    parser.add_argument(
        "--reuse-dex-port", type=int, default=8801, help="the Dex port of --reuse-server"
    )
    parser.add_argument(
        "--seed-hacker-news",
        action="store_true",
        help="give the private dexcli dev a Hacker News connection for the connector step",
    )
    parser.add_argument(
        "--connectors-mcp-binary",
        type=Path,
        default=Path(os.environ["DEX_CONNECTORS_MCP_BINARY"])
        if os.environ.get("DEX_CONNECTORS_MCP_BINARY")
        else None,
        help="dex-connectors-mcp for the private dexcli dev (default DEX_CONNECTORS_MCP_BINARY)",
    )
    parser.add_argument(
        "--hacker-news-version", default="v0.24.0", help="Hacker News Connector release to seed"
    )
    parser.add_argument("--node-bin-dir", type=Path, help="Node.js 22 or 24 directory")
    parser.add_argument("--python", default="3.13", help="Python version of the environments")
    parser.add_argument("--only", help="comma-separated case names, such as py-openai-sdk")
    return parser


def _find_dexcli(explicit: Path | None) -> Path:
    if explicit is not None:
        return explicit.resolve()
    downloaded = Path.home() / ".dex" / "bin" / f"dexcli-{DEXCLI_VERSION}" / "dexcli"
    if downloaded.exists():
        return downloaded
    found = shutil.which("dexcli")
    if found is None:
        raise SystemExit("no dexcli found; pass --dexcli")
    return Path(found)


def _find_node_bin_dir() -> Path | None:
    if _node_major(shutil.which("node")) in _SUPPORTED_NODE_MAJORS:
        return None
    for candidate in _NODE_CANDIDATE_DIRS:
        if _node_major(os.path.join(candidate, "node")) in _SUPPORTED_NODE_MAJORS:
            return Path(candidate)
    raise SystemExit("the TypeScript commands need Node.js 22 or 24; pass --node-bin-dir")


def _node_major(node: str | None) -> str | None:
    if node is None or not os.path.exists(node):
        return None
    version = subprocess.run([node, "--version"], capture_output=True, text=True, check=False)
    return version.stdout.strip().removeprefix("v").split(".", 1)[0]


if __name__ == "__main__":
    main()
