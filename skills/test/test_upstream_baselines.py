#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import importlib.util
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "upstream_baselines", Path(__file__).resolve().parents[1] / "script/check-upstream-baselines.py",
)
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)

def gofmt_tabs(source: str) -> str:
    """Returns the fixture with each leading four-space group replaced by the tab gofmt uses."""
    return re.sub(r"(?m)^((?:    )+)", lambda match: "\t" * (len(match.group(1)) // 4), source)


# Synthetic gofmt-shaped fixture with a decoy function whose name extends another's.
FUNCTION_SOURCE = gofmt_tabs("""package fixture

func (c *Collector) validateWait(
    ctx context.Context,
) error {
    return nil
}

func (c *Collector) validateWaitForMatch(
    ctx context.Context,
) error {
    return c.record(ctx)
}

func (c *Collector) record(ctx context.Context) error {
    return nil
}
""")

SDK_TAGS = {language: f"sdk-{language}/v9.9.9" for language in ("go", "java", "python", "rust", "typescript")}
SDK_SOURCES = {
    "go": {"sdk-go/dex/errors.go": gofmt_tabs(
        "package dex\n\ntype FlowClosingError struct {\n    *ServiceError\n}\n\n"
        "const (\n    ErrorSubStatusFlowClosing ErrorSubStatus = iota\n)\n"
    )},
    "java": {"sdk-java/src/main/java/io/superdurable/dex/exceptions/FlowClosingException.java": (
        "package io.superdurable.dex.exceptions;\n\npublic final class FlowClosingException extends DexServiceException {}\n"
    )},
    "python": {
        "sdk-python/dex/runtime_errors.py": "class FlowClosingError(DexServiceError):\n    pass\n",
        "sdk-python/dex/__init__.py": '__all__ = [\n    "FlowClosingError",\n]\n',
    },
    "rust": {
        "sdk-rust/crates/dex-sdk/src/sdk_error.rs": "pub enum SdkError {\n    FlowClosing {\n        service: ServiceError,\n    },\n}\n",
        "sdk-rust/crates/dex-sdk/src/lib.rs": "pub use sdk_error::{ErrorSubStatus, SdkError, SdkResult};\n",
    },
    "typescript": {
        "sdk-typescript/src/errors.ts": "export class FlowClosingError extends DexServiceError {}\n",
        "sdk-typescript/src/index.ts": 'export * from "./errors.js";\n',
    },
}
SDK_REFERENCE_PAGES = {
    "go": "Match `*dex.FlowClosingError` with `errors.As`. Its sub-status is `dex.ErrorSubStatusFlowClosing`.\n",
    "java": "Catch `FlowClosingException` from `io.superdurable.dex.exceptions`.\n",
    "python": "Catch `FlowClosingError` from `dex`.\n",
    "rust": "Match `SdkError::FlowClosing { .. }`.\n",
    "typescript": "Narrow the caught value with `instanceof FlowClosingError`.\n",
}
# Per language: (tagged source path, released name, renamed name, reference text, misspelled reference text).
SDK_RENAMES = {
    "go": ("sdk-go/dex/errors.go", "type FlowClosingError struct", "type FlowClosedError struct",
           "`*dex.FlowClosingError`", "`*dex.FlowClosedError`"),
    "java": ("sdk-java/src/main/java/io/superdurable/dex/exceptions/FlowClosingException.java",
             "public final class FlowClosingException ", "public final class FlowClosedException ",
             "`FlowClosingException`", "`FlowClosedException`"),
    "python": ("sdk-python/dex/runtime_errors.py", "class FlowClosingError(", "class FlowClosedError(",
               "`FlowClosingError`", "`FlowClosedError`"),
    "rust": ("sdk-rust/crates/dex-sdk/src/sdk_error.rs", "    FlowClosing {", "    FlowClosed {",
             "`SdkError::FlowClosing { .. }`", "`SdkError::FlowClosed { .. }`"),
    "typescript": ("sdk-typescript/src/errors.ts", "export class FlowClosingError ", "export class FlowClosedError ",
                   "`instanceof FlowClosingError`", "`instanceof FlowClosedError`"),
}


def replace_once(source: str, old: str, new: str) -> str:
    if source.count(old) != 1:
        raise AssertionError(f"fixture mutation target occurs {source.count(old)} times: {old!r}")
    return source.replace(old, new)


def replace_in_function(source: str, declaration: str, old: str, new: str) -> str:
    """Returns the source with old replaced inside one fixture function, located without the checker.

    The function ends at the first later line holding only "}" at the declaration's indentation.
    """
    start = source.index(declaration)
    indentation = declaration[: len(declaration) - len(declaration.lstrip())]
    closing = re.compile(rf"\n{re.escape(indentation)}\}}(?=\n|$)")
    match = closing.search(source, start)
    if match is None:
        raise AssertionError(f"fixture function has no closing brace: {declaration!r}")
    function = source[start : match.end()]
    return source[:start] + replace_once(function, old, new) + source[match.end() :]


def tagged_sources(tag: str, *paths: str) -> dict[str, str]:
    """Returns released files read from this repository's tag, used to seed fixture tags."""
    source_root = Path(__file__).resolve().parents[2]
    return {path: CHECKER.git(source_root, "show", f"refs/tags/{tag}:{path}") for path in paths}


def copy_references(destination: Path, *pages: str) -> Path:
    """Copies the real reference pages under destination/references and returns that directory."""
    references = destination / "references"
    for page in pages:
        target = references / page
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text((CHECKER.SDK_REFERENCES / page).read_text())
    return references


def mutate_page(test: unittest.TestCase, check, page: Path, old: str, new: str) -> None:
    """Asserts the check rejects one reference-page edit, then restores the page."""
    original = page.read_text()
    page.write_text(replace_once(original, old, new))
    try:
        with test.assertRaises(SystemExit) as raised:
            check()
        test.assertIn(str(page), str(raised.exception))
    finally:
        page.write_text(original)


class FixtureRepository:
    def __init__(self, test: unittest.TestCase):
        temporary = tempfile.TemporaryDirectory()
        test.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.git("init", "--quiet")
        self.git("config", "user.name", "Baseline validator fixture")
        self.git("config", "user.email", "fixture@example.invalid")

    def git(self, *arguments: str) -> None:
        subprocess.run(["git", *arguments], cwd=self.root, check=True, capture_output=True, text=True)

    def commit_and_tag(self, tag: str, files: dict[str, str]) -> None:
        for path, content in files.items():
            source = self.root / path
            source.parent.mkdir(parents=True, exist_ok=True)
            source.write_text(content)
        self.git("add", ".")
        self.git("commit", "--quiet", "--allow-empty", "-m", f"fixture for {tag}")
        self.git("tag", "--force", tag)


class GoFunctionSourceTest(unittest.TestCase):
    def test_returns_only_the_declared_function(self):
        function = CHECKER.go_function_source(
            FUNCTION_SOURCE, "func (c *Collector) validateWaitForMatch(", "fixture",
        )
        self.assertTrue(function.startswith("func (c *Collector) validateWaitForMatch(\n"))
        self.assertTrue(function.endswith("\n}"))
        self.assertEqual(1, function.count("c.record("))
        self.assertNotIn("return nil\n}\n\nfunc", function)

    def test_missing_declaration_names_location_and_declaration(self):
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(FUNCTION_SOURCE, "func (c *Collector) validateMissing(", "tag:path.go")
        self.assertIn("tag:path.go has no function declared as `func (c *Collector) validateMissing(`", str(raised.exception))

    def test_declaration_does_not_match_a_longer_function_name(self):
        renamed = FUNCTION_SOURCE.replace("validateWait(", "validateWaitAll(")
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(renamed, "func (c *Collector) validateWait(", "fixture")
        self.assertIn("has no function declared", str(raised.exception))

    def test_duplicate_declaration_fails(self):
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(FUNCTION_SOURCE + FUNCTION_SOURCE, "func (c *Collector) record(", "fixture")
        self.assertIn("declares `func (c *Collector) record(` 2 times", str(raised.exception))

    def test_function_without_closing_brace_before_next_declaration_fails(self):
        source = "func Broken() error {\n\treturn nil\ntype After struct {\n}\n"
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(source, "func Broken(", "fixture")
        self.assertIn("has no column-0 closing brace", str(raised.exception))


class SdkFlowClosingErrorsTest(unittest.TestCase):
    def setUp(self):
        self.repository = FixtureRepository(self)
        for language, files in SDK_SOURCES.items():
            self.repository.commit_and_tag(SDK_TAGS[language], files)
        self.references = self.repository.root / "references"
        for language, page in SDK_REFERENCE_PAGES.items():
            path = self.references / language / "error-handling.md"
            path.parent.mkdir(parents=True)
            path.write_text(page)

    def check(self):
        CHECKER.check_sdk_flow_closing_errors(self.repository.root, SDK_TAGS, self.references)

    def test_released_names_match_reference_pages(self):
        self.check()

    def test_reference_page_must_use_the_released_name(self):
        for language, (_, _, _, reference, misspelled) in SDK_RENAMES.items():
            with self.subTest(language=language):
                page = self.references / language / "error-handling.md"
                page.write_text(replace_once(SDK_REFERENCE_PAGES[language], reference, misspelled))
                with self.assertRaises(SystemExit) as raised:
                    self.check()
                self.assertIn(f"{page} is missing required text", str(raised.exception))
                page.write_text(SDK_REFERENCE_PAGES[language])

    def test_released_source_must_define_the_documented_name(self):
        for language, (path, released, renamed, _, _) in SDK_RENAMES.items():
            with self.subTest(language=language):
                tag = SDK_TAGS[language]
                self.repository.commit_and_tag(tag, {path: replace_once(SDK_SOURCES[language][path], released, renamed)})
                with self.assertRaises(SystemExit) as raised:
                    self.check()
                self.assertIn(f"{tag}:{path} is missing required text", str(raised.exception))
                self.repository.commit_and_tag(tag, {path: SDK_SOURCES[language][path]})


class GoDescribeFlowTest(unittest.TestCase):
    TAG = "sdk-go/v9.9.9"
    CLIENT_PATH = "sdk-go/dex/client.go"
    MAPPER_PATH = "sdk-go/dex/proto_mapper.go"
    ERRORS_PATH = "sdk-go/dex/errors.go"
    README_PATH = "sdk-go/README.md"
    DOC_PATH = "docs/content/primitives/client/advanced.mdx"

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.files = tagged_sources(
            "sdk-go/v1.24.0", self.CLIENT_PATH, self.MAPPER_PATH, self.ERRORS_PATH, self.README_PATH, self.DOC_PATH,
        )
        self.repository.commit_and_tag(self.TAG, self.files)
        self.references = copy_references(
            self.repository.root, "go/observability.md", "go/error-handling.md", "core/error-handling.md",
        )

    def check(self):
        CHECKER.check_go_describe_flow(self.repository.root, self.TAG, self.references)

    def assert_mutation_fails(self, path, declaration, old, new):
        mutated = replace_in_function(self.files[path], declaration, old, new) if declaration else replace_once(self.files[path], old, new)
        self.repository.commit_and_tag(self.TAG, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{self.TAG}:{path}", str(raised.exception))

    def assert_reference_mutation_fails(self, page, old, new):
        mutate_page(self, self.check, self.references / page, old, new)

    def test_released_api_and_reference_pass(self):
        self.check()

    def test_describe_must_call_summary_rpc_with_flow_id(self):
        self.assert_mutation_fails(
            self.CLIENT_PATH, "func (client *Client) DescribeFlow(",
            "GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})",
            "GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{})",
        )

    def test_client_validation_must_reject_nil_context(self):
        self.assert_mutation_fails(
            self.CLIENT_PATH, "func (client *Client) validateCall(",
            "if ctx == nil {", "if false {",
        )

    def test_client_validation_must_read_closed_state(self):
        self.assert_mutation_fails(
            self.CLIENT_PATH, "func (client *Client) validateCall(",
            "closed := client.closed", "closed := false",
        )

    def test_client_validation_must_return_context_error(self):
        self.assert_mutation_fails(
            self.CLIENT_PATH, "func (client *Client) validateCall(",
            "if err := ctx.Err(); err != nil {\n\t\treturn err", "if err := ctx.Err(); err != nil {\n\t\treturn nil",
        )

    def test_mapper_must_preserve_close_time(self):
        self.assert_mutation_fails(
            self.MAPPER_PATH, "func mapFlowInfo(",
            "closedAt = response.GetCloseTime().AsTime()", "closedAt = time.Time{}",
        )

    def test_missing_flow_must_map_to_typed_error(self):
        self.assert_mutation_fails(
            self.ERRORS_PATH, "func translateRPCError(",
            "return &FlowNotFoundError{ServiceError: serviceError}",
            "return serviceError",
        )

    def test_flow_not_exists_sub_status_must_map_to_flow_not_found(self):
        self.assert_mutation_fails(
            self.ERRORS_PATH, "func mapErrorSubStatus(",
            "case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS:\n\t\treturn ErrorSubStatusFlowNotFound",
            "case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS:\n\t\treturn ErrorSubStatusUncategorized",
        )

    def test_time_travel_must_return_the_new_run(self):
        self.assert_mutation_fails(
            self.CLIENT_PATH, None,
            "The returned run ID identifies the new execution.", "The returned run ID is the original run ID.",
        )

    def test_reference_must_name_released_method(self):
        page = self.references / "go" / "observability.md"
        page.write_text(page.read_text().replace("DescribeFlow", "DescribeRun"))
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(str(page), str(raised.exception))

    def test_reference_signature_must_match_release(self):
        self.assert_reference_mutation_fails(
            "go/observability.md", "DescribeFlow(ctx context.Context, flowID string)",
            "DescribeFlow(ctx context.Context, flowID string, runID string)",
        )

    def test_reference_must_keep_closed_at_semantics(self):
        self.assert_reference_mutation_fails(
            "go/observability.md",
            "A zero `ClosedAt` means the run is open or its close time is unavailable.",
            "A zero `ClosedAt` means the run closed without a recorded time.",
        )

    def test_reference_must_distinguish_time_travel_run_identity(self):
        self.assert_reference_mutation_fails(
            "go/observability.md", "server-zd keeps the run ID across time travel, so `RunID` matches the run ID `StartFlow` returned.",
            "server-zd creates a new run ID after time travel, so `RunID` differs from the run ID `StartFlow` returned.",
        )

    def test_reference_must_name_expired_context(self):
        self.assert_reference_mutation_fails(
            "go/observability.md", "a nil, canceled, or expired context", "a nil or canceled context",
        )

    def test_core_reference_must_cite_go_release(self):
        self.assert_reference_mutation_fails(
            "core/error-handling.md", "(from Go SDK v1.21.0)", "(from Go SDK v1.22.0)",
        )

    def test_core_reference_must_scope_close_time_to_go(self):
        self.assert_reference_mutation_fails(
            "core/error-handling.md", "Only the Go result also carries the close time, when available.",
            "Every result also carries the close time, when available.",
        )

    def test_core_status_read_must_keep_its_lead_in(self):
        self.assert_reference_mutation_fails(
            "core/error-handling.md",
            "For that status read, or another one-off status check of a known Flow ID, call the SDK's DescribeFlow API:",
            "For that status read, use a typed RPC:",
        )

    def test_core_status_read_must_stay_under_closed_flow_races(self):
        page = self.references / "core" / "error-handling.md"
        lines = page.read_text().split("\n")
        paragraph = next(line for line in lines if line.startswith("For that status read,"))
        lines.remove(paragraph)
        heading = lines.index("### Terminal read RPC rule")
        lines[heading + 1 : heading + 1] = ["", paragraph]
        page.write_text("\n".join(lines))
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn("section `## Closed-Flow races` is missing required text", str(raised.exception))

    def test_stall_checklist_must_stay_outside_describe_section(self):
        page = self.references / "go" / "observability.md"
        content = page.read_text()
        stall_start = content.index("For a stall:")
        describe_heading = "## Describe a Flow\n\n"
        heading_start = content.index(describe_heading)
        page.write_text(
            content[:stall_start] + describe_heading + content[stall_start:heading_start]
            + content[heading_start + len(describe_heading):]
        )
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn("section `## Correlation and signals` is missing required text: For a stall:", str(raised.exception))

    def test_completed_status_constant_must_match_release(self):
        self.assert_mutation_fails(
            self.CLIENT_PATH, None,
            "// FlowCompleted means the Flow completed successfully.\n\tFlowCompleted\n",
            "// FlowCompleted means the Flow completed successfully.\n\tFlowSucceeded\n",
        )

    def test_reference_completed_rule_must_not_widen(self):
        self.assert_reference_mutation_fails(
            "go/error-handling.md", "Treat `dex.FlowCompleted` as", "Treat `dex.FlowCompleted` or `dex.FlowCanceled` as",
        )

    def test_reference_must_list_every_flow_info_field(self):
        self.assert_reference_mutation_fails(
            "go/observability.md", "`RunID string` (server-assigned run ID), ", "",
        )

    def test_tagged_readme_must_keep_status_read_boundary(self):
        self.assert_mutation_fails(
            self.README_PATH, None,
            "Use `WaitForFlow` when the caller needs to wait",
            "Use `DescribeFlow` when the caller needs to wait",
        )


class SdkDescribeFlowTest(unittest.TestCase):
    JAVA_ROOT = "sdk-java/src/main/java/io/superdurable/dex"
    RUST_ROOT = "sdk-rust/crates/dex-sdk/src"
    PATHS = {
        "java": (f"{JAVA_ROOT}/Client.java", f"{JAVA_ROOT}/FlowInfo.java",
                 f"{JAVA_ROOT}/GrpcExceptionTranslator.java", f"{JAVA_ROOT}/FlowStatus.java"),
        "python": ("sdk-python/dex/client.py", "sdk-python/dex/async_client.py",
                   "sdk-python/dex/flow_info.py", "sdk-python/dex/_grpc_errors.py"),
        "typescript": ("sdk-typescript/src/client.ts", "sdk-typescript/src/options.ts", "sdk-typescript/src/grpc-status.ts"),
        "rust": (f"{RUST_ROOT}/client.rs", f"{RUST_ROOT}/flow_info.rs", f"{RUST_ROOT}/sdk_error.rs"),
    }
    # Per language: (path, declaration or None, released text, mutated text) for the describe call.
    CALL_RENAMES = {
        "java": (f"{JAVA_ROOT}/Client.java", None,
                 "public FlowInfo describeFlow(final String flowId) {", "public FlowInfo describeRun(final String flowId) {"),
        "python": ("sdk-python/dex/async_client.py", None,
                   "    async def describe_flow(self, flow_id: str) -> FlowInfo:", "    async def describe_run(self, flow_id: str) -> FlowInfo:"),
        "typescript": ("sdk-typescript/src/client.ts", "  public async describeFlow(flowId: string): Promise<FlowInfo> {",
                       'runId: "" }', "runId }"),
        "rust": (f"{RUST_ROOT}/client.rs", "    pub fn describe_flow(&self, flow_id: &str) -> SdkResult<FlowInfo> {",
                 "run_id: String::new(),", "run_id: run_id.to_string(),"),
    }
    # Per language: (path, declaration or None, released text, text that adds a close time).
    CLOSE_TIME_ADDITIONS = {
        "java": (f"{JAVA_ROOT}/FlowInfo.java", None,
                 "    private final Instant startedAt;\n", "    private final Instant startedAt;\n    private final Instant closedAt;\n"),
        "python": ("sdk-python/dex/flow_info.py", None,
                   "    started_at: datetime\n\n\n@dataclass(frozen=True)\nclass HealthInfo:",
                   "    started_at: datetime\n    closed_at: datetime | None\n\n\n@dataclass(frozen=True)\nclass HealthInfo:"),
        "typescript": ("sdk-typescript/src/options.ts", "export interface FlowInfo {",
                       "  readonly startedAt: Date;\n", "  readonly startedAt: Date;\n  readonly closedAt: Date | undefined;\n"),
        "rust": (f"{RUST_ROOT}/flow_info.rs", "pub struct FlowInfo {",
                 "    pub started_at: SystemTime,\n", "    pub started_at: SystemTime,\n    pub closed_at: Option<SystemTime>,\n"),
    }
    # Per language: (path, released not-found mapping, broken mapping).
    NOT_FOUND_MAPPINGS = {
        "java": (f"{JAVA_ROOT}/GrpcExceptionTranslator.java",
                 "            return new FlowNotFoundException(code, detail, cause);",
                 "            return new DexServiceException(code, detail, cause);"),
        "python": ("sdk-python/dex/_grpc_errors.py",
                   '        if requirement == "existing":\n            return FlowNotFoundError(*parameters)',
                   '        if requirement == "existing":\n            return DexServiceError(*parameters)'),
        "typescript": ("sdk-typescript/src/grpc-status.ts",
                       '      if (requirement === "existing") {\n        return new FlowNotFoundError(',
                       '      if (requirement === "existing") {\n        return new DexServiceError('),
        "rust": (f"{RUST_ROOT}/sdk_error.rs",
                 "FlowTargetRequirement::Existing => Self::FlowNotFound { service },",
                 "FlowTargetRequirement::Existing => Self::Service { service },"),
    }
    # Per language: (reference text, misspelled reference text).
    REFERENCE_RENAMES = {
        "java": ("`Client.describeFlow(flowId)`", "`Client.describeRun(flowId)`"),
        "python": ("`await AsyncClient.describe_flow(flow_id)`", "`AsyncClient.describe_flow(flow_id)`"),
        "typescript": ("`await client.describeFlow(flowId)`", "`await client.describe(flowId)`"),
        "rust": ("`client.describe_flow(flow_id)`", "`client.describe_flow(flow_id).await`"),
    }

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.tags = {language: f"sdk-{language}/v9.9.9" for language in ("go", *self.PATHS)}
        self.sources = {}
        for language, paths in self.PATHS.items():
            self.sources[language] = tagged_sources(f"sdk-{language}/v1.23.0", *paths)
            self.repository.commit_and_tag(self.tags[language], self.sources[language])
        self.references = copy_references(
            self.repository.root, "core/error-handling.md",
            *(f"{language}/error-handling.md" for language in self.PATHS),
        )

    def check(self):
        CHECKER.check_sdk_describe_flow(self.repository.root, self.tags, self.references)

    def assert_source_mutation_fails(self, language, path, declaration, old, new):
        source = self.sources[language][path]
        mutated = replace_in_function(source, declaration, old, new) if declaration else replace_once(source, old, new)
        tag = self.tags[language]
        self.repository.commit_and_tag(tag, {path: mutated})
        try:
            with self.assertRaises(SystemExit) as raised:
                self.check()
            self.assertIn(f"{tag}:{path}", str(raised.exception))
        finally:
            self.repository.commit_and_tag(tag, {path: source})

    def test_released_calls_and_references_pass(self):
        self.check()

    def test_describe_call_must_match_release(self):
        for language, (path, declaration, old, new) in self.CALL_RENAMES.items():
            with self.subTest(language=language):
                self.assert_source_mutation_fails(language, path, declaration, old, new)

    def test_flow_info_must_have_no_close_time(self):
        for language, (path, declaration, old, new) in self.CLOSE_TIME_ADDITIONS.items():
            with self.subTest(language=language):
                self.assert_source_mutation_fails(language, path, declaration, old, new)

    def test_missing_flow_must_map_to_typed_not_found(self):
        for language, (path, old, new) in self.NOT_FOUND_MAPPINGS.items():
            with self.subTest(language=language):
                self.assert_source_mutation_fails(language, path, None, old, new)

    def test_reference_must_use_released_call(self):
        for language, (old, new) in self.REFERENCE_RENAMES.items():
            with self.subTest(language=language):
                mutate_page(self, self.check, self.references / language / "error-handling.md", old, new)

    def test_reference_must_say_no_close_time(self):
        for language in self.PATHS:
            with self.subTest(language=language):
                mutate_page(
                    self, self.check, self.references / language / "error-handling.md",
                    "; it has no close time.", ", and its close time.",
                )

    def test_completed_status_must_match_release(self):
        mutations = {
            "java": (f"{self.JAVA_ROOT}/FlowStatus.java", None, "    COMPLETED,", "    SUCCEEDED,"),
            "python": ("sdk-python/dex/flow_info.py", None, 'COMPLETED = "completed"', 'COMPLETED = "succeeded"'),
            "typescript": ("sdk-typescript/src/options.ts", None, '  | "completed"', '  | "succeeded"'),
            "rust": (f"{self.RUST_ROOT}/flow_info.rs", "pub enum FlowStatus {", "    Completed,", "    Succeeded,"),
        }
        for language, (path, declaration, old, new) in mutations.items():
            with self.subTest(language=language):
                self.assert_source_mutation_fails(language, path, declaration, old, new)

    def test_rust_describe_flow_must_block(self):
        self.assert_source_mutation_fails(
            "rust", f"{self.RUST_ROOT}/client.rs", "    pub fn describe_flow(&self, flow_id: &str) -> SdkResult<FlowInfo> {",
            "self.runtime.block_on(", "futures::executor::block_on(",
        )

    def test_typescript_incomplete_summary_must_throw_type_error(self):
        self.assert_source_mutation_fails(
            "typescript", "sdk-typescript/src/client.ts", "  public async describeFlow(flowId: string): Promise<FlowInfo> {",
            "throw new TypeError(", "throw new Error(",
        )

    def test_reference_completed_rule_must_not_widen(self):
        widened = {
            "java": ("Treat `COMPLETED` as", "Treat `COMPLETED` or `CANCELED` as"),
            "python": ("Treat `FlowStatus.COMPLETED` as", "Treat `FlowStatus.COMPLETED` or `FlowStatus.CANCELED` as"),
            "typescript": ('Treat status `"completed"` as', 'Treat status `"completed"` or `"cancelled"` as'),
            "rust": ("Treat `FlowStatus::Completed` as", "Treat `FlowStatus::Completed` or `FlowStatus::Canceled` as"),
        }
        for language, (old, new) in widened.items():
            with self.subTest(language=language):
                mutate_page(self, self.check, self.references / language / "error-handling.md", old, new)

    def test_reference_must_list_every_flow_info_field(self):
        dropped = {
            "java": "`getRunId()`, ",
            "python": "`run_id`, ",
            "typescript": "`runId`, ",
            "rust": "`run_id`, ",
        }
        for language, field in dropped.items():
            with self.subTest(language=language):
                page = self.references / language / "error-handling.md"
                sentence = next(line for line in page.read_text().split(". ") if "it has no close time" in line)
                mutate_page(self, self.check, page, sentence, sentence.replace(field, "", 1))

    def test_core_reference_must_name_every_sdk_call(self):
        mutate_page(
            self, self.check, self.references / "core" / "error-handling.md",
            "TypeScript **Client.describeFlow**", "TypeScript **Client.describe**",
        )

    def test_core_reference_must_not_present_describe_as_go_only(self):
        mutate_page(
            self, self.check, self.references / "core" / "error-handling.md",
            "Java, Python, TypeScript, and Rust already have it at their v1.26.0 baselines.",
            "Only the Go SDK has it.",
        )


class ServerZdSearchTlsTest(unittest.TestCase):
    TAG = "server-zd/v9.9.9"
    CONNECTION_PATH = CHECKER.POSTGRES_CONNECTION_PATH
    TLS_PATH = CHECKER.POSTGRES_TLS_PATH
    DSN_PATH = CHECKER.POSTGRES_DSN_PATH
    BACKEND_PATH = CHECKER.SEARCH_BACKEND_PATH
    PROJECTION_PATH = CHECKER.SEARCH_PROJECTION_PATH
    CARGO_LOCK_PATH = "server-zd/Cargo.lock"

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.files = tagged_sources(
            "server-zd/v0.1.6",
            "server-zd/README.md",
            "docs/content/production/server-zd-operations.mdx",
            self.CARGO_LOCK_PATH,
            self.CONNECTION_PATH,
            self.TLS_PATH,
            self.DSN_PATH,
            self.BACKEND_PATH,
            self.PROJECTION_PATH,
        )
        self.repository.commit_and_tag(self.TAG, self.files)
        self.references = copy_references(self.repository.root, "core/operations.md")

    def check(self):
        CHECKER.check_server_zd_search_tls(self.repository.root, self.TAG, self.references)

    def assert_mutation_fails(self, path, old, new, declaration=None):
        source = self.files[path]
        mutated = replace_in_function(source, declaration, old, new) if declaration else replace_once(source, old, new)
        self.repository.commit_and_tag(self.TAG, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{self.TAG}:{path}", str(raised.exception))

    def assert_reference_mutation_fails(self, old, new):
        mutate_page(self, self.check, self.references / "core" / "operations.md", old, new)

    def test_released_tls_source_and_reference_pass(self):
        self.check()

    def test_search_configuration_must_use_tls_parser(self):
        self.assert_mutation_fails(
            self.PROJECTION_PATH,
            "ConnectionSettings::parse_for_search(", "ConnectionSettings::parse(",
        )

    def test_search_default_must_remain_plaintext(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            "Self::from_parsed_dsn(remaining, parameters, MissingSslMode::Disable)",
            "Self::from_parsed_dsn(remaining, parameters, MissingSslMode::Prefer)",
        )

    def test_direct_negotiation_must_reject_allow(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            'Some("direct") && ssl_mode == "allow"', 'Some("direct") && ssl_mode == "disable"',
        )

    def test_tls_parameters_must_be_extracted_from_dsn(self):
        self.assert_mutation_fails(
            self.DSN_PATH,
            '"sslrootcert" => tls.ssl_root_cert = Some(parameter.value)',
            '"sslrootcert" => kept.push(raw)',
        )

    def test_system_roots_must_load_native_certificates(self):
        self.assert_mutation_fails(
            self.TLS_PATH,
            "rustls_native_certs::load_native_certs()", "Default::default()",
        )

    def test_full_verification_must_use_root_certificates(self):
        self.assert_mutation_fails(
            self.TLS_PATH,
            "builder.with_root_certificates(roots).with_no_client_auth()",
            "builder.dangerous().with_custom_certificate_verifier(verifier).with_no_client_auth()",
        )

    def test_require_without_roots_must_not_verify_certificate(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            'None => Ok((SslMode::Require, Some(CertificateVerification::None)))',
            'None => Ok((SslMode::Require, Some(CertificateVerification::Full(roots))))',
        )

    def test_verify_full_must_check_host_name(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            '"verify-full" => {', '"verify-full-removed" => {',
        )

    def test_timeout_must_bound_connection(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            "tokio::time::timeout(timeout, connecting)", "connecting.await",
        )

    def test_default_timeout_must_remain_ten_seconds(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            "const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);",
            "const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(30);",
        )

    def test_sni_setting_must_reach_tls_client(self):
        self.assert_mutation_fails(
            self.TLS_PATH,
            "config.enable_sni = is_sni_enabled", "config.enable_sni = false",
        )

    def test_connection_errors_must_preserve_causes(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            "source = cause.source()", "source = None",
        )

    def test_connection_error_test_must_reject_dsn_echo(self):
        self.assert_mutation_fails(
            self.BACKEND_PATH,
            'assert!(!message.contains(&dsn), "{message}");',
            'assert!(message.contains(&dsn), "{message}");',
        )

    def test_chain_only_verifier_must_reject_untrusted_chains(self):
        self.assert_mutation_fails(
            self.TLS_PATH, "            result => result,", "            _ => Ok(ServerCertVerified::assertion()),",
            declaration="impl ServerCertVerifier for ChainOnlyVerifier {",
        )

    def test_chain_only_verifier_must_consult_webpki(self):
        self.assert_mutation_fails(
            self.TLS_PATH, "match self.inner.verify_server_cert(", "match Ok::<_, Error>(()).map(|_| (",
            declaration="impl ServerCertVerifier for ChainOnlyVerifier {",
        )

    def test_reconnect_must_reuse_tls_settings(self):
        self.assert_mutation_fails(
            self.BACKEND_PATH, "let replacement = Self::open(configuration).await?;",
            "let replacement = Self::open(&ConnectionSettings::from_config(configuration.configuration().clone())).await?;",
            declaration="    pub(super) async fn reconnect(",
        )

    def test_backend_must_keep_tls_settings_for_reconnects(self):
        self.assert_mutation_fails(
            self.BACKEND_PATH, "configuration: configuration.clone(),",
            "configuration: ConnectionSettings::from_config(configuration.configuration().clone()),",
            declaration="    pub async fn connect_with_settings(",
        )

    def test_search_projection_must_connect_with_tls_settings(self):
        self.assert_mutation_fails(
            self.PROJECTION_PATH,
            "ParadeDbSearchBackend::connect_with_settings(\n                &configuration,",
            "ParadeDbSearchBackend::connect(\n                configuration.configuration(),",
            declaration="    pub(crate) async fn connect(search: &SearchConfig)",
        )

    def test_missing_ssl_mode_with_tls_parameter_must_select_prefer(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH, '{\n                "prefer".to_owned()\n            }',
            '{\n                "verify-full".to_owned()\n            }',
            declaration="fn resolve_tls(",
        )

    def test_direct_negotiation_must_upgrade_prefer_to_require(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH, '        ssl_mode = "require".to_owned();', '        ssl_mode = "prefer".to_owned();',
            declaration="fn resolve_tls(",
        )

    def test_prefer_fallback_dependency_must_match_release(self):
        self.assert_mutation_fails(
            self.CARGO_LOCK_PATH, 'name = "tokio-postgres"\nversion = "0.7.18"', 'name = "tokio-postgres"\nversion = "0.7.19"',
        )

    def test_invalid_keyword_error_must_not_print_connection_string(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            '|keyword| format!("connection string keyword {keyword} has an invalid value"),',
            '|keyword| format!("connection string keyword {keyword} has an invalid value: {error:?}"),',
            declaration="fn describe_config_parse_error(",
        )

    def test_malformed_error_must_not_print_connection_string(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            '|| "connection string has an unsupported keyword or is malformed".to_owned(),',
            '|| format!("connection string has an unsupported keyword or is malformed: {error}"),',
            declaration="fn describe_config_parse_error(",
        )

    def test_parse_errors_must_use_fixed_text(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            "ConnectionSettingsError::InvalidDsn(describe_config_parse_error(&error))",
            "ConnectionSettingsError::InvalidDsn(error.to_string())",
            declaration="    fn from_parsed_dsn(",
        )

    def test_tagged_readme_must_require_postgresql_17_for_direct(self):
        self.assert_mutation_fails(
            "server-zd/README.md", "  requires PostgreSQL 17 or later.", "  requires PostgreSQL 16 or later.",
        )

    def test_system_roots_must_force_verify_full(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH, '                ssl_mode = "verify-full".to_owned();', '                ssl_mode = "require".to_owned();',
            declaration="fn resolve_tls(",
        )

    def test_root_certificate_file_must_load_in_every_mode(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH, "Some(load_root_certificates(ssl_root_cert)?)", "load_root_certificates(ssl_root_cert).ok()",
            declaration="fn resolve_tls(",
        )

    def test_parsing_must_resolve_tls_from_the_connection_string(self):
        self.assert_mutation_fails(
            self.CONNECTION_PATH,
            "let (ssl_mode, verification) = resolve_tls(&parameters, missing_ssl_mode)",
            "let (ssl_mode, verification) = resolve_tls(&TlsParameters::default(), missing_ssl_mode)",
            declaration="    fn from_parsed_dsn(",
        )

    def test_direct_negotiation_must_offer_postgresql_alpn(self):
        self.assert_mutation_fails(
            self.TLS_PATH, 'config.alpn_protocols = vec![b"postgresql".to_vec()];', "config.alpn_protocols = Vec::new();",
            declaration="pub(crate) fn client_config(",
        )

    def test_reference_missing_ssl_mode_must_exclude_system_roots(self):
        self.assert_reference_mutation_fails(
            "any other TLS parameter (an `sslrootcert` file, `sslsni`, or `sslnegotiation=postgres`) selects `prefer`",
            "any other TLS parameter (`sslrootcert`, `sslsni`, or `sslnegotiation`) selects `prefer`",
        )
        self.assert_reference_mutation_fails("`sslrootcert=system` instead forces verify-full. ", "")

    def test_reference_must_say_prefer_ignores_loaded_roots(self):
        self.assert_reference_mutation_fails(
            "server-zd still reads that file at startup but never uses it to verify.",
            "server-zd uses that file to verify the certificate chain.",
        )

    def test_reference_must_list_accepted_parameters(self):
        self.assert_reference_mutation_fails("`sslsni=0|1`, and", "`sslsni=0|1|2`, and")

    def test_reference_must_name_new_release(self):
        self.assert_reference_mutation_fails("From server-zd v0.1.6,", "From server-zd v0.1.5,")

    def test_reference_must_keep_default_timeout(self):
        self.assert_reference_mutation_fails("the connection timeout is ten seconds", "the connection timeout is thirty seconds")

    def test_reference_must_keep_plaintext_default(self):
        self.assert_reference_mutation_fails(
            "A string without TLS parameters stays plaintext.", "A string without TLS parameters tries TLS first.",
        )

    def test_reference_require_with_roots_must_skip_host_name(self):
        self.assert_reference_mutation_fails(
            "with a PEM `sslrootcert` file it verifies the certificate chain but not the host name",
            "with a PEM `sslrootcert` file it verifies the certificate chain and the host name",
        )

    def test_reference_must_warn_about_missing_ssl_mode(self):
        self.assert_reference_mutation_fails(
            "even when `sslrootcert` names a CA file.", "and verifies the certificate when `sslrootcert` names a CA file.",
        )

    def test_reference_must_explain_allow(self):
        self.assert_reference_mutation_fails(
            "`sslmode=allow` behaves like `prefer` and tries TLS first",
            "`sslmode=allow` tries plaintext first, like libpq,",
        )

    def test_reference_must_recommend_verify_full(self):
        self.assert_reference_mutation_fails(
            "For a verified connection, use `sslmode=verify-full` with `sslrootcert`",
            "For a verified connection, use `sslmode=require` with `sslrootcert`",
        )


class FlowSummaryRpcTest(unittest.TestCase):
    SERVER_ZD_TAG = "server-zd/v9.9.9"
    ZD_SERVICE_PATH = "server-zd/crates/dexzd-api/src/flow_service.rs"
    ZD_OPERATIONS_PATH = "server-zd/crates/dexzd-api/src/runtime_flow_operations.rs"
    ZD_READ_PATH = CHECKER.SERVER_ZD_READ_OPERATIONS_PATH
    ZD_STATE_ROWS_PATH = CHECKER.SERVER_ZD_STATE_ROWS_PATH
    ZD_RUNTIME_ERROR_PATH = CHECKER.SERVER_ZD_RUNTIME_ERROR_PATH
    ZD_TIME_TRAVEL_PATH = CHECKER.SERVER_ZD_TIME_TRAVEL_TEST_PATH

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.zd_sources = tagged_sources(
            "server-zd/v0.1.6", self.ZD_SERVICE_PATH, self.ZD_OPERATIONS_PATH, self.ZD_READ_PATH,
            self.ZD_STATE_ROWS_PATH, self.ZD_RUNTIME_ERROR_PATH, self.ZD_TIME_TRAVEL_PATH,
        )
        self.repository.commit_and_tag(self.SERVER_ZD_TAG, self.zd_sources)

    def check(self):
        CHECKER.check_flow_summary_rpc(self.repository.root, self.SERVER_ZD_TAG)

    def assert_mutation_fails(self, tag, sources, path, declaration, old, new):
        source = sources[path]
        mutated = replace_in_function(source, declaration, old, new) if declaration else replace_once(source, old, new)
        self.repository.commit_and_tag(tag, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{tag}:{path}", str(raised.exception))

    def assert_server_zd_mutation_fails(self, path, declaration, old, new):
        self.assert_mutation_fails(self.SERVER_ZD_TAG, self.zd_sources, path, declaration, old, new)

    def test_released_server_zd_rpcs_pass(self):
        self.check()

    def test_server_zd_must_route_summary_rpc(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_SERVICE_PATH, None,
            ".get_flow_summary(TenantId::DEFAULT, request.into_inner())",
            ".wait_for_flow(TenantId::DEFAULT, request.into_inner())",
        )

    def test_server_zd_must_read_latest_run(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_STATE_ROWS_PATH, "pub(crate) fn resolve_command_run(",
            "directory.runs.last().cloned()", "directory.runs.first().cloned()",
        )

    def test_server_zd_missing_run_must_be_not_found(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_STATE_ROWS_PATH, "pub(crate) fn resolve_command_run(",
            "return Err(RuntimeError::flow_not_found(&flow.flow_id));",
            'return Err(RuntimeError::invalid_argument("no run"));',
        )

    def test_server_zd_purging_run_must_be_not_found(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_STATE_ROWS_PATH, "pub(crate) fn read_run(", "&& !record.is_purging =>", "=>",
        )

    def test_server_zd_not_found_must_carry_flow_not_exists(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_RUNTIME_ERROR_PATH, "    pub fn flow_not_found(",
            "ErrorSubStatus::FlowNotExists,", "ErrorSubStatus::Uncategorized,",
        )

    def test_server_zd_summary_must_read_requested_or_latest_run(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_READ_PATH, "    pub async fn get_flow_summary(",
            "&request.run_id)?;", '"")?;',
        )

    def test_server_zd_summary_must_return_close_time(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_READ_PATH, "    pub async fn get_flow_summary(",
            "close_time: (record.closed_at_micros > 0)\n                .then(|| timestamp_from_micros(record.closed_at_micros)),",
            "close_time: None,",
        )

    def test_server_zd_summary_must_return_run_id_and_flow_type(self):
        for old, new in (
            ("run_id: run_id_text.clone(),", "run_id: String::new(),"),
            ("flow_type: record.flow_type,", "flow_type: String::new(),"),
        ):
            with self.subTest(field=old):
                self.repository.commit_and_tag(self.SERVER_ZD_TAG, self.zd_sources)
                self.assert_server_zd_mutation_fails(self.ZD_READ_PATH, "    pub async fn get_flow_summary(", old, new)

    def test_server_zd_time_travel_must_keep_run_id(self):
        self.assert_server_zd_mutation_fails(
            self.ZD_TIME_TRAVEL_PATH, None,
            "ResetFlowResponse {\n            run_id: run_id.clone(),\n            run_attempt: 2,",
            "ResetFlowResponse {\n            run_id: new_run_id.clone(),\n            run_attempt: 2,",
        )


class DexWebTreeTest(unittest.TestCase):
    def setUp(self):
        self.repository = FixtureRepository(self)
        self.repository.commit_and_tag("web/v9.9.9", {"web/README.md": "Dex Web\n", "protos/dex.proto": "syntax\n"})
        self.repository.commit_and_tag("cli-v9.9.9", {})

    def check(self):
        CHECKER.require_same_tree(self.repository.root, "web/v9.9.9", "cli-v9.9.9", ("web", "protos"), "Dex Web baseline")

    def test_identical_web_and_protocol_trees_pass(self):
        self.check()

    def test_changed_web_source_between_the_tags_fails(self):
        self.repository.commit_and_tag("cli-v9.9.9", {"web/README.md": "Changed Dex Web\n"})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn("Dex Web baseline web/v9.9.9 differs from cli-v9.9.9", str(raised.exception))

    def test_missing_web_tag_fails(self):
        with self.assertRaises(SystemExit):
            CHECKER.require_same_tree(self.repository.root, "web/v0.0.0", "cli-v9.9.9", ("web",), "Dex Web baseline")


class ServerZdStatementsTest(unittest.TestCase):
    CITED_TAG = "server-zd/v9.9.1"
    CURRENT_TAG = "server-zd/v9.9.2"
    PATH = "server-zd/README.md"
    STATEMENTS = (("server-zd/v9.9.1", PATH, ("Cited behavior.",)), ("", PATH, ("Current-only behavior.",)))

    def setUp(self):
        self.repository = FixtureRepository(self)

    def check(self):
        CHECKER.check_server_zd_statements(self.repository.root, self.CURRENT_TAG, self.STATEMENTS)

    def tag_readmes(self, cited: str, current: str):
        self.repository.commit_and_tag(self.CITED_TAG, {self.PATH: cited})
        self.repository.commit_and_tag(self.CURRENT_TAG, {self.PATH: current})

    def test_statements_present_at_cited_and_current_releases_pass(self):
        self.tag_readmes("Cited behavior.\n", "Cited behavior.\nCurrent-only behavior.\n")
        self.check()

    def test_statement_missing_at_cited_release_fails(self):
        self.tag_readmes("Older text.\n", "Cited behavior.\nCurrent-only behavior.\n")
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{self.CITED_TAG}:{self.PATH} is missing required text: Cited behavior.", str(raised.exception))

    def test_statement_removed_from_current_release_fails(self):
        self.tag_readmes("Cited behavior.\n", "Current-only behavior.\n")
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{self.CURRENT_TAG}:{self.PATH} is missing required text: Cited behavior.", str(raised.exception))

    def test_missing_cited_release_tag_fails(self):
        self.repository.commit_and_tag(self.CURRENT_TAG, {self.PATH: "Cited behavior.\nCurrent-only behavior.\n"})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"Required release tag {self.CITED_TAG} is unavailable", str(raised.exception))


class ServerZdLatestStatementsTest(unittest.TestCase):
    TAG = "server-zd/v0.1.10"

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.statements = tuple(
            statement for statement in CHECKER.SERVER_ZD_LATEST_STATEMENTS if statement[0] == self.TAG
        )
        self.sources = tagged_sources(self.TAG, *dict.fromkeys(path for _, path, _ in self.statements))
        self.repository.commit_and_tag(self.TAG, self.sources)

    def check(self):
        CHECKER.check_server_zd_statements(self.repository.root, self.TAG, self.statements)

    def test_real_tagged_sources_pass(self):
        self.check()

    def test_each_tagged_statement_must_be_enforced(self):
        for _, path, needles in self.statements:
            for needle in needles:
                with self.subTest(path=path, needle=needle):
                    mutated = replace_once(self.sources[path], needle, needle[:-1] + "?")
                    self.repository.commit_and_tag(self.TAG, {path: mutated})
                    with self.assertRaises(SystemExit) as raised:
                        self.check()
                    self.assertIn(f"{self.TAG}:{path} is missing required text", str(raised.exception))
                    self.repository.commit_and_tag(self.TAG, {path: self.sources[path]})


class Release124ReferencesTest(unittest.TestCase):
    def setUp(self):
        self.references = CHECKER.SDK_REFERENCES

    def check(self):
        CHECKER.check_release_1_24_references(self.references)

    def test_release_statements_pass(self):
        self.check()

    def test_each_release_statement_must_be_enforced(self):
        for page, needles in CHECKER.REFERENCE_1_24_STATEMENTS:
            for needle in needles:
                with self.subTest(page=page, needle=needle):
                    mutate_page(self, self.check, self.references / page, needle, needle[:-1] + "?")


class Release128StatementsTest(unittest.TestCase):
    TAGS = {"python": "sdk-python/v1.28.0", "typescript": "sdk-typescript/v1.28.0"}
    PATHS = {
        "sdk-python/v1.28.0": ("sdk-python/README.md",),
        "sdk-typescript/v1.28.0": ("sdk-typescript/README.md", "sdk-typescript/src/context.ts"),
    }

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.sources = {}
        for tag, paths in self.PATHS.items():
            self.sources[tag] = tagged_sources(tag, *paths)
            self.repository.commit_and_tag(tag, self.sources[tag])

    def check(self):
        CHECKER.check_release_1_28_statements(self.repository.root, self.TAGS)

    def mutate_tag(self, tag: str, path: str, old: str):
        self.repository.commit_and_tag(tag, {path: replace_once(self.sources[tag][path], old, old[:-1] + "?")})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(path, str(raised.exception))

    def test_real_tagged_sources_pass(self):
        self.check()

    def test_each_tagged_statement_must_be_enforced(self):
        for language, path, needles in CHECKER.RELEASE_1_28_TAGGED_STATEMENTS:
            for needle in needles:
                with self.subTest(language=language, needle=needle):
                    self.setUp()
                    self.mutate_tag(self.TAGS[language], path, needle)

    def test_typescript_flow_started_at_must_exist(self):
        self.mutate_tag(self.TAGS["typescript"], "sdk-typescript/src/context.ts", "readonly flowStartedAt: Date;")


class ClientAttributeReadTest(unittest.TestCase):
    TAGS = {
        "go": "sdk-go/v1.29.0",
        "python": "sdk-python/v1.29.0",
        "typescript": "sdk-typescript/v1.29.0",
        "java": "sdk-java/v1.26.0",
        "rust": "sdk-rust/v1.26.0",
    }
    PAGES = tuple(page for page, _ in CHECKER.CLIENT_ATTRIBUTE_READ_REFERENCES)
    SOURCE_ROOT = Path(__file__).resolve().parents[2]
    # Per language: (new file, matching declaration, non-matching declarations).
    ABSENCE_CASES = {
        "java": (
            "sdk-java/src/main/java/io/superdurable/dex/AttributeReader.java",
            "public final class AttributeReader {\n    public AttributeValues getAttributes(\n"
            "            final String flowId, AttributeRead<?>... reads) {\n        return null;\n    }\n}\n",
            ("public final class AttributeReader {\n    public List<String> getAttributes() {\n        return null;\n    }\n}\n",),
        ),
        "rust": (
            "sdk-rust/crates/dex-sdk/src/attribute_reads.rs",
            "impl Client {\n    pub async fn get_attributes(\n        &self,\n        flow_id: &str,\n    ) {}\n}\n",
            ("impl Client {\n    pub(crate) fn get_attributes(&self) {}\n}\n",),
        ),
    }

    def setUp(self):
        self.repository = FixtureRepository(self)
        paths = {}
        for language, path, _ in CHECKER.CLIENT_ATTRIBUTE_READ_TAGGED_STATEMENTS:
            paths.setdefault(language, []).append(path)
        for language, tree, suffix, token, _ in CHECKER.CLIENT_ATTRIBUTE_READ_ABSENT:
            paths.setdefault(language, []).extend(CHECKER.tagged_files_containing(
                self.SOURCE_ROOT, self.TAGS[language], tree, suffix, token,
            ))
        self.sources = {}
        for language, language_paths in paths.items():
            tag = self.TAGS[language]
            self.sources[tag] = tagged_sources(tag, *language_paths)
            self.repository.commit_and_tag(tag, self.sources[tag])
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.references = copy_references(Path(temporary.name), *self.PAGES)

    def check(self):
        CHECKER.check_client_attribute_reads(self.repository.root, self.TAGS, self.references)

    def mutate_tag(self, tag: str, path: str, old: str, new: str):
        self.repository.commit_and_tag(tag, {path: replace_once(self.sources[tag][path], old, new)})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(path, str(raised.exception))

    def test_real_tagged_sources_pass(self):
        self.check()

    def test_each_tagged_statement_must_be_enforced(self):
        for language, path, needles in CHECKER.CLIENT_ATTRIBUTE_READ_TAGGED_STATEMENTS:
            for needle in needles:
                with self.subTest(language=language, needle=needle):
                    self.setUp()
                    self.mutate_tag(self.TAGS[language], path, needle, needle[:-1] + "?")

    def test_java_and_rust_trees_are_seeded_with_real_candidates(self):
        self.assertIn("sdk-java/src/main/java/io/superdurable/dex/Client.java", self.sources[self.TAGS["java"]])
        self.assertIn("sdk-java/src/main/java/io/superdurable/dex/StartFlowOptions.java", self.sources[self.TAGS["java"]])

    def test_added_api_in_java_or_rust_fails(self):
        for language, (path, declaration, _) in self.ABSENCE_CASES.items():
            with self.subTest(language=language):
                self.setUp()
                self.repository.commit_and_tag(self.TAGS[language], {path: declaration})
                with self.assertRaises(SystemExit) as raised:
                    self.check()
                self.assertIn(path, str(raised.exception))

    def test_added_api_in_an_existing_java_client_file_fails(self):
        tag, path = self.TAGS["java"], "sdk-java/src/main/java/io/superdurable/dex/Client.java"
        source = self.sources[tag][path]
        closing = source.rindex("}")
        method = "\n    public AttributeValues getAttributes(final String flowId,\n            AttributeRead<?>... reads) {\n        return null;\n    }\n"
        self.repository.commit_and_tag(tag, {path: source[:closing] + method + source[closing:]})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(path, str(raised.exception))

    def test_non_matching_declarations_and_generated_code_pass(self):
        for language, (path, _, declarations) in self.ABSENCE_CASES.items():
            for declaration in declarations:
                with self.subTest(language=language, declaration=declaration):
                    self.setUp()
                    self.repository.commit_and_tag(self.TAGS[language], {path: declaration})
                    self.check()
        _, _, suffix, _, _ = CHECKER.CLIENT_ATTRIBUTE_READ_ABSENT[1]
        self.setUp()
        self.repository.commit_and_tag(self.TAGS["rust"], {
            "sdk-rust/crates/dex-sdk/src/gen/flow_service" + suffix: self.ABSENCE_CASES["rust"][1],
        })
        self.check()

    def test_each_reference_statement_must_be_enforced(self):
        for page, needles in CHECKER.CLIENT_ATTRIBUTE_READ_REFERENCES:
            for needle in needles:
                with self.subTest(page=page, needle=needle):
                    mutate_page(self, self.check, self.references / page, needle, needle[:-1] + "?")


class FlowServiceTlsTest(unittest.TestCase):
    GO_TAG = "sdk-go/v1.26.0"
    SDK_TAGS = {
        "go": GO_TAG,
        "java": "sdk-java/v1.26.0",
        "python": "sdk-python/v1.28.0",
        "typescript": "sdk-typescript/v1.28.0",
        "rust": "sdk-rust/v1.26.0",
    }
    TAGGED_PATHS = {
        GO_TAG: (
            "sdk-go/dex/flow_service_tls.go", "sdk-go/dex/client.go", "sdk-go/dex/worker.go",
            "sdk-go/dex/message_size.go", "sdk-go/dex/server_protocol.go",
            "sdk-go/dex/credential_api_key.go", "sdk-go/dex/credential_integration_test.go",
            "sdk-go/integ/tls_transport_test.go",
        ),
        "sdk-java/v1.26.0": (
            "sdk-java/src/main/java/io/superdurable/dex/Client.java",
            "sdk-java/src/main/java/io/superdurable/dex/Worker.java",
            "sdk-java/src/main/java/io/superdurable/dex/ClientOptions.java",
            "sdk-java/src/main/java/io/superdurable/dex/WorkerOptions.java",
        ),
        "sdk-python/v1.28.0": (
            "sdk-python/dex/_credential_transport.py", "sdk-python/dex/_pull_worker.py",
            "sdk-python/dex/client_options.py", "sdk-python/dex/worker_options.py",
        ),
        "sdk-typescript/v1.28.0": ("sdk-typescript/src/client.ts", "sdk-typescript/src/worker.ts",
                                      "sdk-typescript/src/options.ts"),
        "sdk-rust/v1.26.0": (
            "sdk-rust/crates/dex-sdk/src/client.rs", "sdk-rust/crates/dex-sdk/src/worker.rs",
            "sdk-rust/crates/dex-sdk/src/client_options.rs", "sdk-rust/crates/dex-sdk/src/worker_options.rs",
        ),
        "server-zd/v0.1.6": (
            "server-zd/crates/dexzd-server/src/config/service_config.rs", "server-zd/README.md",
            "server-zd/crates/dexzd-server/src/server_node.rs",
        ),
    }

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.sources = {}
        for tag, paths in self.TAGGED_PATHS.items():
            self.sources[tag] = tagged_sources(tag, *paths)
            self.repository.commit_and_tag(tag, self.sources[tag])
        self.references = copy_references(
            self.repository.root, "core/operations.md", "core/getting-started.md", "core/pull-workers.md",
            "go/go.md", *(f"{language}/{language}.md" for language in ("java", "python", "typescript", "rust")),
        )

    def check(self):
        CHECKER.check_flow_service_tls(self.repository.root, self.GO_TAG, self.SDK_TAGS, self.references)

    def mutate_tag(self, tag: str, path: str, old: str, new: str):
        mutated = replace_once(self.sources[tag][path], old, new)
        self.repository.commit_and_tag(tag, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(path, str(raised.exception))

    def test_real_tagged_sources_and_references_pass(self):
        self.check()

    def test_plaintext_default_must_remain_nil_only(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/flow_service_tls.go",
                        "if options == nil {\n\t\treturn insecure.NewCredentials(), nil",
                        "if options != nil {\n\t\treturn insecure.NewCredentials(), nil")

    def test_client_must_pass_tls_options_to_dialer(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/client.go",
                        "flowServiceAddress, options.Credential, options.FlowServiceTLS",
                        "flowServiceAddress, options.Credential, nil")

    def test_client_option_type_must_match_reference(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/client.go",
                        "FlowServiceTLS *FlowServiceTLSOptions", "FlowServiceTLS any")

    def test_worker_must_pass_tls_options_to_dialer(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/worker.go",
                        "flowServiceAddress, options.Credential, options.FlowServiceTLS",
                        "flowServiceAddress, options.Credential, nil")

    def test_worker_must_not_open_an_inbound_listener(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/worker.go",
                        "\tpull, err := newPullWorker(",
                        "\tlistener := grpc.NewServer()\n\t_ = listener\n\tpull, err := newPullWorker(")

    def test_ca_bundle_must_be_parsed(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/flow_service_tls.go",
                        "if !rootCAs.AppendCertsFromPEM(options.AdditionalRootCAsPEM) {",
                        "if false {")

    def test_go_tls_options_must_not_gain_client_certificate_field(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/flow_service_tls.go",
                        "\tServerName string\n}", "\tServerName string\n\tClientCertificate tls.Certificate\n}")

    def test_server_name_must_reach_tls_config(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/flow_service_tls.go",
                        "ServerName: options.ServerName", "ServerName: address")

    def test_tls_transport_must_keep_certificate_verification(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/flow_service_tls.go",
                        "return credentials.NewTLS(tlsConfig), nil",
                        "tlsConfig.InsecureSkipVerify = true\n\treturn credentials.NewTLS(tlsConfig), nil")

    def test_tls_transport_must_keep_port_bounds(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/flow_service_tls.go",
                        "portNumber < 1 || portNumber > 65535", "portNumber < 0")

    def test_credential_must_share_selected_transport(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/message_size.go",
                        "credentialDialOptions(credential)...", "credentialDialOptions(nil)...")

    def test_target_authority_test_must_preserve_ipv6(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/credential_integration_test.go",
                        'name: "IPv6 literal", address: ipv6Address, wantAuthority: ipv6Address',
                        'name: "IPv6 literal", address: ipv6Address, wantAuthority: "::1"')

    def test_api_key_credential_must_reach_tls_flow_service(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/credential_integration_test.go",
                        'name: "API key", credential: apiKeyCredential',
                        'name: "API key", credential: nil')

    def test_api_key_http_client_must_remain_separate(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/credential_api_key.go",
                        "return apiKeyHTTPClientOption{httpClient: httpClient}",
                        "return apiKeyHTTPClientOption{httpClient: nil}")

    def test_pull_stream_must_carry_credentials_over_tls(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/credential_integration_test.go",
                        'Credential:         staticTestCredential("pull-token")',
                        'Credential:         nil')

    def test_connection_failure_must_not_be_reported_as_protocol_incompatibility(self):
        self.mutate_tag(self.GO_TAG, "sdk-go/dex/server_protocol.go",
                        "if status.Code(err) == codes.Unavailable {", "if status.Code(err) == codes.Unimplemented {")

    def test_java_transport_must_match_baseline(self):
        self.mutate_tag("sdk-java/v1.26.0", "sdk-java/src/main/java/io/superdurable/dex/Client.java",
                        ".usePlaintext()", ".useTransportSecurity()")

    def test_java_options_must_not_add_tls_without_reference_update(self):
        self.mutate_tag("sdk-java/v1.26.0", "sdk-java/src/main/java/io/superdurable/dex/ClientOptions.java",
                        "private final Credential credential;", "private final Credential credential;\n    private final Object tlsConfig;")

    def test_python_pull_transport_must_match_baseline(self):
        self.mutate_tag("sdk-python/v1.28.0", "sdk-python/dex/_pull_worker.py",
                        "return grpc.aio.insecure_channel(server_address)", "return grpc.aio.secure_channel(server_address)")

    def test_typescript_transport_must_match_baseline(self):
        self.mutate_tag("sdk-typescript/v1.28.0", "sdk-typescript/src/client.ts",
                        "credentials.createInsecure()", "credentials.createSsl()")

    def test_rust_default_transport_must_match_baseline(self):
        self.mutate_tag("sdk-rust/v1.26.0", "sdk-rust/crates/dex-sdk/src/client.rs",
                        'format!("http://{address}")', 'format!("https://{address}")')

    def test_server_zd_listener_must_remain_plaintext(self):
        self.mutate_tag("server-zd/v0.1.6", "server-zd/crates/dexzd-server/src/config/service_config.rs",
                        "bind address for the plaintext public gRPC services", "bind address for the TLS public gRPC services")

    def test_server_zd_must_not_add_native_tls_without_reference_update(self):
        self.mutate_tag("server-zd/v0.1.6", "server-zd/crates/dexzd-server/src/server_node.rs",
                        "tonic::transport::Server::builder()", "tonic::transport::Server::builder().tls_config(config)")

    def test_core_must_name_both_go_options(self):
        mutate_page(self, self.check, self.references / "core/operations.md",
                    "WorkerOptions.FlowServiceTLS", "WorkerOptions.TransportTLS")

    def test_core_must_keep_plaintext_default(self):
        mutate_page(self, self.check, self.references / "core/operations.md",
                    "Nil keeps plaintext", "Nil enables TLS")

    def test_go_reference_must_keep_exact_option_type(self):
        mutate_page(self, self.check, self.references / "go/go.md",
                    "AdditionalRootCAsPEM []byte", "AdditionalRootCAsPEM string")

    def test_language_reference_must_keep_absent_option_boundary(self):
        mutate_page(self, self.check, self.references / "java/java.md",
                    "no FlowService TLS option", "a FlowService TLS option")


class PullOnlyWorkersTest(unittest.TestCase):
    SDK_TAGS = {
        "go": "sdk-go/v1.26.0",
        "java": "sdk-java/v1.26.0",
        "python": "sdk-python/v1.28.0",
        "typescript": "sdk-typescript/v1.28.0",
        "rust": "sdk-rust/v1.26.0",
    }

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.sources = {}
        for language, tag in self.SDK_TAGS.items():
            paths = {path for path, _ in CHECKER.PULL_ONLY_SOURCES[language]}
            paths |= {path for path, _ in CHECKER.PULL_ONLY_FORBIDDEN_OPTIONS[language]}
            paths |= {path for path, _, _ in CHECKER.PULL_ONLY_ROUTING_FUNCTIONS.get(language, ())}
            self.sources[tag] = tagged_sources(tag, *sorted(paths))
            self.repository.commit_and_tag(tag, self.sources[tag])
        self.references = copy_references(
            self.repository.root, *(page for page, _ in CHECKER.PULL_ONLY_REFERENCE_STATEMENTS)
        )

    def check(self):
        CHECKER.check_pull_only_workers(self.repository.root, self.SDK_TAGS, self.references)

    def mutate_tag(self, tag: str, path: str, old: str, new: str):
        mutated = replace_once(self.sources[tag][path], old, new)
        self.repository.commit_and_tag(tag, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(path, str(raised.exception))

    def test_real_tagged_sources_and_references_pass(self):
        self.check()

    def test_go_start_and_sub_flow_must_each_route_to_pull_workers(self):
        tag, path = "sdk-go/v1.26.0", "sdk-go/dex/proto_mapper.go"
        source = self.sources[tag][path]
        pull_false = CHECKER.GO_PULL_ROUTING.replace("IsPullMode: true", "IsPullMode: false")
        for function in ("mapStartFlowOptions", "mapSubFlowOptions"):
            with self.subTest(function=function):
                mutated = replace_in_function(source, f"func {function}(", CHECKER.GO_PULL_ROUTING, pull_false)
                self.repository.commit_and_tag(tag, {path: mutated})
                with self.assertRaises(SystemExit) as raised:
                    self.check()
                self.assertIn(path, str(raised.exception))
                self.assertIn(function, str(raised.exception))
        self.repository.commit_and_tag(tag, {path: source})
        self.check()

    def test_rust_pull_flow_config_must_set_pull_mode(self):
        self.mutate_tag("sdk-rust/v1.26.0", "sdk-rust/crates/dex-sdk/src/client.rs",
                        "ProtoWorkerTarget { is_pull_mode: true }", "ProtoWorkerTarget { is_pull_mode: false }")

    def test_rust_start_flow_must_use_pull_flow_config(self):
        self.mutate_tag("sdk-rust/v1.26.0", "sdk-rust/crates/dex-sdk/src/client.rs",
                        "Some(map_pull_flow_config(config)?)", "Some(map_flow_config(config)?)")

    def test_rust_sub_flow_must_use_pull_flow_config(self):
        self.mutate_tag("sdk-rust/v1.26.0", "sdk-rust/crates/dex-sdk/src/worker_dispatcher.rs",
                        "crate::client::map_pull_flow_config(options.config_override.as_ref())",
                        "crate::client::map_flow_config(options.config_override.as_ref())")

    def test_go_bind_address_must_stay_removed(self):
        self.mutate_tag("sdk-go/v1.26.0", "sdk-go/dex/worker.go",
                        "\tPull *PullWorkerOptions\n", "\tPull *PullWorkerOptions\n\tBindAddress string\n")

    def test_python_worker_target_option_must_stay_removed(self):
        self.mutate_tag("sdk-python/v1.28.0", "sdk-python/dex/client_options.py",
                        "class ClientOptions", "worker_target = None\n\n\nclass ClientOptions")

    def test_typescript_bind_address_must_stay_removed(self):
        self.mutate_tag("sdk-typescript/v1.28.0", "sdk-typescript/src/options.ts",
                        "export interface WorkerOptions {", "export interface WorkerOptions {\n  bindAddress?: string;")

    def test_rust_pull_default_must_remain(self):
        self.mutate_tag("sdk-rust/v1.26.0", "sdk-rust/crates/dex-sdk/src/worker_options.rs",
                        "pull: PullWorkerOptions::default(),", "pull: PullWorkerOptions::disabled(),")

    def test_each_reference_statement_must_be_enforced(self):
        for page, needles in CHECKER.PULL_ONLY_REFERENCE_STATEMENTS:
            for needle in needles:
                with self.subTest(page=page, needle=needle):
                    mutate_page(self, self.check, self.references / page, needle, needle[:-1] + "?")


if __name__ == "__main__":
    unittest.main()
