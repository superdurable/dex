#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SDK_REFERENCES = ROOT / "dex-sdk" / "references"
# The server-zd release used by core operations guidance.
SERVER_ZD_RELEASE = "server-zd/v0.1.6"
WORKFLOW_UPDATER_PATH = "server/service/interpreter/workflowUpdater.go"
API_SERVICE_PATH = "server/service/api/service.go"
TERMINAL_COORDINATOR_PATH = "server/service/interpreter/terminalCoordinator.go"
POSTGRES_CONNECTION_PATH = "server-zd/crates/dexzd-postgres-connection/src/lib.rs"
POSTGRES_TLS_PATH = "server-zd/crates/dexzd-postgres-connection/src/tls.rs"
POSTGRES_DSN_PATH = "server-zd/crates/dexzd-postgres-connection/src/dsn.rs"
SEARCH_BACKEND_PATH = "server-zd/crates/dexzd-search/src/paradedb/search_backend.rs"
SEARCH_PROJECTION_PATH = "server-zd/crates/dexzd-server/src/server_node/search_projection.rs"
SERVER_ZD_READ_OPERATIONS_PATH = "server-zd/crates/dexzd-engine/src/runtime/read_operations/mod.rs"
SERVER_ZD_STATE_ROWS_PATH = "server-zd/crates/dexzd-engine/src/runtime/read_operations/state_rows.rs"
SERVER_ZD_RUNTIME_ERROR_PATH = "server-zd/crates/dexzd-engine/src/runtime/runtime_error.rs"
SERVER_ZD_TIME_TRAVEL_TEST_PATH = "server-zd/crates/dexzd-e2e-tests/tests/dev_server/time_travel.rs"
SERVER_ERROR_STATUS_PATH = "server/service/common/errors/errorStatus.go"
# Each language closed-Flow race section ends its completed-status rule with this text.
COMPLETED_STATUS_RULE = " as idempotent success only when successful completion guarantees the requested condition."
GO_DESCRIBE_FLOW_SIGNATURE = "func (client *Client) DescribeFlow(ctx context.Context, flowID string) (FlowInfo, error)"
# Each server-zd statement as (release the references cite, tagged path, needles); "" means the bundled release.
SERVER_ZD_STATEMENTS = (
    ("server-zd/v0.1.3", "server-zd/README.md", (
        "Only `type: postgres` is supported.",
        "Synchronization is asynchronous.",
        "Rows normally appear within a second of the commit.",
        "(default `dexzd_attribute_sync_fencing`)",
        "dex-server-zd never creates or alters tables",
        "`--attribute-store-config` reads only the `attributeStore` section",
        "which must define at least one store",
        "StopFlow closes the run as soon as it commits.",
    )),
    ("server-zd/v0.1.4", "server-zd/README.md", (
        "server-zd v0.1.4 or later waits, like the Go Server.",
        "GetFlowSummary and SearchFlows",
        "for every\n  transactional or locking RPC",
        "SetAttributes, PublishToChannel, and non-transactional RPC results are",
        "`syncRetryPolicy.totalDuration` bounds each pending entry's retry window",
        "Entries of a store that is no longer configured",
        "StopFlow (CANCEL or FAIL), Terminate",
        "`processCrashSafe` (default)",
        "Delete the whole data directory to start",
        "A directory that an earlier release stopped gracefully keeps working in every mode.",
        "A directory that an earlier release left after an abrupt exit is refused in the default and",
        "After the first start of a release with this rule, the directory is stamped, and any mode works.",
        "takes it over at once",
    )),
    ("server-zd/v0.1.5", "server-zd/README.md", (
        "cleanup fail with FAILED_PRECONDITION and FLOW_CLOSING",
    )),
    ("server-zd/v0.1.4", "docs/content/production/server-zd-operations.mdx", (
        "server-zd v0.1.4 or later waits, like the Go Server.",
        "Transactional or locking RPCs, WaitForStepCompletion, WaitForAttribute, and",
        "**syncRetryPolicy.totalDuration** limits how long each pending write is",
        "Pending writes for a store that is no longer configured are dropped at once",
        "**processCrashSafe**",
    )),
    ("server-zd/v0.1.5", "docs/content/production/server-zd-operations.mdx", (
        "every write erases a currently stored hard-delete Attribute with null",
    )),
    ("", "server-zd/crates/dexzd-worker-dispatch/src/worker_registry.rs", ("CancelTask(",)),
    ("", "server-zd/crates/dexzd-engine/src/effects/dispatch/method_attempt.rs", ("session.cancel();",)),
    ("server-zd/v0.1.5", "server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs", (
        "is_closed_hard_delete_erase",
        "is_hard_delete_on_null",
        "request.action_permission_mappings.is_some()",
    )),
)
# Go Server update validators that reject new requests during Flow close cleanup, with their handlers.
CLEANUP_REJECTING_UPDATES = (
    ("validateWorkerRpc", "handleWorkerRpc"),
    ("validateDeleteChannelMessage", "handleDeleteChannelMessage"),
    ("validateWaitForStepCompletion", "handleWaitForStepCompletion"),
    ("validateWaitForAttribute", "handleWaitForAttribute"),
)
TERMINAL_UPDATE_GUARD = "if err := u.rejectTerminalUpdate(); err != nil {\n\t\treturn err\n\t}"


def fail(message: str) -> None:
    raise SystemExit(message)


