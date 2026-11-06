#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

"""Validate the Dex Skills package.

The checks cover packaging (manifests, versions, hooks, front matter), the
standalone bundle layout and its local links, the external link allow-list, and
the content boundaries (no downstream products, no backend internals, no Dex
version numbers). They never pin exact file lists or sentences.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
from pathlib import Path
from urllib.parse import urlsplit


ROOT = Path(__file__).resolve().parents[1]
SKILL_NAMES = ("dex-app-builder", "dex-sdk", "dex-connector-contributor")
MANIFESTS = {
    "codex": Path(".codex-plugin/plugin.json"),
    "claude": Path(".claude-plugin/plugin.json"),
    "cursor": Path(".cursor-plugin/plugin.json"),
}
MARKETPLACES = {
    "codex": Path(".agents/plugins/marketplace.json"),
    "claude": Path(".claude-plugin/marketplace.json"),
    "cursor": Path(".cursor-plugin/marketplace.json"),
}
HOOK_CONFIGS = {"claude": "./hooks/claude.json", "cursor": "./hooks/cursor.json"}
SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
CHANGELOG_HEADING = re.compile(r"^## (?P<version>\d+\.\d+\.\d+)(?: - .+)?\s*$", re.MULTILINE)
SKILL_NAME = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
RELEASE_OPT_OUT = re.compile(r"^\s*(?:[-*]\s+)?skills-release:\s*no bump needed:[ \t]*\S", re.IGNORECASE | re.MULTILINE)

MARKDOWN_LINK = re.compile(r"\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)")
FENCED_BLOCK = re.compile(r"^(```|~~~).*?^\1", re.DOTALL | re.MULTILINE)
# Inline code such as `DefineChannel[T](name)` looks like a link but is not one.
INLINE_CODE = re.compile(r"`[^`\n]+`")
HEADING = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$", re.MULTILINE)
EXPLICIT_ANCHOR = re.compile(r"<a\s+(?:id|name)=\"([^\"]+)\"")
URL = re.compile(r"https?://[^\s<>()`\"'\]\\|]+")

# Dex Skills documents source-available Dex only; reject a downstream product name.
DOWNSTREAM_PRODUCT_NAME = re.compile(r"super[\s_-]*verse", re.IGNORECASE)
CONCEPT_BOUNDARY_TERMS = (
    (re.compile(r"continue[\s_-]*as[\s_-]*new", re.IGNORECASE), "Continue-As-New"),
    (re.compile(r"\bTemporal\b"), "the Temporal backend"),
    (re.compile(r"\bCadence\b"), "the Cadence backend"),
    (re.compile(r"\b(?:schedule|start)[\s_-]*to[\s_-]*(?:close|start)\b", re.IGNORECASE), "a backend activity timeout name"),
    (re.compile(r"\b(?:signal|update)[\s_-]*with[\s_-]*start\b", re.IGNORECASE), "a backend Signal or Update API"),
    # Lowercase temporal and cadence are English words; flag them only as backend names.
    (
        re.compile(
            r"\b(?:temporal|cadence)[\s_-]+(?:server|cluster|service|backend|sdk|cli|namespaces?|workflows?|"
            r"activit(?:y|ies)|signals?|quer(?:y|ies)|updates?|history|task[\s_-]*queues?|workers?)\b",
            re.IGNORECASE,
        ),
        "a Temporal or Cadence backend name",
    ),
    (re.compile(r"\bsignals?\s*,\s*quer(?:y|ies)\s*,?\s*(?:and|or)\s*updates?\b", re.IGNORECASE), "backend Signals, Queries, and Updates"),
    (re.compile(r"\b(?:workflow|decision)[\s_-]+(?:history|tasks?)\b", re.IGNORECASE), "backend workflow history or task mechanics"),
    (re.compile(r"\bactivit(?:y|ies)[\s_-]+(?:tasks?|timeouts?|workers?|heartbeats?)\b", re.IGNORECASE), "backend activity mechanics"),
)

VERSION_NUMBER = r"v?\d+\.\d+(?:\.\d+)?(?!\.?\d)"
RELEASE_VERSION_NUMBER = r"(?:v\d+\.\d+(?:\.\d+)?|\d+\.\d+\.\d+)(?!\.?\d)"
DEX_RELEASE_PATTERNS = (
    re.compile(r"\b(?:sdk-(?:go|java|python|typescript|rust)|server-zd|server|web|sdkgo)/v\d+"),
    re.compile(r"\bcli-v\d+"),
    re.compile(r"\b(?:dex[\w-]*|superdurable[\w/.-]*)[\"']?\s*(?:==|~=|>=|@|:|=)\s*[\"']?[\^~]?" + VERSION_NUMBER, re.IGNORECASE),
)
# Version phrases that are Dex versions everywhere except in the documents about n8n.
GENERIC_VERSION_PATTERNS = (
    re.compile(r"\bv\d+\.\d+(?:\.\d+)?(?!\.?\d)"),
    re.compile(r"\b(?:Dex|SDK|server-zd|Server|dexcli|CLI|Web)\s+" + VERSION_NUMBER, re.IGNORECASE),
    re.compile(
        r"\b(?:since|newer than|older than|before|after|until|through)\s+" + RELEASE_VERSION_NUMBER,
        re.IGNORECASE,
    ),
    # Developer reference version notes, such as "Since 1.35" and "Changed in 1.35".
    re.compile(
        r"\b(?:since|changed in|added in|deprecated in|removed in|introduced in)\s+" + VERSION_NUMBER,
        re.IGNORECASE,
    ),
    re.compile(RELEASE_VERSION_NUMBER + r"\s+(?:or|and)\s+(?:later|newer|earlier|older)\b", re.IGNORECASE),
)
# These App Builder references describe n8n releases and node type versions.
N8N_VERSION_DOCUMENTS = {
    "dex-app-builder/references/n8n-semantics.md",
    "dex-app-builder/references/workflow-import.md",
}
# A bare 0.x.y or 1.x.y counts only on a line that names a Dex component or a release.
BARE_DEX_VERSION = re.compile(r"(?<![\w.])[01]\.\d+\.\d+(?!\w|\.\d)")
BARE_VERSION_CONTEXT = re.compile(r"\b(?:dex|sdk|server|cli|web|release|version)", re.IGNORECASE)
# Versions of external standards, formats, and toolchains, not Dex releases.
ALLOWED_VERSION_TEXT = re.compile(
    r"(?i:\b(?:FDG|OAuth|TLS|HTTP|schema[- ]version)[ /]v?\d+(?:\.\d+)?|--schema-version[ =]+\d+\.\d+)"
    r"|(?<![-\w/])(?:Node(?:\.js)?|Go|Rust|Java|JDK|Python|TypeScript|n8n) \d+(?:\.\d+)*"
    r"|\btypeVersion`?:?\s+\d+(?:\.\d+)*"
)

LOCAL_HOSTS = {"localhost", "127.0.0.1", "0.0.0.0", "[::1]"}
PLACEHOLDER_HOST_SUFFIXES = (".example.com", ".example", ".invalid", ".test")
# Patterns match host plus path, without the scheme, query, fragment, or a trailing slash.
DEX_EXAMPLES_URL = re.compile(r"github\.com/superdurable/dex/(?:blob|tree)/main/examples(?:/[^?#]*)?")
# Each exception is a destination the Developer reference cannot replace.
ALLOWED_EXTERNAL_URL_EXCEPTIONS = (
    (
        re.compile(r"connectors\.superdurable\.io/catalog\.yaml"),
        "the released Connector catalog, which lists each Connector and its language support",
    ),
    (
        re.compile(r"github\.com/superdurable/dex-connectors-library"),
        "the Connector library, which Connectors are released from",
    ),
    (
        re.compile(r"github\.com/superdurable/dex-connectors-library/(?:blob|tree)/main/(?:[^?#]+/)?examples(?:/[^?#]*)?"),
        "runnable Connector examples",
    ),
    (
        re.compile(r"github\.com/superdurable/dex-connectors-library/blob/main/(?:[^?#]+/)?README\.md"),
        "Connector and Connector SDK READMEs, the reference for released Connectors",
    ),
    (
        re.compile(r"github\.com/superdurable/dex-skills"),
        "the Skills repository named in the plugin update notice",
    ),
    (
        re.compile(r"api\.github\.com/repos/superdurable/dex-skills/releases/latest"),
        "the release lookup of the plugin version check",
    ),
)


def fail(message: str) -> None:
    raise SystemExit(message)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-ref", help="check that skill changes since this ref carry release metadata")
    parser.add_argument("--pr-body", default="", help="pull request description, for the release opt-out marker")
    arguments = parser.parse_args()

    version = (ROOT / "VERSION").read_text().strip()
    check_versions(version)
    check_manifests(version)
    skills = check_skill_layout()
    check_skill_links(skills)
    check_skill_content(skills)
    check_agent_rules()
    check_open_source_boundary()
    if arguments.base_ref:
        check_release_change(arguments.base_ref, version, arguments.pr_body)
    print(f"validated superdurable-dex {version} with three root skills")


def check_versions(version: str) -> None:
    version_tuple(version)
    if (ROOT / "dex-sdk" / "VERSION").read_text().strip() != version:
        fail("dex-sdk/VERSION must match the root VERSION")
    headings = {match.group("version") for match in CHANGELOG_HEADING.finditer((ROOT / "CHANGELOG.md").read_text())}
    if version not in headings:
        fail(f"CHANGELOG.md must have a '## {version} - YYYY-MM-DD' section")


def check_manifests(version: str) -> None:
    manifests = {name: load_json(path) for name, path in MANIFESTS.items()}
    for name, manifest in manifests.items():
        location = MANIFESTS[name]
        if manifest.get("name") != "superdurable-dex":
            fail(f"{location} must name plugin superdurable-dex")
        if manifest.get("version") != version:
            fail(f"{location} version must be {version}")
        if manifest.get("repository") != "https://github.com/superdurable/dex-skills":
            fail(f"{location} must use the dex-skills repository")
        if manifest.get("author", {}).get("name") != "Super Durable":
            fail(f"{location} publisher must remain Super Durable")
    check_hook_configs(manifests)

    skill_paths = [f"./{name}" for name in SKILL_NAMES]
    codex = manifests["codex"]
    if codex.get("skills") != "./" or "mainSkill" in codex:
        fail("Codex manifest must discover root skills with ./ and no mainSkill")
    interface = codex.get("interface", {})
    if interface.get("displayName") != "Dex" or interface.get("developerName") != "Super Durable":
        fail("Codex interface must display Dex by Super Durable")
    for field in ("composerIcon", "logo"):
        if interface.get(field) != "./assets/logo.png":
            fail(f"Codex {field} must use ./assets/logo.png")
    prompts = interface.get("defaultPrompt")
    if not isinstance(prompts, list) or not prompts or any("$dex-" in prompt for prompt in prompts):
        fail("Codex default prompts must be natural-language requests without standalone Skill syntax")
    if manifests["cursor"].get("skills") != skill_paths:
        fail("Cursor manifest must expose all three public skills")
    if not (ROOT / "assets/logo.png").read_bytes().startswith(b"\x89PNG\r\n\x1a\n"):
        fail("assets/logo.png must be a PNG file")

    marketplaces = {name: load_json(path) for name, path in MARKETPLACES.items()}
    for name, marketplace in marketplaces.items():
        plugins = marketplace.get("plugins")
        if marketplace.get("name") != "superdurable" or not isinstance(plugins, list) or len(plugins) != 1:
            fail(f"{MARKETPLACES[name]} must be the superdurable marketplace with one plugin")
        if plugins[0].get("name") != "superdurable-dex":
            fail(f"{MARKETPLACES[name]} has the wrong plugin ID")
    codex_entry = marketplaces["codex"]["plugins"][0]
    if codex_entry.get("source", {}).get("path") != "./" or "version" in codex_entry:
        fail("Codex marketplace must point to the repository root without a version")
    for name in ("claude", "cursor"):
        entry = marketplaces[name]["plugins"][0]
        if entry.get("source") != "./" or entry.get("version") != version:
            fail(f"{MARKETPLACES[name]} must point to the repository root at version {version}")
    if marketplaces["claude"]["plugins"][0].get("skills") != skill_paths:
        fail("Claude marketplace must expose all three public skills")


def check_hook_configs(manifests: dict) -> None:
    for name, expected in HOOK_CONFIGS.items():
        if manifests[name].get("hooks") != expected:
            fail(f"{name} manifest hooks must be {expected}")
    if (ROOT / "hooks" / "hooks.json").exists():
        fail("hooks/hooks.json must not exist; clients require dedicated hook configs")
    script = (ROOT / "hooks" / "version-check.mjs").read_text()
    for forbidden in ("npm install", "npx skills", "plugin update", "plugin marketplace upgrade"):
        if forbidden in script:
            fail(f"version check hook must not install or update: {forbidden}")

    claude = load_json(Path(HOOK_CONFIGS["claude"]))
    entries = claude.get("hooks", {}).get("SessionStart", [])
    if len(entries) != 1 or entries[0].get("matcher") != "startup|clear|fork" or len(entries[0].get("hooks", [])) != 1:
        fail("Claude hooks must define one SessionStart command for startup|clear|fork")
    claude_hook = entries[0]["hooks"][0]
    expected_args = ["${CLAUDE_PLUGIN_ROOT}/hooks/version-check.mjs", "--client", "claude"]
    if claude_hook.get("command") != "node" or claude_hook.get("args") != expected_args:
        fail("Claude hook must run the shared version script with Node for the Claude client")

    cursor = load_json(Path(HOOK_CONFIGS["cursor"]))
    command = 'node "${CURSOR_PLUGIN_ROOT}/hooks/version-check.mjs" --client cursor'
    expected_commands = {"workspaceOpen": f"{command} --refresh-only", "sessionStart": command}
    for event, expected in expected_commands.items():
        hooks = cursor.get("hooks", {}).get(event, [])
        if len(hooks) != 1 or hooks[0].get("command") != expected:
            fail(f"Cursor {event} hook must run: {expected}")
    for hook in [claude_hook, *(cursor["hooks"][event][0] for event in expected_commands)]:
        if hook.get("timeout") != 2:
            fail("version hook timeouts must be two seconds")
    for forbidden in ("resume", "compact", "SessionEnd", "sessionEnd"):
        if forbidden in json.dumps({"claude": claude, "cursor": cursor}):
            fail(f"version hooks must not run on {forbidden}")


def check_skill_layout() -> list[Path]:
    found = sorted(path.parent.name for path in ROOT.glob("*/SKILL.md"))
    if found != sorted(SKILL_NAMES):
        fail(f"expected exactly the three public Dex skills, found: {', '.join(found)}")
    if (ROOT / "plugins").exists():
        fail("plugins/ wrapper must not exist")
    skills = [ROOT / name for name in SKILL_NAMES]
    for skill in skills:
        front_matter = parse_front_matter(skill / "SKILL.md")
        if front_matter.get("name") != skill.name or SKILL_NAME.fullmatch(skill.name) is None:
            fail(f"{skill.name}/SKILL.md front matter name must be {skill.name}")
        description = front_matter.get("description", "")
        if not description or len(description) > 1024 or "<" in description or ">" in description:
            fail(f"{skill.name}/SKILL.md needs a description of at most 1024 characters without angle brackets")
        if front_matter.get("disable-model-invocation") == "true":
            fail(f"{skill.name}/SKILL.md must allow model invocation")
        agent = skill / "agents" / "openai.yaml"
        if not agent.is_file() or "allow_implicit_invocation: true" not in agent.read_text():
            fail(f"{skill.name}/agents/openai.yaml must allow implicit invocation")
    if (ROOT / "dex-connector-contributor" / "references").exists():
        fail("dex-connector-contributor must defer to the target repository, not ship references")
    return skills


def check_skill_links(skills: list[Path]) -> None:
    """Every local link must resolve inside the three installed skill directories."""
    bundle = [path.resolve() for path in skills]
    anchors: dict[Path, set[str]] = {}
    for skill in skills:
        markdown_files = sorted(skill.rglob("*.md"))
        graph: dict[Path, set[Path]] = {path.resolve(): set() for path in markdown_files}
        for markdown in markdown_files:
            for target in find_markdown_links(markdown.read_text()):
                if "://" in target or target.startswith("mailto:"):
                    continue
                path_part, _, anchor = target.strip("<>").partition("#")
                resolved = (markdown.parent / path_part).resolve() if path_part else markdown.resolve()
                location = f"{markdown.relative_to(ROOT)}: {target}"
                if not any(resolved.is_relative_to(root) for root in bundle):
                    fail(f"link escapes the three installed skills in {location}")
                if not resolved.exists():
                    fail(f"broken link in {location}")
                if anchor and resolved.suffix == ".md":
                    if resolved not in anchors:
                        anchors[resolved] = markdown_anchors(resolved.read_text())
                    if anchor not in anchors[resolved]:
                        fail(f"broken anchor in {location}")
                if resolved in graph:
                    graph[markdown.resolve()].add(resolved)
        reachable: set[Path] = set()
        pending = [(skill / "SKILL.md").resolve()]
        while pending:
            current = pending.pop()
            if current not in reachable:
                reachable.add(current)
                pending.extend(graph[current] - reachable)
        unreachable = [str(path.relative_to(ROOT)) for path in markdown_files if path.resolve() not in reachable]
        if unreachable:
            fail(f"files not reachable from {skill.name}/SKILL.md: {', '.join(unreachable)}")


def check_skill_content(skills: list[Path]) -> None:
    problems = []
    for skill in skills:
        for path in sorted([*skill.rglob("*.md"), *skill.glob("agents/*.yaml")]):
            location = path.relative_to(ROOT)
            content = path.read_text()
            if "[TODO:" in content:
                problems.append(f"{location}: unfinished [TODO: placeholder")
            for url in sorted(set(URL.findall(content))):
                problem = external_url_problem(url.rstrip(".,;:*"))
                if problem:
                    problems.append(f"{location}: {problem}: {url}")
            for term in find_boundary_terms(content):
                problems.append(f"{location}: names {term}; describe the Dex behavior instead")
            is_n8n_document = location.as_posix() in N8N_VERSION_DOCUMENTS
            for line_number, text in find_dex_versions(content, is_n8n_document):
                problems.append(f"{location}:{line_number}: version number {text!r}; skills contain no version numbers")
    if problems:
        fail("skill content problems:\n" + "\n".join(f"  {problem}" for problem in problems))


def check_agent_rules() -> None:
    agents = (ROOT / "AGENTS.md").read_text()
    if (ROOT / "CLAUDE.md").read_text() != agents:
        fail("AGENTS.md and CLAUDE.md must be identical")
    if not (ROOT / ".cursor" / "rules" / "dex-skills.mdc").read_text().endswith(agents):
        fail("the Cursor rule must contain the same repository instructions")


def check_open_source_boundary() -> None:
    for relative_path in git_output("ls-files").splitlines():
        path = ROOT / relative_path
        if not path.is_file():
            continue
        try:
            content = path.read_text()
        except UnicodeDecodeError:
            continue
        if DOWNSTREAM_PRODUCT_NAME.search(content):
            fail(f"{relative_path} must not describe a product built on Dex")


def check_release_change(base_ref: str, version: str, pr_body: str) -> None:
    changed = set(git_output("diff", "--name-only", "--relative", f"{base_ref}...HEAD").splitlines())
    if not any(path.startswith(tuple(f"{name}/" for name in SKILL_NAMES)) for path in changed):
        return
    if RELEASE_OPT_OUT.search(pr_body):
        print("skill content changed without a release: the pull request states 'skills-release: no bump needed'")
        return
    try:
        base_version = git_output("show", f"{base_ref}:./VERSION").strip()
    except subprocess.CalledProcessError:
        print("no skills VERSION at the base; skipping the release metadata check")
        return
    missing = {"VERSION", "CHANGELOG.md"} - changed
    if missing:
        fail(
            f"skill changes must update {', '.join(sorted(missing))}, or the pull request description "
            "must contain a line 'skills-release: no bump needed: <reason>'"
        )
    if version_tuple(version) <= version_tuple(base_version):
        fail(f"VERSION must increase beyond {base_version}")


def external_url_problem(url: str) -> str | None:
    """Returns why an external URL is not allowed in skill content, or None when it is allowed.

    Skills link to the latest Developer reference, to runnable Dex examples on main, and to
    the narrow exceptions in ALLOWED_EXTERNAL_URL_EXCEPTIONS.
    """
    parts = urlsplit(url)
    host = parts.netloc.lower()
    segments = [segment for segment in parts.path.split("/") if segment]
    if host in LOCAL_HOSTS or host.split(":")[0] in LOCAL_HOSTS:
        return None
    if host == "example.com" or host.endswith(PLACEHOLDER_HOST_SUFFIXES):
        return None
    if host == "docs.superdurable.io":
        if not segments or segments[0] != "reference":
            return "skills link only to the docs Developer reference"
        if len(segments) > 2 and re.fullmatch(r"v?\d+(?:\.\d+)*", segments[2]):
            return "link the latest Developer reference, not a version"
        return None
    if ".." in segments:
        return "links must not contain '..' path segments"
    location = f"{host}{parts.path.rstrip('/')}"
    if DEX_EXAMPLES_URL.fullmatch(location):
        return None
    if any(pattern.fullmatch(location) for pattern, _reason in ALLOWED_EXTERNAL_URL_EXCEPTIONS):
        return None
    if host == "github.com" and len(segments) > 3 and segments[2] in ("blob", "tree") and segments[3] != "main":
        return "GitHub links must use the main branch, not a tag or commit"
    return "skills link only to the Developer reference and to examples on main"


def find_boundary_terms(content: str) -> list[str]:
    return [description for pattern, description in CONCEPT_BOUNDARY_TERMS if pattern.search(content)]


def find_dex_versions(content: str, is_n8n_document: bool = False) -> list[tuple[int, str]]:
    """Returns (line number, matched text) for each Dex version number in the content.

    An n8n document is checked only for Dex release tags and dependency pins.
    """
    patterns = DEX_RELEASE_PATTERNS if is_n8n_document else DEX_RELEASE_PATTERNS + GENERIC_VERSION_PATTERNS
    found = []
    for line_number, line in enumerate(content.splitlines(), start=1):
        text = ALLOWED_VERSION_TEXT.sub(" ", line)
        matches = [match.group(0) for pattern in patterns for match in pattern.finditer(text)]
        if not is_n8n_document and BARE_VERSION_CONTEXT.search(text):
            matches.extend(match.group(0) for match in BARE_DEX_VERSION.finditer(text))
        found.extend((line_number, match) for match in dict.fromkeys(matches))
    return found


def find_markdown_links(content: str) -> list[str]:
    """Returns the Markdown link targets outside fenced blocks and inline code."""
    return MARKDOWN_LINK.findall(INLINE_CODE.sub("", FENCED_BLOCK.sub("", content)))


def markdown_anchors(content: str) -> set[str]:
    """Returns the GitHub-style heading anchors and explicit HTML anchors of one Markdown file."""
    anchors = set(EXPLICIT_ANCHOR.findall(content))
    counts: dict[str, int] = {}
    for heading in HEADING.findall(FENCED_BLOCK.sub("", content)):
        text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", heading)
        slug = re.sub(r"[^\w\- ]", "", text.strip().lower()).replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        anchors.add(slug if count == 0 else f"{slug}-{count}")
    return anchors


def parse_front_matter(path: Path) -> dict[str, str]:
    lines = path.read_text().splitlines()
    if not lines or lines[0] != "---" or "---" not in lines[1:]:
        fail(f"{path.relative_to(ROOT)} must start with YAML front matter")
    front_matter: dict[str, str] = {}
    key = ""
    for line in lines[1:lines.index("---", 1)]:
        if line.startswith((" ", "\t")) and key:
            front_matter[key] = f"{front_matter[key]} {line.strip()}".strip()
            continue
        key, separator, value = line.partition(":")
        key = key.strip() if separator else ""
        if key:
            value = value.strip()
            # A block scalar (> or |) continues on the indented lines that follow.
            front_matter[key] = "" if value[:1] in (">", "|") else value.strip("\"'")
    return front_matter


def load_json(relative_path: Path) -> dict:
    try:
        value = json.loads((ROOT / relative_path).read_text())
    except (OSError, json.JSONDecodeError) as error:
        fail(f"invalid JSON in {relative_path}: {error}")
    if not isinstance(value, dict):
        fail(f"{relative_path} must contain a JSON object")
    return value


def version_tuple(version: str) -> tuple[int, int, int]:
    match = SEMVER.fullmatch(version)
    if match is None:
        fail(f"VERSION must be stable SemVer: {version}")
    return tuple(int(part) for part in match.groups())


def git_output(*arguments: str) -> str:
    return subprocess.run(["git", *arguments], cwd=ROOT, check=True, capture_output=True, text=True).stdout


if __name__ == "__main__":
    main()
