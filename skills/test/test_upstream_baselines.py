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

SERVER_TAG = "server/v9.9.9"
UPDATER_PATH = "server/service/interpreter/workflowUpdater.go"
SERVICE_PATH = "server/service/api/service.go"
TERMINAL_PATH = "server/service/interpreter/terminalCoordinator.go"
VALIDATORS = (
    "validateWorkerRpc",
    "validateDeleteChannelMessage",
    "validateWaitForStepCompletion",
    "validateWaitForAttribute",
)
GUARD = "\tif err := u.rejectTerminalUpdate(); err != nil {\n\t\treturn err\n\t}\n"


def gofmt_tabs(source: str) -> str:
    """Returns the fixture with each leading four-space group replaced by the tab gofmt uses."""
    return re.sub(r"(?m)^((?:    )+)", lambda match: "\t" * (len(match.group(1)) // 4), source)


# Synthetic gofmt-shaped fixtures that keep the decoys which made whole-file needles vacuous.
UPDATER_SOURCE = gofmt_tabs("""package interpreter

func NewWorkflowUpdater(
    ctx interfaces.UnifiedContext,
) error {
    updater := &WorkflowUpdater{}
    if err := provider.SetInvokeRPCUpdateHandler(
        ctx,
        updater.validateWorkerRpc,
        updater.handleWorkerRpc,
    ); err != nil {
        return err
    }
    if err := provider.SetDeleteChannelMessageUpdateHandler(
        ctx,
        updater.validateDeleteChannelMessage,
        updater.handleDeleteChannelMessage,
    ); err != nil {
        return err
    }
    if err := provider.SetWaitForStepCompletionUpdateHandler(
        ctx,
        updater.validateWaitForStepCompletion,
        updater.handleWaitForStepCompletion,
    ); err != nil {
        return err
    }
    if err := provider.SetWaitForAttributeUpdateHandler(
        ctx,
        updater.validateWaitForAttribute,
        updater.handleWaitForAttribute,
    ); err != nil {
        return err
    }
    return nil
}

func (u *WorkflowUpdater) validateDeleteChannelMessage(
    _ interfaces.UnifiedContext,
    request *dexpb.DeleteChannelMessageRequest,
) error {
    if err := u.rejectTerminalUpdate(); err != nil {
        return err
    }
    return nil
}

func (u *WorkflowUpdater) validateWorkerRpc(
    _ interfaces.UnifiedContext,
    input *dexpb.InvokeRPCRequest,
) error {
    if err := u.rejectTerminalUpdate(); err != nil {
        return err
    }
    return nil
}

func (u *WorkflowUpdater) validateWaitForStepCompletion(
    _ interfaces.UnifiedContext,
    request *dexpb.WaitForStepCompletionRequest,
) error {
    if err := u.rejectTerminalUpdate(); err != nil {
        return err
    }
    return nil
}

func (u *WorkflowUpdater) validateWaitForAttribute(
    _ interfaces.UnifiedContext,
    request *dexpb.WaitForAttributeRequest,
) error {
    if err := u.rejectTerminalUpdate(); err != nil {
        return err
    }
    return nil
}

func (u *WorkflowUpdater) rejectTerminalUpdate() error {
    if !u.terminalCoordinator.HasStartedFinalizing() {
        return nil
    }
    return u.provider.NewUpdateError(
        dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,
        service.FlowTerminalCleanupInProgressMessage,
    )
}
""")

SERVICE_SOURCE = gofmt_tabs("""package api

func (s *serviceImpl) DeleteChannelMessage(
    ctx context.Context,
    req *dexpb.DeleteChannelMessageRequest,
) (*emptypb.Empty, error) {
    runID := req.GetRunId()
    if s.client.GetBackendType() == service.BackendTypeCadence {
        return s.deleteCadenceChannelMessage(ctx, req, runID)
    }
    return s.deleteTemporalChannelMessage(ctx, req, runID)
}

func (s *serviceImpl) deleteCadenceChannelMessage(
    ctx context.Context,
    req *dexpb.DeleteChannelMessageRequest,
    runID string,
) (*emptypb.Empty, error) {
    if err := s.client.SignalWorkflow(ctx, req.GetFlowId(), runID); err != nil {
        return nil, s.handleError(err)
    }
    return &emptypb.Empty{}, nil
}

func (s *serviceImpl) PublishToChannel(
    ctx context.Context,
    req *dexpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
    if err := s.client.SignalWorkflow(ctx, req.GetFlowId(), ""); err != nil {
        return nil, s.handleError(err)
    }
    return &emptypb.Empty{}, nil
}

func (s *serviceImpl) InvokeRPC(
    ctx context.Context,
    req *dexpb.InvokeRPCRequest,
) (*dexpb.InvokeRPCResponse, error) {
    backendType := s.client.GetBackendType()
    if len(req.GetLockAttributeKeys()) > 0 && backendType == service.BackendTypeCadence {
        return nil, status.Errorf(codes.Unimplemented, "locking RPC requires Temporal synchronous update")
    }
    isTransactional := s.shouldInvokeRPCTransactionally(req)
    return s.doInvokeRPC(ctx, req, isTransactional)
}

func (s *serviceImpl) WaitForStepCompletion(
    ctx context.Context,
    req *dexpb.WaitForStepCompletionRequest,
) (*dexpb.WaitForStepCompletionResponse, error) {
    if s.client.GetBackendType() == service.BackendTypeCadence {
        return nil, status.Errorf(codes.Unimplemented, "WaitForStepCompletion requires Temporal synchronous update")
    }
    return s.waitForStepCompletion(ctx, req)
}

func (s *serviceImpl) WaitForAttribute(
    ctx context.Context,
    req *dexpb.WaitForAttributeRequest,
) (*dexpb.WaitForAttributeResponse, error) {
    if s.client.GetBackendType() == service.BackendTypeCadence {
        return nil, status.Errorf(codes.Unimplemented, "WaitForAttribute requires Temporal synchronous update")
    }
    return s.waitForAttribute(ctx, req)
}

func (s *serviceImpl) shouldInvokeRPCTransactionally(req *dexpb.InvokeRPCRequest) bool {
    return s.client.GetBackendType() == service.BackendTypeTemporal &&
        (req.GetIsTransactional() || len(req.GetLockAttributeKeys()) > 0 ||
            s.apiCfg.UseTemporalSynchronousUpdateForAllRPCs)
}

func (s *serviceImpl) handleError(err error) error {
    var details string
    if updateType, ok := s.client.GetIfUpdateError(err, &details); ok {
        switch updateType {
        case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION:
            subStatus := dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED
            if details == service.FlowTerminalCleanupInProgressMessage {
                subStatus = dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_CLOSING
            }
            return serviceerrors.NewErrorAndStatus(
                codes.FailedPrecondition,
                subStatus,
                details,
            ).ToGRPCError()
        }
    }
    return err
}
""")

TERMINAL_SOURCE = gofmt_tabs("""package interpreter

func (c *TerminalCoordinator) CoordinateAndFinalizeError(
    ctx interfaces.UnifiedContext,
    retErr error,
) error {
    c.startedFinalizing = true
    if err := c.provider.Await(ctx, func() bool {
        return c.attributeSyncer.ProducersDrained() && c.continueAsNewer.inflightUpdateOperations == 0
    }); err != nil {
        return err
    }
    for {
        if err := c.attributeSyncer.FlushAndClose(ctx); err != nil {
            return err
        }
        return retErr
    }
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
            SERVICE_SOURCE, "func (s *serviceImpl) deleteCadenceChannelMessage(", "fixture",
        )
        self.assertTrue(function.startswith("func (s *serviceImpl) deleteCadenceChannelMessage(\n"))
        self.assertTrue(function.endswith("\n}"))
        self.assertEqual(1, function.count("s.client.SignalWorkflow("))
        self.assertNotIn("PublishToChannel", function)

    def test_missing_declaration_names_location_and_declaration(self):
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(UPDATER_SOURCE, "func (u *WorkflowUpdater) validateMissing(", "tag:path.go")
        self.assertIn("tag:path.go has no function declared as `func (u *WorkflowUpdater) validateMissing(`", str(raised.exception))

    def test_declaration_does_not_match_a_longer_function_name(self):
        renamed = UPDATER_SOURCE.replace("validateWaitForAttribute(", "validateWaitForAttributeMatch(")
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(renamed, "func (u *WorkflowUpdater) validateWaitForAttribute(", "fixture")
        self.assertIn("has no function declared", str(raised.exception))

    def test_duplicate_declaration_fails(self):
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(UPDATER_SOURCE + UPDATER_SOURCE, "func NewWorkflowUpdater(", "fixture")
        self.assertIn("declares `func NewWorkflowUpdater(` 2 times", str(raised.exception))

    def test_function_without_closing_brace_before_next_declaration_fails(self):
        source = "func Broken() error {\n\treturn nil\ntype After struct {\n}\n"
        with self.assertRaises(SystemExit) as raised:
            CHECKER.go_function_source(source, "func Broken(", "fixture")
        self.assertIn("has no column-0 closing brace", str(raised.exception))


class GoServerCloseCleanupTest(unittest.TestCase):
    def setUp(self):
        self.repository = FixtureRepository(self)
        self.files = {UPDATER_PATH: UPDATER_SOURCE, SERVICE_PATH: SERVICE_SOURCE, TERMINAL_PATH: TERMINAL_SOURCE}
        self.repository.commit_and_tag(SERVER_TAG, self.files)

    def check(self):
        CHECKER.check_go_server_close_cleanup(self.repository.root, SERVER_TAG)

    def assert_mutation_fails(self, path, declaration, old, new, missing_fragment, whole_file_needles):
        """Asserts one function-scoped mutation fails while every former whole-file needle still matches."""
        mutated = replace_in_function(self.files[path], declaration, old, new)
        for needle in whole_file_needles:
            self.assertIn(needle, mutated)
        self.repository.commit_and_tag(SERVER_TAG, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        message = str(raised.exception)
        for expected in (f"{SERVER_TAG}:{path}", f"function `{declaration}`", "missing required text", missing_fragment):
            self.assertIn(expected, message)

    def test_unmodified_fixture_passes(self):
        self.check()

    def test_each_validator_requires_its_cleanup_guard(self):
        for validator in VALIDATORS:
            with self.subTest(validator=validator):
                self.repository.commit_and_tag(SERVER_TAG, self.files)
                self.assert_mutation_fails(
                    UPDATER_PATH, f"func (u *WorkflowUpdater) {validator}(", GUARD, "",
                    "rejectTerminalUpdate()", ("rejectTerminalUpdate", "FlowTerminalCleanupInProgressMessage", validator),
                )

    def test_removing_every_guard_fails_while_the_helper_remains(self):
        mutated = UPDATER_SOURCE
        for validator in VALIDATORS:
            mutated = replace_in_function(mutated, f"func (u *WorkflowUpdater) {validator}(", GUARD, "")
        self.assertIn("rejectTerminalUpdate", mutated)
        self.repository.commit_and_tag(SERVER_TAG, {UPDATER_PATH: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn("rejectTerminalUpdate()", str(raised.exception))

    def test_unregistered_validator_fails(self):
        self.assert_mutation_fails(
            UPDATER_PATH, "func NewWorkflowUpdater(", "updater.validateWaitForAttribute,", "nil,",
            "updater.validateWaitForAttribute", ("validateWaitForAttribute",),
        )

    def test_terminal_update_rejection_must_depend_on_finalizing(self):
        self.assert_mutation_fails(
            UPDATER_PATH, "func (u *WorkflowUpdater) rejectTerminalUpdate(",
            "if !u.terminalCoordinator.HasStartedFinalizing() {", "if true {",
            "HasStartedFinalizing", ("rejectTerminalUpdate", "FlowTerminalCleanupInProgressMessage"),
        )

    def test_cleanup_rejection_must_map_to_flow_closing(self):
        self.assert_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) handleError(",
            "if details == service.FlowTerminalCleanupInProgressMessage {", 'if details == "" {',
            "ERROR_SUB_STATUS_FLOW_CLOSING", ("ErrorSubStatus_ERROR_SUB_STATUS_FLOW_CLOSING",),
        )

    def test_transactional_routing_requires_temporal_backend(self):
        self.assert_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) shouldInvokeRPCTransactionally(",
            "return s.client.GetBackendType() == service.BackendTypeTemporal &&\n\t\t(", "return (",
            "BackendTypeTemporal", ("shouldInvokeRPCTransactionally",),
        )

    def test_invoke_rpc_must_use_transactional_routing(self):
        self.assert_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) InvokeRPC(",
            "isTransactional := s.shouldInvokeRPCTransactionally(req)", "isTransactional := req.GetIsTransactional()",
            "shouldInvokeRPCTransactionally(req)", ("shouldInvokeRPCTransactionally",),
        )

    def test_invoke_rpc_must_reject_cadence_locking_rpcs(self):
        self.assert_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) InvokeRPC(",
            "len(req.GetLockAttributeKeys()) > 0 && backendType == service.BackendTypeCadence", "false",
            "codes.Unimplemented", (),
        )

    def test_delete_channel_message_must_dispatch_cadence(self):
        self.assert_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) DeleteChannelMessage(",
            "if s.client.GetBackendType() == service.BackendTypeCadence {", "if false {",
            "deleteCadenceChannelMessage(", ("deleteCadenceChannelMessage",),
        )

    def test_cadence_delete_requires_signal_inside_its_function(self):
        self.assert_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) deleteCadenceChannelMessage(",
            "s.client.SignalWorkflow(", "s.client.UpdateWorkflow(",
            "SignalWorkflow", ("deleteCadenceChannelMessage", "s.client.SignalWorkflow("),
        )

    def test_cadence_waits_return_unimplemented(self):
        for wait in ("WaitForStepCompletion", "WaitForAttribute"):
            with self.subTest(wait=wait):
                self.repository.commit_and_tag(SERVER_TAG, self.files)
                self.assert_mutation_fails(
                    SERVICE_PATH, f"func (s *serviceImpl) {wait}(",
                    "return nil, status.Errorf(codes.Unimplemented,",
                    "return nil, status.Errorf(codes.FailedPrecondition,",
                    "codes.Unimplemented", ("codes.Unimplemented", wait),
                )

    def test_terminal_cleanup_waits_for_inflight_updates(self):
        self.assert_mutation_fails(
            TERMINAL_PATH, "func (c *TerminalCoordinator) CoordinateAndFinalizeError(",
            "c.continueAsNewer.inflightUpdateOperations == 0", "true",
            "inflightUpdateOperations == 0", ("CoordinateAndFinalizeError",),
        )


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
            "sdk-go/v1.21.0", self.CLIENT_PATH, self.MAPPER_PATH, self.ERRORS_PATH, self.README_PATH, self.DOC_PATH,
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
            "closedAt = response.CloseTime.AsTime()", "closedAt = time.Time{}",
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
            "go/observability.md", "server-zd keeps the run ID across time travel.",
            "server-zd also creates a new run ID after time travel.",
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
            self.sources[language] = tagged_sources(f"sdk-{language}/v1.20.0", *paths)
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
            "Java, Python, TypeScript, and Rust already have it at their v1.20.0 baselines.",
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
    SERVER_TAG = "server/v9.9.9"
    SERVER_ZD_TAG = "server-zd/v9.9.9"
    ERROR_STATUS_PATH = CHECKER.SERVER_ERROR_STATUS_PATH
    ZD_SERVICE_PATH = "server-zd/crates/dexzd-api/src/flow_service.rs"
    ZD_OPERATIONS_PATH = "server-zd/crates/dexzd-api/src/runtime_flow_operations.rs"
    ZD_READ_PATH = CHECKER.SERVER_ZD_READ_OPERATIONS_PATH
    ZD_STATE_ROWS_PATH = CHECKER.SERVER_ZD_STATE_ROWS_PATH
    ZD_RUNTIME_ERROR_PATH = CHECKER.SERVER_ZD_RUNTIME_ERROR_PATH
    ZD_TIME_TRAVEL_PATH = CHECKER.SERVER_ZD_TIME_TRAVEL_TEST_PATH

    def setUp(self):
        self.repository = FixtureRepository(self)
        self.server_sources = tagged_sources("server/v1.20.0", SERVICE_PATH, self.ERROR_STATUS_PATH)
        self.zd_sources = tagged_sources(
            "server-zd/v0.1.6", self.ZD_SERVICE_PATH, self.ZD_OPERATIONS_PATH, self.ZD_READ_PATH,
            self.ZD_STATE_ROWS_PATH, self.ZD_RUNTIME_ERROR_PATH, self.ZD_TIME_TRAVEL_PATH,
        )
        self.repository.commit_and_tag(self.SERVER_TAG, self.server_sources)
        self.repository.commit_and_tag(self.SERVER_ZD_TAG, self.zd_sources)

    def check(self):
        CHECKER.check_flow_summary_rpc(self.repository.root, self.SERVER_TAG, self.SERVER_ZD_TAG)

    def assert_mutation_fails(self, tag, sources, path, declaration, old, new):
        source = sources[path]
        mutated = replace_in_function(source, declaration, old, new) if declaration else replace_once(source, old, new)
        self.repository.commit_and_tag(tag, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{tag}:{path}", str(raised.exception))

    def assert_server_mutation_fails(self, path, declaration, old, new):
        self.assert_mutation_fails(self.SERVER_TAG, self.server_sources, path, declaration, old, new)

    def assert_server_zd_mutation_fails(self, path, declaration, old, new):
        self.assert_mutation_fails(self.SERVER_ZD_TAG, self.zd_sources, path, declaration, old, new)

    def test_released_server_rpcs_pass(self):
        self.check()

    def test_go_server_must_describe_execution(self):
        self.assert_server_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) GetFlowSummary(",
            "s.client.DescribeWorkflowExecution(", "s.client.QueryWorkflow(",
        )

    def test_go_server_must_return_described_run_id(self):
        self.assert_server_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) GetFlowSummary(",
            "RunId:  description.RunId,", "RunId:  req.GetRunId(),",
        )

    def test_go_server_must_return_flow_type(self):
        self.assert_server_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) GetFlowSummary(",
            "FlowType:   description.IndexedAttributes[service.SearchAttributeDexWorkflowType].GetStringValue(),",
            'FlowType:   "",',
        )

    def test_go_server_must_return_close_time(self):
        self.assert_server_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) GetFlowSummary(",
            "\t\tresponse.CloseTime = timestamppb.New(*description.CloseTime)\n", "",
        )

    def test_go_server_missing_flow_must_map_to_not_found(self):
        self.assert_server_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) handleError(",
            "return serviceerrors.NotFound(err.Error()).ToGRPCError()",
            "return serviceerrors.Internal(err.Error()).ToGRPCError()",
        )

    def test_go_server_not_found_must_carry_flow_not_exists(self):
        self.assert_server_mutation_fails(
            self.ERROR_STATUS_PATH, "func NotFound(",
            "dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS", "dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED",
        )

    def test_go_server_time_travel_must_return_reset_run_id(self):
        self.assert_server_mutation_fails(
            SERVICE_PATH, "func (s *serviceImpl) ResetFlow(",
            "return &dexpb.ResetFlowResponse{RunId: runId}, nil", "return &dexpb.ResetFlowResponse{RunId: req.GetRunId()}, nil",
        )

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


if __name__ == "__main__":
    unittest.main()