def git(root: Path, *arguments: str) -> str:
    return subprocess.run(
        ["git", *arguments],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()


def require_revision(checkout: Path, expected: str, label: str) -> None:
    checkout = checkout.resolve()
    if not (checkout / ".git").exists():
        fail(f"{label} is not a Git checkout: {checkout}")
    actual = git(checkout, "rev-parse", "HEAD")
    resolved = git(checkout, "rev-parse", f"{expected}^{{commit}}")
    if actual != resolved:
        fail(f"{label} checkout {actual} does not match baseline {resolved}")


def require_text(path: Path, *needles: str) -> None:
    content = path.read_text()
    for needle in needles:
        if needle not in content:
            fail(f"{path} is missing required text: {needle}")


def require_section_text(path: Path, heading: str, *needles: str) -> None:
    """Requires needles in one Markdown section's own text, before its next heading of any level."""
    lines = path.read_text().splitlines()
    if lines.count(heading) != 1:
        fail(f"{path} must contain the heading `{heading}` exactly once")
    start = lines.index(heading) + 1
    end = len(lines)
    is_fenced = False
    for index in range(start, len(lines)):
        if lines[index].startswith("```"):
            is_fenced = not is_fenced
        elif not is_fenced and lines[index].startswith("#"):
            end = index
            break
    section = "\n".join(lines[start:end])
    for needle in needles:
        if needle not in section:
            fail(f"{path} section `{heading}` is missing required text: {needle}")


def require_tagged_text(checkout: Path, tag: str, path: str, *needles: str) -> None:
    content = git(checkout, "show", f"refs/tags/{tag}:{path}")
    for needle in needles:
        if needle not in content:
            fail(f"{tag}:{path} is missing required text: {needle}")


def go_function_source(source: str, declaration: str, location: str) -> str:
    """Returns one gofmt-formatted top-level Go function from its unique declaration line through its closing brace."""
    lines = source.splitlines()
    starts = [index for index, line in enumerate(lines) if line.startswith(declaration)]
    if not starts:
        fail(f"{location} has no function declared as `{declaration}`")
    if len(starts) > 1:
        fail(f"{location} declares `{declaration}` {len(starts)} times")
    start = starts[0]
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line == "}":
            return "\n".join(lines[start : index + 1])
        # gofmt indents bodies, so only a multi-line signature's ") ... {" may also start at column 0.
        if line and not line[0].isspace() and not line.startswith(")"):
            break
    fail(f"{location} function `{declaration}` has no column-0 closing brace before the next declaration")


def require_tagged_go_function_text(checkout: Path, tag: str, path: str, declaration: str, *needles: str) -> None:
    location = f"{tag}:{path}"
    function = go_function_source(git(checkout, "show", f"refs/tags/{tag}:{path}"), declaration, location)
    for needle in needles:
        if needle not in function:
            fail(f"{location} function `{declaration}` is missing required text: {needle!r}")


def rust_function_source(source: str, declaration: str, location: str) -> str:
    lines = source.splitlines()
    starts = [index for index, line in enumerate(lines) if line.startswith(declaration)]
    if len(starts) != 1:
        fail(f"{location} must declare `{declaration}` exactly once; found {len(starts)}")
    start = starts[0]
    indentation = declaration[: len(declaration) - len(declaration.lstrip())]
    for index in range(start + 1, len(lines)):
        if lines[index] == indentation + "}":
            return "\n".join(lines[start : index + 1])
    fail(f"{location} function `{declaration}` has no matching closing brace")


def require_tagged_rust_function_text(checkout: Path, tag: str, path: str, declaration: str, *needles: str) -> None:
    location = f"{tag}:{path}"
    function = rust_function_source(git(checkout, "show", f"refs/tags/{tag}:{path}"), declaration, location)
    for needle in needles:
        if needle not in function:
            fail(f"{location} function `{declaration}` is missing required text: {needle!r}")


def python_class_source(source: str, declaration: str, location: str) -> str:
    """Returns one top-level Python class from its unique declaration line up to the next column-0 line."""
    lines = source.splitlines()
    starts = [index for index, line in enumerate(lines) if line.startswith(declaration)]
    if len(starts) != 1:
        fail(f"{location} must declare `{declaration}` exactly once; found {len(starts)}")
    start = starts[0]
    end = next(
        (index for index in range(start + 1, len(lines)) if lines[index] and not lines[index][0].isspace()),
        len(lines),
    )
    return "\n".join(lines[start:end])


def require_block_text(block: str, location: str, declaration: str, needles: tuple[str, ...], forbidden: tuple[str, ...] = ()) -> None:
    """Requires every needle and no forbidden lowercase fragment in one extracted declaration block."""
    for needle in needles:
        if needle not in block:
            fail(f"{location} declaration `{declaration}` is missing required text: {needle!r}")
    for fragment in forbidden:
        if fragment in block.lower():
            fail(f"{location} declaration `{declaration}` unexpectedly contains: {fragment!r}")


def check_server_zd_statements(checkout: Path, current_release: str, statements) -> None:
    """Checks each server-zd statement at the release the references cite and at the bundled release."""
    for cited_release, path, needles in statements:
        for tag in dict.fromkeys((cited_release or current_release, current_release)):
            require_tag(checkout, tag)
            require_tagged_text(checkout, tag, path, *needles)


def check_go_describe_flow(checkout: Path, tag: str, references: Path) -> None:
    client_path = "sdk-go/dex/client.go"
    require_tagged_go_function_text(
        checkout, tag, client_path, "type FlowInfo struct {",
        "FlowID string", "RunID string", "FlowType string", "Status FlowStatus",
        "StartedAt time.Time", "ClosedAt time.Time",
    )
    require_tagged_go_function_text(
        checkout, tag, client_path,
        "func (client *Client) validateCall(ctx context.Context) error {",
        "if ctx == nil {", "closed := client.closed", "if closed {\n\t\treturn errClientClosed",
        "if err := ctx.Err(); err != nil {\n\t\treturn err",
    )
    require_tagged_go_function_text(
        checkout, tag, client_path,
        "func (client *Client) validateFlowCall(ctx context.Context, flowID string) error {",
        "client.validateCall(ctx)", 'strings.TrimSpace(flowID) == ""',
    )
    require_tagged_go_function_text(
        checkout, tag, client_path,
        GO_DESCRIBE_FLOW_SIGNATURE + " {",
        "client.validateFlowCall(ctx, flowID)",
        "client.service.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})",
        'translateRPCError(err, "DescribeFlow", flowID, flowTargetExisting)',
        "return mapFlowInfo(response)",
    )
    require_tagged_go_function_text(
        checkout, tag, "sdk-go/dex/errors.go", "func translateRPCError(",
        "case ErrorSubStatusFlowNotFound:\n"
        "\t\tswitch requirement {\n"
        "\t\tcase flowTargetExisting:\n"
        "\t\t\treturn &FlowNotFoundError{ServiceError: serviceError}",
    )
    require_tagged_go_function_text(
        checkout, tag, "sdk-go/dex/errors.go", "func mapErrorSubStatus(",
        "case dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS:\n\t\treturn ErrorSubStatusFlowNotFound",
    )
    require_tagged_text(
        checkout, tag, client_path,
        "// TimeTravel creates a new run from a selected point in an existing Flow's history.",
        "The returned run ID identifies the new execution.",
        "// FlowCompleted means the Flow completed successfully.\n\tFlowCompleted\n",
    )
    require_tagged_go_function_text(
        checkout, tag, "sdk-go/dex/proto_mapper.go",
        "func mapFlowInfo(response *dexpb.GetFlowSummaryResponse) (FlowInfo, error) {",
        'response.FlowExecutionId.FlowId == "" || response.FlowExecutionId.RunId == ""',
        'response.FlowType == "" || response.StartTime == nil',
        "mapFlowStatus(response.FlowStatus)", "response.StartTime.CheckValid()",
        "closedAt := time.Time{}", "if response.CloseTime != nil {",
        "response.CloseTime.CheckValid()", "closedAt = response.CloseTime.AsTime()",
        "FlowID:    response.FlowExecutionId.FlowId", "RunID:     response.FlowExecutionId.RunId",
        "FlowType:  response.FlowType", "Status:    status",
        "StartedAt: response.StartTime.AsTime()", "ClosedAt:  closedAt",
    )
    require_text(
        references / "go" / "observability.md",
        f"From Go SDK v1.21.0, `{GO_DESCRIBE_FLOW_SIGNATURE}` reads the current or latest run",
        "`FlowInfo` has `FlowID string` (application-assigned ID), `RunID string` (server-assigned run ID), "
        "`FlowType string`, `Status FlowStatus`, `StartedAt time.Time`, and `ClosedAt time.Time`.",
        "A zero `ClosedAt` means the run is open or its close time is unavailable.",
        "On the Go Server, time travel creates a new run ID, so `RunID` can differ from the run ID `StartFlow` returned.",
        "server-zd keeps the run ID across time travel.",
        "The method rejects a nil, canceled, or expired context, a closed Client, or an empty or whitespace-only Flow ID "
        "before the RPC.",
        "A missing Flow returns `*dex.FlowNotFoundError`",
        "uses `GetFlowSummary` with the Flow ID and no run ID",
    )
    require_section_text(
        references / "core" / "error-handling.md", "## Closed-Flow races",
        "For that status read, or another one-off status check of a known Flow ID, call the SDK's DescribeFlow API: "
        "Go **Client.DescribeFlow** (from Go SDK v1.21.0)",
        "Each reads the current or latest run without waiting.",
        "Only the Go result also carries the close time, when available.",
        "Both the Go Server and server-zd serve the underlying **GetFlowSummary** RPC.",
        "Do not call it after a missing-target error at a query-only Get boundary.",
    )
    require_section_text(references / "go" / "observability.md", "## Correlation and signals", "For a stall: confirm IDs/status;")
    require_section_text(references / "go" / "observability.md", "## Describe a Flow", GO_DESCRIBE_FLOW_SIGNATURE)
    require_text(
        references / "go" / "error-handling.md",
        "Call `Client.DescribeFlow` from Go SDK v1.21.0", "`*dex.FlowNotFoundError`",
        f"Treat `dex.FlowCompleted`{COMPLETED_STATUS_RULE}",
    )
    require_tagged_text(
        checkout, tag, "sdk-go/README.md",
        "`DescribeFlow(ctx, flowID)` reads one known Flow's current or latest run without",
        "Use `WaitForFlow` when the caller needs to wait",
        "and `SearchFlows` to find Flow IDs by query.",
    )
    require_tagged_text(
        checkout, tag, "docs/content/primitives/client/advanced.mdx",
        "The Go SDK also returns the close time when available.",
    )


def check_flow_summary_rpc(checkout: Path, server_tag: str, server_zd_tag: str) -> None:
    require_tagged_go_function_text(
        checkout, server_tag, API_SERVICE_PATH,
        "func (s *serviceImpl) GetFlowSummary(",
        'req == nil || req.GetFlowId() == ""',
        "s.client.DescribeWorkflowExecution(",
        "req.GetFlowId()", "req.GetRunId()",
        "FlowId: req.GetFlowId(),\n\t\t\tRunId:  description.RunId,",
        "FlowType:   description.IndexedAttributes[service.SearchAttributeDexWorkflowType].GetStringValue(),",
        "FlowStatus: description.Status", "StartTime:  timestamppb.New(description.StartTime)",
        "if description.CloseTime != nil {\n\t\tresponse.CloseTime = timestamppb.New(*description.CloseTime)",
        "return nil, s.handleError(err)",
    )
    require_tagged_go_function_text(
        checkout, server_tag, API_SERVICE_PATH,
        "func (s *serviceImpl) handleError(",
        "if s.client.IsNotFoundError(err) {\n\t\treturn serviceerrors.NotFound(err.Error()).ToGRPCError()",
    )
    require_tagged_go_function_text(
        checkout, server_tag, API_SERVICE_PATH,
        "func (s *serviceImpl) ResetFlow(",
        "runId, err := s.client.ResetWorkflow(ctx, req)", "return &dexpb.ResetFlowResponse{RunId: runId}, nil",
    )
    require_tagged_go_function_text(
        checkout, server_tag, SERVER_ERROR_STATUS_PATH,
        "func NotFound(",
        "NewErrorAndStatus(codes.NotFound, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS, details)",
    )
    require_tagged_rust_function_text(
        checkout, server_zd_tag, "server-zd/crates/dexzd-api/src/flow_service.rs",
        "    async fn get_flow_summary(",
        ".get_flow_summary(TenantId::DEFAULT, request.into_inner())",
    )
    require_tagged_rust_function_text(
        checkout, server_zd_tag, "server-zd/crates/dexzd-api/src/runtime_flow_operations.rs",
        "    async fn get_flow_summary(",
        "FlowRuntimeRegistry::get_flow_summary(self, tenant_id, request)",
    )
    require_tagged_rust_function_text(
        checkout, server_zd_tag, SERVER_ZD_READ_OPERATIONS_PATH,
        "    pub async fn get_flow_summary(",
        "let run = read_run(committed.as_ref(), context, &flow, &request.run_id)?;",
        "flow_id: request.flow_id,\n                run_id: run_id_text.clone(),",
        "// Time travel keeps the run ID",
        "flow_type: record.flow_type,", "flow_status: record.status,",
        "start_time: Some(timestamp_from_micros(record.started_at_micros)),",
        "close_time: (record.closed_at_micros > 0)\n                .then(|| timestamp_from_micros(record.closed_at_micros)),",
    )
    require_tagged_rust_function_text(
        checkout, server_zd_tag, SERVER_ZD_STATE_ROWS_PATH,
        "pub(crate) fn resolve_command_run(",
        "let newest = directory.and_then(|directory| directory.runs.last().cloned());",
        "let Some(newest) = newest else {\n        return Err(RuntimeError::flow_not_found(&flow.flow_id));",
    )
    require_tagged_rust_function_text(
        checkout, server_zd_tag, SERVER_ZD_STATE_ROWS_PATH,
        "pub(crate) fn read_run(",
        "let run_id = resolve_command_run(state, context, flow, run_id)?;",
        "&& !record.is_purging =>", "_ => Err(RuntimeError::flow_not_found(&flow.flow_id)),",
    )
    require_tagged_rust_function_text(
        checkout, server_zd_tag, SERVER_ZD_RUNTIME_ERROR_PATH,
        "    pub fn flow_not_found(",
        "grpc_code::NOT_FOUND,\n            ErrorSubStatus::FlowNotExists,",
    )
    require_tagged_text(
        checkout, server_zd_tag, SERVER_ZD_TIME_TRAVEL_TEST_PATH,
        "ResetFlowResponse {\n            run_id: run_id.clone(),\n            run_attempt: 2,\n        }",
    )


def check_server_zd_search_tls(checkout: Path, tag: str, references: Path) -> None:
    require_tagged_text(checkout, tag, POSTGRES_CONNECTION_PATH, "const DEFAULT_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);")
    # The documented `prefer` plaintext fallback is tokio-postgres 0.7.18 SslMode::Prefer behavior.
    require_tagged_text(checkout, tag, "server-zd/Cargo.lock", 'name = "tokio-postgres"\nversion = "0.7.18"')
    require_tagged_text(
        checkout, tag, "server-zd/README.md",
        "`search.paradeDbConnectionString`", "`sslmode` (`disable`, `allow`, `prefer`, `require`, `verify-ca`,",
        "`sslrootcert` (a PEM CA file or `system`)", "`sslsni` (`0` or `1`)",
        "`sslnegotiation` (`postgres` or `direct`), and `connect_timeout` in seconds. Direct negotiation\n"
        "  requires PostgreSQL 17 or later.",
        "The connect timeout covers the TLS handshake",
        "Without TLS parameters, the connection remains plaintext",
    )
    require_tagged_text(
        checkout, tag, "docs/content/production/server-zd-operations.mdx",
        "**search.paradeDbConnectionString**", "**sslmode=require** without **sslrootcert**",
        "**sslmode=verify-full** verifies the chain and the host name",
    )
    require_tagged_rust_function_text(
        checkout, tag, SEARCH_PROJECTION_PATH,
        "fn postgres_configuration(search: &SearchConfig)",
        "ConnectionSettings::parse_for_search(", "&search.parade_db_connection_string",
    )
    require_tagged_rust_function_text(
        checkout, tag, SEARCH_PROJECTION_PATH,
        "    pub(crate) async fn connect(search: &SearchConfig)",
        "let configuration = postgres_configuration(search)?;",
        "ParadeDbSearchBackend::connect_with_settings(\n                &configuration,",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_CONNECTION_PATH,
        "    pub fn parse_for_search(dsn: &str)",
        "split_tls_parameters(dsn)", 'value != "0" && value != "1"',
        "Self::from_parsed_dsn(remaining, parameters, MissingSslMode::Disable)",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_DSN_PATH,
        "pub(crate) fn split_tls_parameters(dsn: &str)",
        '"sslmode" => tls.ssl_mode = Some(parameter.value)',
        '"sslrootcert" => tls.ssl_root_cert = Some(parameter.value)',
        '"sslsni" => tls.ssl_sni = Some(parameter.value)',
        '"sslnegotiation" => {\n'
        "                tls.ssl_negotiation = Some(parameter.value);\n"
        "                kept.push(raw);",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_CONNECTION_PATH,
        "    fn from_parsed_dsn(",
        "ConnectionSettingsError::InvalidDsn(describe_config_parse_error(&error))",
        "let (ssl_mode, verification) = resolve_tls(&parameters, missing_ssl_mode)",
        'parameters.ssl_negotiation.as_deref() == Some("direct")',
        'parameters.ssl_sni.as_deref() != Some("0")',
        "client_config(verification, is_direct, is_sni_enabled)",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_CONNECTION_PATH,
        "fn resolve_tls(",
        "MissingSslMode::Disable\n"
        "                if parameters.ssl_negotiation.is_some()\n"
        "                    || parameters.ssl_root_cert.is_some()\n"
        "                    || parameters.ssl_sni.is_some() =>\n"
        "            {\n"
        '                "prefer".to_owned()\n'
        "            }",
        "MissingSslMode::Disable => \"disable\".to_owned()",
        'Some("direct") && ssl_mode == "prefer" {\n'
        '        ssl_mode = "require".to_owned();',
        'Some("direct") && ssl_mode == "allow" {\n'
        '        return Err("sslmode=allow is invalid with sslnegotiation=direct".to_owned());',
        'Some(ssl_root_cert) => {\n'
        '            if ssl_root_cert == "system" {\n'
        '                ssl_mode = "verify-full".to_owned();\n'
        '            }\n'
        '            Some(load_root_certificates(ssl_root_cert)?)',
        '"disable" => Ok((SslMode::Disable, None))',
        '"allow" | "prefer" => Ok((SslMode::Prefer, Some(CertificateVerification::None)))',
        '"require" => match roots {\n'
        "            Some(roots) => Ok((\n"
        "                SslMode::Require,\n"
        "                Some(CertificateVerification::ChainOnly(roots)),\n"
        "            )),\n"
        "            None => Ok((SslMode::Require, Some(CertificateVerification::None))),",
        '"verify-ca" => {\n'
        "            let roots = roots.map_or_else(system_roots, Ok)?;\n"
        "            Ok((\n"
        "                SslMode::Require,\n"
        "                Some(CertificateVerification::ChainOnly(roots)),",
        '"verify-full" => {\n'
        "            let roots = roots.map_or_else(system_roots, Ok)?;\n"
        "            Ok((SslMode::Require, Some(CertificateVerification::Full(roots))))",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_CONNECTION_PATH,
        "    pub async fn connect(&self)",
        ".get_connect_timeout()", "unwrap_or(DEFAULT_CONNECT_TIMEOUT)",
        "self.config.connect(tls.clone()).await?", "tokio::time::timeout(timeout, connecting)",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_TLS_PATH,
        "pub(crate) fn client_config(",
        "CertificateVerification::Full(roots)",
        "builder.with_root_certificates(roots).with_no_client_auth()",
        "CertificateVerification::ChainOnly(roots)",
        "ChainOnlyVerifier { inner: verifier }",
        "CertificateVerification::None", "UnverifiedCertificate",
        'config.alpn_protocols = vec![b"postgresql".to_vec()]',
        "config.enable_sni = is_sni_enabled",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_TLS_PATH,
        "impl ServerCertVerifier for ChainOnlyVerifier {",
        "match self.inner.verify_server_cert(",
        "Err(Error::InvalidCertificate(\n"
        "                CertificateError::NotValidForName | CertificateError::NotValidForNameContext { .. },\n"
        "            )) => Ok(ServerCertVerified::assertion()),\n"
        "            result => result,",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_TLS_PATH,
        "pub(crate) fn load_root_certificates(",
        'if ssl_root_cert == "system"',
        "rustls_native_certs::load_native_certs()",
        "CertificateDer::pem_file_iter(ssl_root_cert)",
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_CONNECTION_PATH,
        "fn describe_config_parse_error(",
        "VALUE_CHECKED_KEYWORDS",
        '|| "connection string has an unsupported keyword or is malformed".to_owned(),',
        '|keyword| format!("connection string keyword {keyword} has an invalid value"),',
    )
    require_tagged_rust_function_text(
        checkout, tag, POSTGRES_CONNECTION_PATH,
        "pub fn error_with_sources(", "error.source()", "cause.to_string()", "source = cause.source()",
    )
    require_tagged_rust_function_text(
        checkout, tag, SEARCH_BACKEND_PATH,
        "    pub(super) async fn open(configuration: &ConnectionSettings)",
        "configuration.connect().await", "classify_database_error(\"connect to ParadeDB\", source)",
    )
    require_tagged_rust_function_text(
        checkout, tag, SEARCH_BACKEND_PATH,
        "    pub(super) async fn reconnect(",
        "let replacement = Self::open(configuration).await?;", "*self = replacement;",
    )
    require_tagged_rust_function_text(
        checkout, tag, SEARCH_BACKEND_PATH,
        "    pub async fn connect_with_settings(",
        "ConnectionSlot::open(configuration).await?", "configuration: configuration.clone(),",
    )
    require_tagged_rust_function_text(
        checkout, tag, SEARCH_BACKEND_PATH,
        "pub(super) fn database_error_message(error: &tokio_postgres::Error)",
        "error_with_sources(error)",
    )
    require_tagged_text(
        checkout, tag, SEARCH_BACKEND_PATH,
        'assert!(!message.contains(&dsn)', 'assert!(!message.contains("hidden-password")',
    )
    require_text(
        references / "core" / "operations.md",
        "From server-zd v0.1.6, `search.paradeDbConnectionString` accepts PostgreSQL URL or keyword-string TLS parameters: "
        "`sslmode=disable|allow|prefer|require|verify-ca|verify-full`, `sslrootcert` as a PEM CA file or `system` roots, "
        "`sslsni=0|1`, and `sslnegotiation=postgres|direct`.",
        "Direct negotiation requires PostgreSQL 17 or later; `sslmode=allow` with `sslnegotiation=direct` is invalid.",
        "Explicit `sslmode=prefer` first attempts TLS without certificate verification and uses plaintext when the server declines TLS.",
        "A string without TLS parameters stays plaintext.",
        "Always set `sslmode` explicitly. Without it, any other TLS parameter (an `sslrootcert` file, `sslsni`, or "
        "`sslnegotiation=postgres`) selects `prefer`: TLS without certificate verification, with plaintext fallback, "
        "even when `sslrootcert` names a CA file.",
        "`sslrootcert=system` instead forces verify-full.",
        "server-zd still reads that file at startup but never uses it to verify.",
        "With `sslnegotiation=direct`, a missing or `prefer` mode becomes `require`, which never falls back to plaintext.",
        "`sslmode=allow` behaves like `prefer` and tries TLS first, unlike libpq and pgx, which try plaintext first.",
        "`sslmode=verify-ca` verifies the certificate chain but not the host name, using system roots when `sslrootcert` is absent.",
        "For a verified connection, use `sslmode=verify-full` with `sslrootcert`, or `sslrootcert=system`.",
        "`sslmode=require` without `sslrootcert` encrypts without verifying the server certificate; with a PEM `sslrootcert` file it verifies the certificate chain but not the host name.",
        "`sslmode=verify-full` verifies the chain and host name against the supplied roots, or system roots when `sslrootcert` is absent. `sslrootcert=system` forces verify-full.",
        "`connect_timeout` is in seconds and bounds the TLS handshake and authentication, including reconnects; without it, the connection timeout is ten seconds.",
        "Connection errors include TLS and I/O causes but never the connection string.",
    )


def check_go_server_close_cleanup(checkout: Path, tag: str) -> None:
    """Checks the Go Server Flow close cleanup rejection set and its backend routing in the tagged source."""
    require_tagged_go_function_text(
        checkout,
        tag,
        WORKFLOW_UPDATER_PATH,
        "func NewWorkflowUpdater(",
        *(f"updater.{validator},\n\t\tupdater.{handler}," for validator, handler in CLEANUP_REJECTING_UPDATES),
    )
    for validator, _ in CLEANUP_REJECTING_UPDATES:
        require_tagged_go_function_text(
            checkout,
            tag,
            WORKFLOW_UPDATER_PATH,
            f"func (u *WorkflowUpdater) {validator}(",
            TERMINAL_UPDATE_GUARD,
        )
    require_tagged_go_function_text(
        checkout,
        tag,
        WORKFLOW_UPDATER_PATH,
        "func (u *WorkflowUpdater) rejectTerminalUpdate(",
        "if !u.terminalCoordinator.HasStartedFinalizing() {",
        "dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION,\n\t\tservice.FlowTerminalCleanupInProgressMessage,",
    )
    require_tagged_go_function_text(
        checkout,
        tag,
        API_SERVICE_PATH,
        "func (s *serviceImpl) handleError(",
        "case dexpb.UpdateErrorType_UPDATE_ERROR_TYPE_FAILED_PRECONDITION:\n"
        "\t\t\tsubStatus := dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED\n"
        "\t\t\tif details == service.FlowTerminalCleanupInProgressMessage {\n"
        "\t\t\t\tsubStatus = dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_CLOSING\n"
        "\t\t\t}\n"
        "\t\t\treturn serviceerrors.NewErrorAndStatus(\n"
        "\t\t\t\tcodes.FailedPrecondition,\n"
        "\t\t\t\tsubStatus,",
    )
    require_tagged_go_function_text(
        checkout,
        tag,
        API_SERVICE_PATH,
        "func (s *serviceImpl) shouldInvokeRPCTransactionally(",
        "return s.client.GetBackendType() == service.BackendTypeTemporal &&\n"
        "\t\t(req.GetIsTransactional() || len(req.GetLockAttributeKeys()) > 0 ||\n"
        "\t\t\ts.apiCfg.UseTemporalSynchronousUpdateForAllRPCs)",
    )
    require_tagged_go_function_text(
        checkout,
        tag,
        API_SERVICE_PATH,
        "func (s *serviceImpl) InvokeRPC(",
        "if len(req.GetLockAttributeKeys()) > 0 && backendType == service.BackendTypeCadence {\n"
        "\t\treturn nil, status.Errorf(codes.Unimplemented,",
        "isTransactional := s.shouldInvokeRPCTransactionally(req)",
    )
    for wait in ("WaitForStepCompletion", "WaitForAttribute"):
        require_tagged_go_function_text(
            checkout, tag, API_SERVICE_PATH,
            f"func (s *serviceImpl) {wait}(",
            "if s.client.GetBackendType() == service.BackendTypeCadence {\n"
            f'\t\treturn nil, status.Errorf(codes.Unimplemented, "{wait} requires Temporal synchronous update")',
        )
    require_tagged_go_function_text(
        checkout,
        tag,
        API_SERVICE_PATH,
        "func (s *serviceImpl) DeleteChannelMessage(",
        "if s.client.GetBackendType() == service.BackendTypeCadence {\n"
        "\t\treturn s.deleteCadenceChannelMessage(",
    )
    require_tagged_go_function_text(
        checkout,
        tag,
        API_SERVICE_PATH,
        "func (s *serviceImpl) deleteCadenceChannelMessage(",
        "s.client.SignalWorkflow(",
    )
    require_tagged_go_function_text(
        checkout, tag, TERMINAL_COORDINATOR_PATH,
        "func (c *TerminalCoordinator) CoordinateAndFinalizeError(",
        "c.startedFinalizing = true",
        "c.provider.Await(ctx, func() bool {\n"
        "\t\treturn c.attributeSyncer.ProducersDrained() && c.continueAsNewer.inflightUpdateOperations == 0",
        "c.attributeSyncer.FlushAndClose(ctx)",
    )


def check_sdk_flow_closing_errors(checkout: Path, baselines: dict[str, str], references: Path) -> None:
    """Checks each SDK's tagged typed Flow close cleanup error and that its reference page uses the same name."""
    go_error = "FlowClosingError"
    go_sub_status = "ErrorSubStatusFlowClosing"
    require_tagged_text(checkout, baselines["go"], "sdk-go/dex/errors.go", f"type {go_error} struct", go_sub_status)
    require_text(references / "go" / "error-handling.md", f"`*dex.{go_error}`", f"`dex.{go_sub_status}`")

    java_package = "io.superdurable.dex.exceptions"
    java_error = "FlowClosingException"
    require_tagged_text(
        checkout,
        baselines["java"],
        f"sdk-java/src/main/java/{java_package.replace('.', '/')}/{java_error}.java",
        f"package {java_package};",
        f"public final class {java_error} ",
    )
    require_text(references / "java" / "error-handling.md", f"`{java_error}` from `{java_package}`")

    python_error = "FlowClosingError"
    require_tagged_text(checkout, baselines["python"], "sdk-python/dex/runtime_errors.py", f"class {python_error}(")
    require_tagged_text(checkout, baselines["python"], "sdk-python/dex/__init__.py", f'"{python_error}"')
    require_text(references / "python" / "error-handling.md", f"`{python_error}` from `dex`")

    rust_variant = "FlowClosing"
    require_tagged_text(checkout, baselines["rust"], "sdk-rust/crates/dex-sdk/src/sdk_error.rs", f"    {rust_variant} {{")
    require_tagged_text(
        checkout,
        baselines["rust"],
        "sdk-rust/crates/dex-sdk/src/lib.rs",
        "pub use sdk_error::{ErrorSubStatus, SdkError",
    )
    require_text(references / "rust" / "error-handling.md", f"`SdkError::{rust_variant} {{ .. }}`")

    typescript_error = "FlowClosingError"
    require_tagged_text(
        checkout,
        baselines["typescript"],
        "sdk-typescript/src/errors.ts",
        f"export class {typescript_error} ",
    )
    require_tagged_text(checkout, baselines["typescript"], "sdk-typescript/src/index.ts", 'export * from "./errors.js"')
    require_text(references / "typescript" / "error-handling.md", f"`instanceof {typescript_error}`")


def require_tagged_block(
    checkout: Path, tag: str, path: str, declaration: str, needles: tuple[str, ...], forbidden: tuple[str, ...] = (),
) -> None:
    """Checks one brace-delimited (Rust/TypeScript), Python class, or whole-file ("") block at a tag."""
    location = f"{tag}:{path}"
    source = git(checkout, "show", f"refs/tags/{tag}:{path}")
    if not declaration:
        block = source
    elif path.endswith(".py"):
        block = python_class_source(source, declaration, location)
    else:
        block = rust_function_source(source, declaration, location)
    require_block_text(block, location, declaration or "<file>", needles, forbidden)


def check_sdk_describe_flow(checkout: Path, baselines: dict[str, str], references: Path) -> None:
    """Checks each non-Go SDK's tagged DescribeFlow call, its close-time-free FlowInfo, and its reference names."""
    java = baselines["java"]
    java_root = "sdk-java/src/main/java/io/superdurable/dex"
    require_tagged_block(checkout, java, f"{java_root}/Client.java", "", (
        "    public FlowInfo describeFlow(final String flowId) {\n"
        "        final GetFlowSummaryResponse response = call(() -> service.getFlowSummary(\n"
        "                GetFlowSummaryRequest.newBuilder().setFlowId(flowId).build()),\n"
        "                FlowTargetRequirement.EXISTING,",
    ))
    require_tagged_block(checkout, java, f"{java_root}/FlowInfo.java", "", (
        "    private final String flowId;\n    private final String runId;\n    private final String flowType;\n"
        "    private final FlowStatus status;\n    private final Instant startedAt;\n\n    FlowInfo(",
        "public Instant getStartedAt()",
    ), ("close",))
    require_tagged_block(checkout, java, f"{java_root}/GrpcExceptionTranslator.java", "", (
        "if (requirement == FlowTargetRequirement.EXISTING) {\n            return new FlowNotFoundException(",
    ))
    require_tagged_block(checkout, java, f"{java_root}/FlowStatus.java", "", ("    COMPLETED,",))
    require_text(
        references / "java" / "error-handling.md",
        "`Client.describeFlow(flowId)` reads the current or latest run without waiting.",
        "It returns `FlowInfo` with `getFlowId()`, `getRunId()`, `getFlowType()`, `getStatus()`, and `getStartedAt()`; "
        "it has no close time.",
        f"Treat `COMPLETED`{COMPLETED_STATUS_RULE}", "`FlowNotFoundException`",
    )

    python = baselines["python"]
    python_request = (
        'pb.GetFlowSummaryRequest(flow_id=require_name(flow_id)),\n'
        '                "describe_flow",\n                flow_id,\n                "existing",'
    )
    require_tagged_block(checkout, python, "sdk-python/dex/client.py", "", (
        "    def describe_flow(self, flow_id: str) -> FlowInfo:", python_request,
    ))
    require_tagged_block(checkout, python, "sdk-python/dex/async_client.py", "", (
        "    async def describe_flow(self, flow_id: str) -> FlowInfo:", python_request,
    ))
    require_tagged_block(checkout, python, "sdk-python/dex/flow_info.py", "class FlowInfo:", (
        "    flow_id: str\n    run_id: str\n    flow_type: str\n    status: FlowStatus\n    started_at: datetime",
    ), ("close",))
    require_tagged_block(checkout, python, "sdk-python/dex/flow_info.py", "class FlowStatus(", ('COMPLETED = "completed"',))
    require_tagged_block(checkout, python, "sdk-python/dex/_grpc_errors.py", "", (
        'if sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:\n        if requirement == "existing":\n'
        "            return FlowNotFoundError(*parameters)",
    ))
    require_text(
        references / "python" / "error-handling.md",
        "Call `Client.describe_flow(flow_id)` or `await AsyncClient.describe_flow(flow_id)`",
        "It returns `FlowInfo` with `flow_id`, `run_id`, `flow_type`, `status`, and `started_at`; it has no close time.",
        f"Treat `FlowStatus.COMPLETED`{COMPLETED_STATUS_RULE}", "`FlowNotFoundError`",
    )

    typescript = baselines["typescript"]
    require_tagged_block(
        checkout, typescript, "sdk-typescript/src/client.ts", "  public async describeFlow(flowId: string): Promise<FlowInfo> {", (
            '{ operation: "describeFlow", flowId, requirement: "existing" }',
            'this.service.getFlowSummary({ flowId: requireName(flowId), runId: "" }, callback)',
            "throw new TypeError(",
        ),
    )
    require_tagged_block(checkout, typescript, "sdk-typescript/src/options.ts", "export interface FlowInfo {", (
        "readonly flowId: string;", "readonly runId: string;", "readonly flowType: string;",
        "readonly status: FlowStatus;", "readonly startedAt: Date;",
    ), ("close",))
    require_tagged_block(checkout, typescript, "sdk-typescript/src/options.ts", "", ('  | "completed"',))
    require_tagged_block(checkout, typescript, "sdk-typescript/src/grpc-status.ts", "", (
        'if (requirement === "existing") {\n        return new FlowNotFoundError(',
    ))
    require_text(
        references / "typescript" / "error-handling.md",
        "Call `await client.describeFlow(flowId)`",
        "It resolves to `FlowInfo` with `flowId`, `runId`, `flowType`, `status`, and `startedAt`; it has no close time.",
        f'Treat status `"completed"`{COMPLETED_STATUS_RULE}', "`FlowNotFoundError`",
        "An incomplete server summary rejects with `TypeError`.",
    )

    rust = baselines["rust"]
    rust_root = "sdk-rust/crates/dex-sdk/src"
    require_tagged_block(
        checkout, rust, f"{rust_root}/client.rs", "    pub fn describe_flow(&self, flow_id: &str) -> SdkResult<FlowInfo> {", (
            "self.runtime.block_on(",
            ".get_flow_summary(GetFlowSummaryRequest {\n"
            "                    flow_id: flow_id.to_string(),\n                    run_id: String::new(),",
            "FlowTargetRequirement::Existing",
        ),
    )
    require_tagged_block(checkout, rust, f"{rust_root}/flow_info.rs", "pub struct FlowInfo {", (
        "pub flow_id: String,", "pub run_id: String,", "pub flow_type: String,",
        "pub status: FlowStatus,", "pub started_at: SystemTime,",
    ), ("close",))
    require_tagged_block(checkout, rust, f"{rust_root}/flow_info.rs", "pub enum FlowStatus {", ("    Completed,",))
    require_tagged_block(checkout, rust, f"{rust_root}/sdk_error.rs", "", (
        "FlowTargetRequirement::Existing => Self::FlowNotFound { service },",
    ))
    require_text(
        references / "rust" / "error-handling.md",
        "Call `client.describe_flow(flow_id)`", "It is a blocking call",
        "It returns `SdkResult<FlowInfo>` with `flow_id`, `run_id`, `flow_type`, `status`, and `started_at`; "
        "it has no close time.",
        f"Treat `FlowStatus::Completed`{COMPLETED_STATUS_RULE}", "`SdkError::FlowNotFound { .. }`",
    )

    require_text(
        references / "core" / "error-handling.md",
        "Java **Client.describeFlow**, Python **Client.describe_flow** or **AsyncClient.describe_flow**, "
        "TypeScript **Client.describeFlow**, or Rust **Client::describe_flow**.",
        "Java, Python, TypeScript, and Rust already have it at their v1.20.0 baselines.",
        "It returns the Flow ID, run ID, Flow type, status, and start time.",
        "A missing Flow returns the SDK's typed Flow-not-found error.",
    )


def require_ancestor(checkout: Path, ancestor: str, descendant: str, label: str) -> None:
    result = subprocess.run(
        ["git", "merge-base", "--is-ancestor", ancestor, descendant],
        cwd=checkout,
        check=False,
    )
    if result.returncode != 0:
        fail(f"{label} {ancestor} is not an ancestor of {descendant}")


def require_tag(checkout: Path, tag: str) -> None:
    result = subprocess.run(
        ["git", "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}"],
        cwd=checkout,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        fail(f"Required release tag {tag} is unavailable in {checkout}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dex-root", required=True, type=Path)
    arguments = parser.parse_args()

    server_baseline = (ROOT / "DEX_SERVER_BASELINE").read_text().strip()
    cli_baseline = (ROOT / "DEX_CLI_BASELINE").read_text().strip()
    go_sdk_baseline = (ROOT / "DEX_BASELINE").read_text().strip()
    sdk_baselines = {
        "go": go_sdk_baseline,
        **{
            language: (ROOT / f"DEX_{language.upper()}_BASELINE").read_text().strip()
            for language in ("java", "python", "rust", "typescript")
        },
    }
    require_revision(arguments.dex_root, cli_baseline, "Dex CLI")
    require_ancestor(arguments.dex_root, server_baseline, cli_baseline, "Dex Server baseline")
    require_tag(arguments.dex_root, SERVER_ZD_RELEASE)

    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "v2_directives.go",
        "GetDexSummary",
        "GetDexDisplay",
        "dex:group",
        "RPCOptions.Action",
        "ActionRequiresPermission",
        '"ui-slot"',
        '"capture"',
        '"qr-code"',
        "capture requires a source:user string input field",
    )
    require_text(
        arguments.dex_root / "cli" / "schema" / "flow-definition-graph.v2.schema.json",
        '"capture"',
        '"qr-code"',
    )
    require_text(
        arguments.dex_root / "cli" / "README.md",
        "--schema-version 2.0",
        "Go-only",
    )
    require_text(
        arguments.dex_root / "web" / "api" / "v2.go",
        "V2DefinitionRevisionHeader",
        'json:"workQueuePermissions"',
        "Action permission denied",
        'json:"capture,omitempty"',
    )
    require_text(
        arguments.dex_root / "web" / "app" / "v2" / "workspace" / "qrScanner.ts",
        "await import('@zxing/browser')",
        "facingMode: { ideal: 'environment' }",
        "for (const track of stream.getTracks()) track.stop()",
    )
    require_text(
        arguments.dex_root / "sdk-go" / "README.md",
        "Once a permission has",
        "remains searchable after completion",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "go_connector_factory.go",
        "parseConnectorFactoryStep",
        "parseConnectorAnnotations",
        r"(?:-rc\.[0-9]+)?",
        '"annotations"',
        '"connectionName"',
        '"moduleVersion"',
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "go_connector_trigger.go",
        "collectConnectorTriggerBindings",
        '"bindingName"',
        "TriggerBindingFactoryConfigMarker",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "dev" / "config.go",
        "connector-config-dir",
        "connector-release-override",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "dev" / "config.go",
        '"engine"',
        '"zd-binary"',
        '"zd-config"',
        '"zd-data-dir"',
        'flags.StringVar(&cfg.ZDConfigPath, "zd-config"',
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "dev" / "zd_process.go",
        '"--attribute-store-config", cfg.AttributeStoreConfigPath',
    )
    require_text(
        arguments.dex_root / "cli" / "README.md",
        "`--attribute-store-config` also works with `--engine zd`",
        "`attributeStore` in the `--zd-config` file.",
        "### Connect over TLS",
        "TLS never falls back to plaintext.",
        "ALPN `h2`",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "command" / "options.go",
        'lookupEnv("DEX_TLS")',
        'lookupEnv("DEX_TLS_CA_FILE")',
        'lookupEnv("DEX_TLS_SERVER_NAME")',
        '"tls-ca-file"',
        '"tls-server-name"',
        "require --tls or DEX_TLS=true",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "command" / "client.go",
        "MinVersion: tls.VersionTLS12",
        "x509.SystemCertPool()",
    )
    check_server_zd_statements(arguments.dex_root, SERVER_ZD_RELEASE, SERVER_ZD_STATEMENTS)
    check_server_zd_search_tls(arguments.dex_root, SERVER_ZD_RELEASE, SDK_REFERENCES)
    check_go_server_close_cleanup(arguments.dex_root, server_baseline)
    check_flow_summary_rpc(arguments.dex_root, server_baseline, SERVER_ZD_RELEASE)
    require_tagged_text(
        arguments.dex_root,
        server_baseline,
        "server/config/config.go",
        'yaml:"useTemporalSynchronousUpdateForAllRPCs"',
    )
    require_tagged_text(
        arguments.dex_root,
        server_baseline,
        "server/service/interpreter/activityImpl.go",
        "recordImplicitStepHeartbeat",
        "a.activityProvider.RecordHeartbeat(ctx",
    )
    require_tagged_text(
        arguments.dex_root,
        server_baseline,
        "server/service/interpreter/pullDispatch.go",
        "deadline, hasDeadline := ctx.Deadline()",
        "Deadline:                 timestamppb.New(deadline)",
    )
    require_tagged_text(
        arguments.dex_root,
        server_baseline,
        "server/service/matching/matching.go",
        "w.cancelTask(request.TaskId)",
    )
    check_sdk_flow_closing_errors(arguments.dex_root, sdk_baselines, SDK_REFERENCES)
    check_go_describe_flow(arguments.dex_root, go_sdk_baseline, SDK_REFERENCES)
    check_sdk_describe_flow(arguments.dex_root, sdk_baselines, SDK_REFERENCES)
    require_tagged_text(
        arguments.dex_root,
        go_sdk_baseline,
        "sdk-go/dex/client.go",
        "func (client *Client) WaitForAttributeMatch(",
        "func (client *Client) WaitForAttributeMapInstanceMatch(",
    )
    require_tagged_text(
        arguments.dex_root,
        go_sdk_baseline,
        "sdk-go/dex/worker_pull.go",
        "context.WithDeadline(taskCtx, task.GetDeadline().AsTime())",
    )
    require_text(
        arguments.dex_root / "web" / "api" / "v2.go",
        '"serverEngine"',
    )
    require_text(
        arguments.dex_root / "web" / "app" / "v2" / "start" / "workerTarget.ts",
        "isAddressWorkerTargetSupported: !isPullOnly",
    )
    require_text(
        arguments.dex_root / "web" / "server.go",
        "checkFlowServiceReadiness",
        "client.HealthCheck",
    )
    require_text(
        arguments.dex_root / "web" / "README.md",
        "### Run against server-zd",
        "?runAttempt=N",
    )
    require_text(
        arguments.dex_root / "sdk-go" / "dex" / "context.go",
        "func (info *RecoveryErrorInfo) IsMethodTimeout() bool",
        "func (info *RecoveryErrorInfo) IsHeartbeatTimeout() bool",
        "func (info *RecoveryErrorInfo) IsRetryTotalDurationExceeded() bool",
    )
    require_text(
        arguments.dex_root / "web" / "connector_connection_store.go",
        "connections.json",
        "TriggerBindings",
        "putTriggerBinding",
        "os.Rename",
    )
    require_text(
        arguments.dex_root / "web" / "connector_oauth.go",
        "code_verifier",
        "connectorOAuthResponseValue",
        "credentials[mapping.Credential]",
        "credentialSecrets",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "go_connector_configuration_ui.go",
        "ConnectorConfigurationUI",
        "ConnectorUIUnit",
        "ConnectorUIBinding",
        "compile-time literals",
    )
    require_text(
        arguments.dex_root / "web" / "connector_studio_command.go",
        "connectorStudioCommandRequest",
        "CONNECTOR_STUDIO_COMMAND_NOT_FOUND",
        "containsConnectorSecret",
        "https",
    )
    require_text(
        arguments.dex_root / "web" / "connector_use_configuration.go",
        "validateConnectorUseConfiguration",
        "allowedPointers",
        "Connector configuration path",
    )
    require_text(
        arguments.dex_root / "web" / "app" / "v2" / "connectors" / "ConnectorsPage.tsx",
        "provider.command.execute",
        "use.configuration.save",
        "connector.frame.resize",
        "Local override",
    )
    require_text(
        arguments.dex_root / "web" / "api" / "v2_start.go",
        "WORKER_UNHEALTHY",
        "BypassWorkerHealthCheck",
        "v2StartStepInput",
        "SkipWaitFor: definition.Start.SkipWaitFor",
        "requestID := uuid.NewString()",
    )
    require_text(
        arguments.dex_root / "web" / "flow_definition_provider.go",
        "filepath.WalkDir",
        "FLOW_DEFINITION_INVALID",
        "FLOW_DEFINITION_SOURCE_UNAVAILABLE",
    )
    require_text(
        arguments.dex_root / "server" / "config" / "config.go",
        'yaml:"flowRenderingDirectory"',
        'yaml:"startFlowWorkerTargetHeadless"',
        'yaml:"rowKey"',
        'yaml:"columnNaming"',
        'yaml:"attributeMaps"',
        'yaml:"instanceKeyColumn"',
    )
    require_text(
        arguments.dex_root / "server" / "service" / "common" / "attributestore" / "sql_store.go",
        "confirmedMissingColumns",
        "needsColumnRefresh",
        "refresh Attribute Store schema for an unknown column",
    )
    require_text(
        arguments.dex_root / "web" / "connector_release_oauth_settings.go",
        "fromSetting",
    )
    require_text(
        arguments.dex_root / "web" / "connector_oauth.go",
        "CONNECTOR_OAUTH_SETTING_INVALID",
        "client_secret_basic",
    )
    require_text(
        arguments.dex_root / "web" / "connector_address_policy.go",
        "IsLinkLocalUnicast",
        "IsPrivate",
        "connectorAWSIPv6MetadataAddress",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "go_state_loads.go",
        "attribute_map_read_not_loaded",
        "attribute_map_enumeration_not_loaded",
        "attribute_map_instance_not_loaded",
        "channel_messages_not_loaded",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "go_analyzer.go",
        "step_progress_outside_step",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "flowviz" / "go_start_flow_inputs.go",
        "start_flow_input_type_mismatch",
    )
    print("validated Dex SDK, Server, CLI, and server-zd baselines")


if __name__ == "__main__":
    main()
