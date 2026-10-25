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
    """Returns the source with old replaced inside one fixture function, located without the checker."""
    start = source.index(declaration)
    end = source.index("\n}\n", start) + len("\n}\n")
    function = source[start:end]
    return source[:start] + replace_once(function, old, new) + source[end:]


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
        source_root = Path(__file__).resolve().parents[2]
        self.files = {
            path: CHECKER.git(source_root, "show", f"refs/tags/sdk-go/v1.21.0:{path}")
            for path in (self.CLIENT_PATH, self.MAPPER_PATH, self.ERRORS_PATH, self.README_PATH, self.DOC_PATH)
        }
        self.repository.commit_and_tag(self.TAG, self.files)
        self.references = self.repository.root / "references"
        for language, page, content in (
            ("go", "observability.md", "func (client *Client) DescribeFlow(ctx context.Context, flowID string) (FlowInfo, error) returns *dex.FlowNotFoundError."),
            ("core", "error-handling.md", "The Go SDK Client.DescribeFlow arrives in v1.21.0."),
        ):
            path = self.references / language / page
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)

    def check(self):
        CHECKER.check_go_describe_flow(self.repository.root, self.TAG, self.references)

    def assert_mutation_fails(self, path, declaration, old, new):
        mutated = replace_in_function(self.files[path], declaration, old, new) if declaration else replace_once(self.files[path], old, new)
        self.repository.commit_and_tag(self.TAG, {path: mutated})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{self.TAG}:{path}", str(raised.exception))

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

    def test_reference_must_name_released_method(self):
        page = self.references / "go" / "observability.md"
        page.write_text(page.read_text().replace("DescribeFlow", "DescribeRun"))
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(str(page), str(raised.exception))

    def test_tagged_readme_must_keep_status_read_boundary(self):
        self.assert_mutation_fails(
            self.README_PATH, None,
            "Use `WaitForFlow` when the caller needs to wait",
            "Use `DescribeFlow` when the caller needs to wait",
        )


class ServerZdSearchTlsTest(unittest.TestCase):
    TAG = "server-zd/v9.9.9"
    CONNECTION_PATH = CHECKER.POSTGRES_CONNECTION_PATH
    TLS_PATH = CHECKER.POSTGRES_TLS_PATH
    DSN_PATH = CHECKER.POSTGRES_DSN_PATH
    BACKEND_PATH = CHECKER.SEARCH_BACKEND_PATH
    PROJECTION_PATH = "server-zd/crates/dexzd-server/src/server_node/search_projection.rs"

    def setUp(self):
        self.repository = FixtureRepository(self)
        source_root = Path(__file__).resolve().parents[2]
        paths = (
            "server-zd/README.md",
            "docs/content/production/server-zd-operations.mdx",
            self.CONNECTION_PATH,
            self.TLS_PATH,
            self.DSN_PATH,
            self.BACKEND_PATH,
            self.PROJECTION_PATH,
        )
        self.files = {
            path: CHECKER.git(source_root, "show", f"refs/tags/server-zd/v0.1.6:{path}")
            for path in paths
        }
        self.repository.commit_and_tag(self.TAG, self.files)
        self.references = self.repository.root / "references"
        page = self.references / "core" / "operations.md"
        page.parent.mkdir(parents=True, exist_ok=True)
        page.write_text("From server-zd v0.1.6, sslmode=require and sslnegotiation=direct are supported.")

    def check(self):
        CHECKER.check_server_zd_search_tls(self.repository.root, self.TAG, self.references)

    def assert_mutation_fails(self, path, old, new):
        self.repository.commit_and_tag(self.TAG, {path: replace_once(self.files[path], old, new)})
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(f"{self.TAG}:{path}", str(raised.exception))

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

    def test_reference_must_name_new_release(self):
        page = self.references / "core" / "operations.md"
        page.write_text(page.read_text().replace("v0.1.6", "v0.1.5"))
        with self.assertRaises(SystemExit) as raised:
            self.check()
        self.assertIn(str(page), str(raised.exception))


class FlowSummaryRpcTest(unittest.TestCase):
    SERVER_TAG = "server/v9.9.9"
    SERVER_ZD_TAG = "server-zd/v9.9.9"
    ZD_SERVICE_PATH = "server-zd/crates/dexzd-api/src/flow_service.rs"
    ZD_OPERATIONS_PATH = "server-zd/crates/dexzd-api/src/runtime_flow_operations.rs"

    def setUp(self):
        self.repository = FixtureRepository(self)
        source_root = Path(__file__).resolve().parents[2]
        self.server_source = CHECKER.git(source_root, "show", f"refs/tags/server/v1.20.0:{SERVICE_PATH}")
        self.zd_sources = {
            path: CHECKER.git(source_root, "show", f"refs/tags/server-zd/v0.1.6:{path}")
            for path in (self.ZD_SERVICE_PATH, self.ZD_OPERATIONS_PATH)
        }
        self.repository.commit_and_tag(self.SERVER_TAG, {SERVICE_PATH: self.server_source})
        self.repository.commit_and_tag(self.SERVER_ZD_TAG, self.zd_sources)

    def check(self):
        CHECKER.check_flow_summary_rpc(self.repository.root, self.SERVER_TAG, self.SERVER_ZD_TAG)

    def test_released_server_rpcs_pass(self):
        self.check()

    def test_go_server_must_describe_execution(self):
        mutated = replace_in_function(
            self.server_source, "func (s *serviceImpl) GetFlowSummary(",
            "s.client.DescribeWorkflowExecution(", "s.client.QueryWorkflow(",
        )
        self.repository.commit_and_tag(self.SERVER_TAG, {SERVICE_PATH: mutated})
        with self.assertRaises(SystemExit):
            self.check()

    def test_server_zd_must_route_summary_rpc(self):
        mutated = replace_once(
            self.zd_sources[self.ZD_SERVICE_PATH],
            ".get_flow_summary(TenantId::DEFAULT, request.into_inner())",
            ".wait_for_flow(TenantId::DEFAULT, request.into_inner())",
        )
        self.repository.commit_and_tag(self.SERVER_ZD_TAG, {self.ZD_SERVICE_PATH: mutated})
        with self.assertRaises(SystemExit):
            self.check()


if __name__ == "__main__":
    unittest.main()
