#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SDK_REFERENCES = ROOT / "dex-sdk" / "references"
# The server-zd release used by core operations guidance.
SERVER_ZD_RELEASE = "server-zd/v0.1.6"
SERVER_ZD_LATEST_RELEASE = "server-zd/v0.1.11"
POSTGRES_CONNECTION_PATH = "server-zd/crates/dexzd-postgres-connection/src/lib.rs"
POSTGRES_TLS_PATH = "server-zd/crates/dexzd-postgres-connection/src/tls.rs"
POSTGRES_DSN_PATH = "server-zd/crates/dexzd-postgres-connection/src/dsn.rs"
SEARCH_BACKEND_PATH = "server-zd/crates/dexzd-search/src/paradedb/search_backend.rs"
SEARCH_PROJECTION_PATH = "server-zd/crates/dexzd-server/src/server_node/search_projection.rs"
SERVER_ZD_READ_OPERATIONS_PATH = "server-zd/crates/dexzd-engine/src/runtime/read_operations/mod.rs"
SERVER_ZD_STATE_ROWS_PATH = "server-zd/crates/dexzd-engine/src/runtime/read_operations/state_rows.rs"
SERVER_ZD_RUNTIME_ERROR_PATH = "server-zd/crates/dexzd-engine/src/runtime/runtime_error.rs"
SERVER_ZD_TIME_TRAVEL_TEST_PATH = "server-zd/crates/dexzd-e2e-tests/tests/dev_server/time_travel.rs"
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
        "without reopening the run",
    )),
    ("", "server-zd/crates/dexzd-worker-dispatch/src/worker_registry.rs", ("CancelTask(",)),
    ("", "server-zd/crates/dexzd-engine/src/effects/dispatch/method_attempt.rs", ("session.cancel();",)),
    ("server-zd/v0.1.5", "server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs", (
        "is_closed_hard_delete_erase",
        "is_hard_delete_on_null",
        "request.action_permission_mappings.is_some()",
    )),
)
# Statements first made by server-zd v0.1.7 through v0.1.10, checked at the latest release.
SERVER_ZD_LATEST_STATEMENTS = (
    ("server-zd/v0.1.7", "server-zd/README.md", (
        "`recovery.startupReadRetryBudget` wall-clock window (default 30 seconds)",
        "ResetFlow replaces a pending close with the state of the reset point.",
    )),
    ("server-zd/v0.1.8", "server-zd/README.md", (
        "From v0.1.8, a transactional or locking InvokeRPC on a closed run returns NOT_FOUND and",
        "A later unsynced write does not replace a pending synced value.",
    )),
    ("server-zd/v0.1.10", "server-zd/crates/dexzd-apply/README.md", (
        "Each logical WaitFor or Execute call, including the Flow timeout handler, loads",
        "Retries reuse its\nAttributes, Channel messages, Channel infos, and selections.",
        "Conflicting RPCs fail immediately while those locks are held.",
        "Locks release when the logical method ends.",
        "If a required permission or mapping Attribute value was collected,",
        "TimeTravel returns `FAILED_PRECONDITION` without changing state.",
    )),
    ("server-zd/v0.1.10", "server-zd/crates/dexzd-engine/README.md", (
        "## SubFlow hard-delete Attributes",
        "While a shared task is active, transactional and locking RPCs share one node-owned task",
        "Concurrent identical requests receive the\nsame result or error.",
        "Conflicting payloads retain the existing\nfingerprint rejection.",
        "The RPC timeout starts at the Worker\ninvocation",
        "Exceeding it returns UNAVAILABLE with an\nunknown outcome and releases the shared task.",
        "An already\nsubmitted command may still commit after the budget expires.",
        "A retry may run the RPC again.",
        "A node crash before the result commits can invoke\nthe Worker again on retry.",
        "generate a fresh request ID for each explicit\nRPC invocation and expose no override.",
    )),
    ("server-zd/v0.1.10", "server-zd/crates/dexzd-engine/src/flow_coordination/client_operations/rpc_requests.rs", (
        "if pending.fingerprint != fingerprint {",
        "request ID {request_id} was already used for a different request",
    )),
    ("server-zd/v0.1.10", "server-zd/crates/dexzd-apply/src/read_model/flow.rs", (
        "InternalTimerStatus::Skipped",
    )),
    ("server-zd/v0.1.10", "server-zd/crates/dexzd-apply/src/steps/step_results.rs", (
        "dispatch_task_error_reason: dispatch_reason as i32,",
    )),
    ("server-zd/v0.1.11", "server-zd/crates/dexzd-request-validation/src/shared.rs", (
        'format!("{path}.flow_start_delay_seconds"),\n            "flow_start_delay_seconds > 0",',
        'format!("{path}.retry_policy"),\n            "Flow retry_policy",',
    )),
    ("server-zd/v0.1.11", "server-zd/crates/dexzd-server/src/config/attribute_store_config.rs", (
        "Selects the Attribute Store backend; dex-server-zd supports only `postgres`.",
    )),
    ("server-zd/v0.1.11", "server-zd/config/dex-server-zd.example.yaml", ("pingInterval: 20s",)),
)


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


def python_function_source(source: str, declaration: str, location: str) -> str:
    lines = source.splitlines()
    starts = [index for index, line in enumerate(lines) if line.startswith(declaration)]
    if len(starts) != 1:
        fail(f"{location} must declare `{declaration}` exactly once; found {len(starts)}")
    start = starts[0]
    end = next((index for index in range(start + 1, len(lines))
                if lines[index] and not lines[index][0].isspace() and not lines[index].startswith(")")), len(lines))
    return "\n".join(lines[start:end])


def require_block_text(block: str, location: str, declaration: str, needles: tuple[str, ...], forbidden: tuple[str, ...] = ()) -> None:
    """Requires every needle and no forbidden lowercase fragment in one extracted declaration block."""
    for needle in needles:
        if needle not in block:
            fail(f"{location} declaration `{declaration}` is missing required text: {needle!r}")
    for fragment in forbidden:
        if fragment in block.lower():
            fail(f"{location} declaration `{declaration}` unexpectedly contains: {fragment!r}")


REFERENCE_1_24_STATEMENTS = (
    ("core/pull-workers.md", (
        "From SDK v1.23.0 in all five languages, a queue that declines a Step for reserved RPC capacity keeps one additional idle stream",
        "sends an unsolicited upstream Pong every 20 seconds while a task runs",
        "above both 20 seconds and server-zd **matching.pingInterval**",
    )),
    ("core/error-handling.md", (
        "return NOT_FOUND with FLOW_NOT_EXISTS",
        "A pull dispatch failure such as no available Worker carries its text",
    )),
    ("core/read-after-write.md", ("From server-zd v0.1.8, a transactional or locking RPC on a closed run returns NOT_FOUND",)),
    ("core/operations.md", (
        "From server-zd v0.1.7, node start retries transient or unknown-outcome object-storage reads",
        "**recovery.startupReadRetryBudget** (default 30 seconds)",
        "From server-zd v0.1.8, Attribute Sync never replaces a pending synced value",
        "server-zd v0.1.10 changes these application-visible behaviors:",
        "From server-zd v0.1.10, time travel returns FAILED_PRECONDITION when rebuilding the target point needs a permission or permission-mapping Attribute value",
        "Concurrent transactional or locking RPCs to the same Flow run that share a request ID",
        "carry identical payloads run the Worker handler once while their shared in-flight task is active",
        "Identical request IDs in different Flow runs do not share a task.",
        "GetFlowState reports a skipped Timer as SKIPPED",
        "server-zd rejects a Flow start delay (**flow_start_delay_seconds** greater than zero) and a Flow-level retry policy (**retry_policy** in the Flow start options) with INVALID_ARGUMENT.",
        "Step-level retry is supported.",
        "### Retired Go Server",
        "Its final release is `server/v1.25.0`.",
    )),
    ("core/step-options.md", (
        "From server-zd v0.1.10, a WaitFor, Execute, or Flow timeout handler retry reuses the state snapshot loaded for that call's first attempt.",
        "From server-zd v0.1.10, every WaitFor, Execute, and Flow timeout handler retry reuses that snapshot",
        "The Attribute locks stay held through retry backoff",
    )),
    ("core/read-after-write.md", (
        "Every SDK generates a fresh request ID for each RPC invocation and exposes no override.",
        "share a request ID and carry identical payloads run the Worker handler once",
        "A request that reuses the ID with a different payload fails with INVALID_ARGUMENT.",
        "This holds only while the shared in-flight task is active.",
        "the call returns UNAVAILABLE and the outcome is unknown",
        "another invocation with the same request ID can run the handler again while the first command may still commit",
    )),
    ("core/data-handling.md", (
        "That holder can be a Step method in retry backoff.",
        "Retries keep their locks until the method call ends from server-zd v0.1.10.",
        "a hard-delete Attribute needs v0.1.10 or later",
    )),
    ("core/bundle-baselines.md", ("guidance covers v0.1.3 through v0.1.11.",)),
    ("go/go.md", ("From Go SDK v1.24.0, a queue that declines a Step for reserved RPC capacity",)),
    ("java/java.md", ("From Java SDK v1.23.0, a queue that declines a Step for reserved RPC capacity",)),
    ("python/python.md", ("From Python SDK v1.23.0, a queue that declines a Step for reserved RPC capacity",)),
    ("rust/rust.md", ("From Rust SDK v1.23.0, a queue that declines a Step for reserved RPC capacity",)),
    ("typescript/typescript.md", ("From TypeScript SDK v1.23.0, a queue that declines a Step for reserved RPC capacity",)),
)


def check_release_1_24_references(references: Path) -> None:
    for page, needles in REFERENCE_1_24_STATEMENTS:
        require_text(references / page, *needles)


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
        "return mapFlowInfo(flowID, response)",
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
        "func mapFlowInfo(flowID string, response *dexpb.GetFlowSummaryResponse) (FlowInfo, error) {",
        "response.GetFlowExecutionId() == nil", 'response.GetFlowExecutionId().GetFlowId() == ""',
        'response.GetFlowExecutionId().GetRunId() == ""', 'response.GetFlowType() == ""',
        "response.GetStartTime() == nil", "mapFlowStatus(response.GetFlowStatus())",
        "response.GetStartTime().CheckValid()",
        "closedAt := time.Time{}", "if response.GetCloseTime() != nil {",
        "response.GetCloseTime().CheckValid()", "closedAt = response.GetCloseTime().AsTime()",
        "FlowID:    response.GetFlowExecutionId().GetFlowId()", "RunID:     response.GetFlowExecutionId().GetRunId()",
        "FlowType:  response.GetFlowType()", "Status:    status",
        "StartedAt: response.GetStartTime().AsTime()", "ClosedAt:  closedAt",
    )
    require_text(
        references / "go" / "observability.md",
        f"From Go SDK v1.21.0, `{GO_DESCRIBE_FLOW_SIGNATURE}` reads the current or latest run",
        "`FlowInfo` has `FlowID string` (application-assigned ID), `RunID string` (server-assigned run ID), "
        "`FlowType string`, `Status FlowStatus`, `StartedAt time.Time`, and `ClosedAt time.Time`.",
        "A zero `ClosedAt` means the run is open or its close time is unavailable.",
        "server-zd keeps the run ID across time travel, so `RunID` matches the run ID `StartFlow` returned.",
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
        "server-zd serves the underlying **GetFlowSummary** RPC.",
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


# Pull-only Worker statements, checked at each language's baseline tag.
PULL_ONLY_SOURCES = {
    "go": (
        ("sdk-go/dex/worker.go", ("Pull *PullWorkerOptions",)),
    ),
    "java": (
        ("sdk-java/src/main/java/io/superdurable/dex/Client.java", (".setIsPullMode(true)",)),
        ("sdk-java/src/main/java/io/superdurable/dex/WorkerOptions.java",
         ("private PullWorkerOptions pull = PullWorkerOptions.newBuilder().build();",)),
    ),
    "python": (
        ("sdk-python/dex/client.py", ("pb.WorkerTarget(is_pull_mode=True)",)),
        ("sdk-python/dex/worker_options.py", ("pull: PullWorkerOptions = field(default_factory=PullWorkerOptions)",)),
    ),
    "typescript": (
        ("sdk-typescript/src/client.ts", ("workerTarget: { isPullMode: true }",)),
        ("sdk-typescript/src/worker.ts", ('options.pull ?? {}',)),
    ),
    "rust": (
        ("sdk-rust/crates/dex-sdk/src/worker_options.rs", ("pull: PullWorkerOptions::default(),",)),
    ),
}
# Each StartFlow and SubFlow mapping must route the new Flow to pull Workers.
GO_PULL_ROUTING = "config.WorkerTarget = &dexpb.WorkerTarget{IsPullMode: true}"
PULL_ONLY_ROUTING_FUNCTIONS = {
    "go": (
        ("sdk-go/dex/proto_mapper.go", "func mapStartFlowOptions(", (GO_PULL_ROUTING,)),
        ("sdk-go/dex/proto_mapper.go", "func mapSubFlowOptions(", (GO_PULL_ROUTING,)),
    ),
    "rust": (
        ("sdk-rust/crates/dex-sdk/src/client.rs", "pub(crate) fn map_pull_flow_config(",
         ("worker_target: Some(ProtoWorkerTarget { is_pull_mode: true }),",)),
        ("sdk-rust/crates/dex-sdk/src/client.rs", "    fn map_start_options(",
         ("Some(map_pull_flow_config(config)?)",)),
        ("sdk-rust/crates/dex-sdk/src/worker_dispatcher.rs", "fn map_sub_flow_options(",
         ("crate::client::map_pull_flow_config(options.config_override.as_ref())",)),
    ),
}
PULL_ONLY_FORBIDDEN_OPTIONS = {
    "go": (("sdk-go/dex/worker.go", ("BindAddress", "WorkerTarget *WorkerTarget")),
           ("sdk-go/dex/client.go", ("WorkerTarget *WorkerTarget",))),
    "java": (("sdk-java/src/main/java/io/superdurable/dex/WorkerOptions.java", ("bindAddress", "workerTarget")),
             ("sdk-java/src/main/java/io/superdurable/dex/ClientOptions.java", ("workerTarget",))),
    "python": (("sdk-python/dex/worker_options.py", ("bind_address", "worker_target")),
               ("sdk-python/dex/client_options.py", ("worker_target",))),
    "typescript": (("sdk-typescript/src/options.ts", ("bindAddress", "workerTarget")),),
    "rust": (("sdk-rust/crates/dex-sdk/src/worker_options.rs", ("bind_address", "worker_target")),
             ("sdk-rust/crates/dex-sdk/src/client_options.rs", ("worker_target",))),
}
PULL_ONLY_REFERENCE_STATEMENTS = (
    ("core/pull-workers.md", ("From SDK v1.25.0 in all five languages, every Worker is a pull Worker",
                              "UpdateFlowConfig does not change a Flow's Worker routing",
                              "server-zd always rejected a Worker address, so no stored Flow holds one",
                              "From SDK v1.26.0 the protocol has no Worker address or headless field",
                              "`dexcli flow start --config` rejects a non-null `workerTarget`")),
    ("go/go.md", ("From Go SDK v1.25.0, every Worker is a pull Worker",)),
    ("java/java.md", ("From Java SDK v1.25.0, every Worker is a pull Worker",)),
    ("python/python.md", ("From Python SDK v1.25.0, every Worker is a pull Worker",)),
    ("typescript/typescript.md", ("From TypeScript SDK v1.25.0, every Worker is a pull Worker",)),
    ("rust/rust.md", ("From Rust SDK v1.25.0, every Worker is a pull Worker",)),
)


def check_pull_only_workers(checkout: Path, sdk_baselines: dict[str, str], references: Path) -> None:
    for language, files in PULL_ONLY_SOURCES.items():
        tag = sdk_baselines[language]
        for path, needles in files:
            source = git(checkout, "show", f"refs/tags/{tag}:{path}")
            for needle in needles:
                if needle not in source:
                    fail(f"{tag}:{path} is missing pull-only text: {needle!r}")
    for language, functions in PULL_ONLY_ROUTING_FUNCTIONS.items():
        require_function_text = (
            require_tagged_go_function_text if language == "go" else require_tagged_rust_function_text
        )
        for path, declaration, needles in functions:
            require_function_text(checkout, sdk_baselines[language], path, declaration, *needles)
    for language, files in PULL_ONLY_FORBIDDEN_OPTIONS.items():
        tag = sdk_baselines[language]
        for path, terms in files:
            source = git(checkout, "show", f"refs/tags/{tag}:{path}")
            for term in terms:
                if term in source:
                    fail(f"{tag}:{path} exposes {term!r}; update the pull-only references")
    for page, needles in PULL_ONLY_REFERENCE_STATEMENTS:
        require_text(references / page, *needles)


def check_flow_service_tls(checkout: Path, go_tag: str, sdk_baselines: dict[str, str], references: Path) -> None:
    tls_path = "sdk-go/dex/flow_service_tls.go"
    for path, declaration in (("sdk-go/dex/client.go", "type ClientOptions struct {"),
                              ("sdk-go/dex/worker.go", "type WorkerOptions struct {")):
        require_tagged_go_function_text(checkout, go_tag, path, declaration,
                                        "FlowServiceTLS *FlowServiceTLSOptions")
    tls_source = git(checkout, "show", f"refs/tags/{go_tag}:{tls_path}")
    tls_options = go_function_source(tls_source, "type FlowServiceTLSOptions struct {", f"{go_tag}:{tls_path}")
    fields = re.findall(r"^\t([A-Z][A-Za-z0-9]*)\s+([^\n]+)$", tls_options, re.MULTILINE)
    if fields != [("AdditionalRootCAsPEM", "[]byte"), ("ServerName", "string")]:
        fail(f"{go_tag}:{tls_path} has unexpected FlowServiceTLSOptions fields: {fields}")
    transport_function = go_function_source(tls_source, "func flowServiceTransportCredentials(", f"{go_tag}:{tls_path}")
    require_block_text(
        transport_function, f"{go_tag}:{tls_path}", "func flowServiceTransportCredentials(", (
        "if options == nil {\n\t\treturn insecure.NewCredentials(), nil",
        'strings.EqualFold(scheme, "dns")', 'strings.ContainsAny(endpoint, "/?#")',
        'resolver.Get(strings.ToLower(scheme)) != nil',
        'host, port, err := net.SplitHostPort(targetAddress)',
        'portNumber < 1 || portNumber > 65535',
        'options.ServerName != "" && !isValidFlowServiceTLSName(options.ServerName)',
        'tls.Config{MinVersion: tls.VersionTLS12, ServerName: options.ServerName}',
        'if len(options.AdditionalRootCAsPEM) > 0 {\n\t\trootCAs, err := x509.SystemCertPool()',
        'if !rootCAs.AppendCertsFromPEM(options.AdditionalRootCAsPEM) {',
        'tlsConfig.RootCAs = rootCAs', 'return credentials.NewTLS(tlsConfig), nil',
        ), ("insecureskipverify", "tlsconfig.certificates"),
    )
    require_tagged_go_function_text(
        checkout, go_tag, tls_path, "func isValidFlowServiceTLSName(",
        'name != ""', 'net.ParseIP(name) != nil',
    )
    for path, declaration, needle in (
        ("sdk-go/dex/client.go", "func NewClient(",
         "messageLimits.flowServiceDialOptions(flowServiceAddress, options.Credential, options.FlowServiceTLS)"),
        ("sdk-go/dex/worker.go", "func NewWorker(",
         "messageLimits.flowServiceDialOptions(flowServiceAddress, options.Credential, options.FlowServiceTLS)"),
        ("sdk-go/dex/message_size.go", "func (limits grpcMessageLimits) flowServiceDialOptions(",
         "grpc.WithTransportCredentials(transportCredentials)"),
        ("sdk-go/dex/worker.go", "func (worker *Worker) Start()", "worker.flowService.GetServerInfo("),
    ):
        require_tagged_go_function_text(checkout, go_tag, path, declaration, needle)
    require_tagged_go_function_text(
        checkout, go_tag, "sdk-go/dex/worker.go", "func NewWorker(",
        "newPullWorker(registry, service, flowConn, pullOptions, currentGoSDKVersion())",
    )
    new_worker = go_function_source(
        git(checkout, "show", f"refs/tags/{go_tag}:sdk-go/dex/worker.go"), "func NewWorker(",
        f"{go_tag}:sdk-go/dex/worker.go",
    )
    if "grpc.NewServer(" in new_worker:
        fail(f"{go_tag}:sdk-go/dex/worker.go NewWorker opens an inbound listener; update the pull-only references")
    require_tagged_go_function_text(
        checkout, go_tag, "sdk-go/dex/message_size.go", "func (limits grpcMessageLimits) flowServiceDialOptions(",
        "flowServiceTransportCredentials(address, tlsOptions)", "credentialDialOptions(credential)...",
    )
    require_tagged_go_function_text(
        checkout, go_tag, "sdk-go/dex/server_protocol.go", "func serverProtocolRequestError(",
        'if status.Code(err) == codes.Unavailable {',
        'FlowService connection failed during GetServerInfo',
        'protocol [%d,%d] is incompatible with Server version',
    )
    require_tagged_go_function_text(
        checkout, go_tag, "sdk-go/integ/tls_transport_test.go",
        "func TestFlowServiceTLSRejectsServerWithoutHTTP2ALPN(",
        'nextProtos: []string{"http/1.1"}', 'want: "missing selected ALPN property"',
    )
    credential_tests = "sdk-go/dex/credential_integration_test.go"
    require_tagged_go_function_text(
        checkout, go_tag, credential_tests, "func TestFlowServiceTLSSendsTargetAuthority(",
        'name: "IPv6 literal", address: ipv6Address, wantAuthority: ipv6Address',
        'wantAuthority: "localhost:" + ipv4Port',
        'serverName: "localhost", wantAuthority: "localhost"',
        'require.Equal(t, testCase.wantAuthority, authorities[len(authorities)-1])',
        'require.Equal(t, testCase.wantTLSServerName, tlsServerNames[len(tlsServerNames)-1])',
    )
    require_tagged_go_function_text(
        checkout, go_tag, credential_tests, "func TestClientCredentialsReachFlowServiceOverTLS(",
        'name: "API key", credential: apiKeyCredential', 'FlowServiceTLS: &FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM}',
        'require.Equal(t, testCase.want, received[len(received)-1])',
    )
    require_tagged_go_function_text(
        checkout, go_tag, credential_tests, "func TestWorkerCredentialReachesPullStreamOverTLS(",
        'FlowServiceTLS:     &FlowServiceTLSOptions{AdditionalRootCAsPEM: caPEM}',
        'Credential:         staticTestCredential("pull-token")',
        'recorder.received(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName)',
    )
    require_section_text(
        references / "core" / "operations.md", "#### FlowService TLS",
        "Go SDK v1.22.0", "ClientOptions.FlowServiceTLS", "WorkerOptions.FlowServiceTLS",
        "pointer to **dex.FlowServiceTLSOptions**", "Nil keeps plaintext",
        "a non-nil empty option verifies with system roots", "version 1.2 or newer",
        "ALPN **h2**", "never falls back to plaintext",
        "dns://authority/host:port", "port from 1 through 65535",
        "default certificate name and SNI", "HTTP/2 **:authority**",
        "FlowServiceTLSOptions.AdditionalRootCAsPEM", "FlowServiceTLSOptions.ServerName",
        "matching IP SAN", "IPv6 endpoint", "no client-certificate or private-key field",
        "NewClient", "NewWorker", "gRPC **Unavailable**", "APIKeyCredential",
        "**api.listen** serves plaintext FlowService gRPC",
        "dexcli", "Dex Web connects with plaintext gRPC unless **--tls** is set",
        "Java", "Python", "TypeScript", "Rust", "no FlowService TLS option",
        "default FlowService connections use plaintext",
    )
    require_section_text(references / "go" / "go.md", "## FlowService TLS",
                         "ClientOptions.FlowServiceTLS", "WorkerOptions.FlowServiceTLS",
                         "*dex.FlowServiceTLSOptions", "AdditionalRootCAsPEM []byte", "ServerName string",
                         "Nil keeps plaintext", "does not accept client certificates")
    require_text(references / "core" / "getting-started.md",
                 "Go Client and Worker each select **FlowServiceTLS**", "operations.md#flowservice-tls")
    require_text(references / "core" / "pull-workers.md",
                 "WorkerOptions.FlowServiceTLS", "ClientOptions.FlowServiceTLS",
                 "pull task streams use the Worker's FlowService connection")
    require_text(ROOT / "dex-sdk" / "SKILL.md", "Go FlowService TLS", "references/core/operations.md#public-api-authentication")
    for language in ("java", "python", "typescript", "rust"):
        require_section_text(references / language / f"{language}.md", "## FlowService transport",
                             "v1.20.0", "ClientOptions", "WorkerOptions", "no FlowService TLS option", "plaintext")
    require_tagged_go_function_text(
        checkout, go_tag, "sdk-go/dex/credential_api_key.go", "func WithAPIKeyHTTPClient(",
        "return apiKeyHTTPClientOption{httpClient: httpClient}",
    )
    for language, paths in (
        ("java", ("sdk-java/src/main/java/io/superdurable/dex/ClientOptions.java",
                  "sdk-java/src/main/java/io/superdurable/dex/WorkerOptions.java")),
        ("python", ("sdk-python/dex/client_options.py", "sdk-python/dex/worker_options.py")),
        ("typescript", ("sdk-typescript/src/options.ts",)),
        ("rust", ("sdk-rust/crates/dex-sdk/src/client_options.rs",
                  "sdk-rust/crates/dex-sdk/src/worker_options.rs")),
    ):
        for path in paths:
            source = git(checkout, "show", f"refs/tags/{sdk_baselines[language]}:{path}").lower()
            if any(term in source for term in ("tls", "ssl", "certificate", "transportsecurity")):
                fail(f"{sdk_baselines[language]}:{path} adds a TLS option; update the language reference")
    java_client_path = "sdk-java/src/main/java/io/superdurable/dex/Client.java"
    java_client_source = git(checkout, "show", f"refs/tags/{sdk_baselines['java']}:{java_client_path}")
    java_client_constructor = rust_function_source(
        java_client_source[java_client_source.index("    public Client(\n"):],
        "    public Client(", f"{sdk_baselines['java']}:{java_client_path}",
    )
    require_block_text(java_client_constructor, f"{sdk_baselines['java']}:{java_client_path}", "Client constructor",
                       ("ManagedChannelBuilder.forTarget(options.getServerAddress())\n                .usePlaintext()",))
    java_worker_path = "sdk-java/src/main/java/io/superdurable/dex/Worker.java"
    require_tagged_rust_function_text(
        checkout, sdk_baselines["java"], java_worker_path,
        "    private static ManagedChannel createOutboundChannel(",
        "ManagedChannelBuilder.forTarget(address).usePlaintext().build()",
    )
    python_transport_path = "sdk-python/dex/_credential_transport.py"
    python_transport = git(checkout, "show", f"refs/tags/{sdk_baselines['python']}:{python_transport_path}")
    for declaration, needle in (
        ("def sync_channel(", "channel = grpc.insecure_channel(server_address)"),
        ("def async_channel(", "return grpc.aio.insecure_channel(server_address)"),
    ):
        python_function = python_function_source(python_transport, declaration, f"{sdk_baselines['python']}:{python_transport_path}")
        require_block_text(python_function, f"{sdk_baselines['python']}:{python_transport_path}", declaration, (needle,))
    python_pull_path = "sdk-python/dex/_pull_worker.py"
    python_pull = git(checkout, "show", f"refs/tags/{sdk_baselines['python']}:{python_pull_path}")
    pull_channel = python_function_source(python_pull, "def outbound_worker_task_channel(",
                                       f"{sdk_baselines['python']}:{python_pull_path}")
    require_block_text(pull_channel, f"{sdk_baselines['python']}:{python_pull_path}",
                       "outbound_worker_task_channel", ("return grpc.aio.insecure_channel(server_address)",))
    for path, class_name in (("sdk-typescript/src/client.ts", "Client"),
                             ("sdk-typescript/src/worker.ts", "Worker")):
        source = git(checkout, "show", f"refs/tags/{sdk_baselines['typescript']}:{path}")
        class_source = source.split(f"export class {class_name} {{", 1)[1].split("\n}\n", 1)[0]
        constructor = rust_function_source(class_source, "  public constructor(",
                                           f"{sdk_baselines['typescript']}:{path}")
        require_block_text(constructor, f"{sdk_baselines['typescript']}:{path}",
                           f"{class_name} constructor", ("new FlowServiceClient(", "credentials.createInsecure()"))
    for path in ("sdk-rust/crates/dex-sdk/src/client.rs", "sdk-rust/crates/dex-sdk/src/worker.rs"):
        require_tagged_rust_function_text(checkout, sdk_baselines["rust"], path,
                                          "fn endpoint_address(", 'format!("http://{address}")')
    require_tagged_text(checkout, SERVER_ZD_RELEASE, "server-zd/crates/dexzd-server/src/config/service_config.rs",
                        "bind address for the plaintext public gRPC services")
    require_tagged_text(checkout, SERVER_ZD_RELEASE, "server-zd/README.md",
                        "`api.listen` serves plaintext gRPC", "Terminate TLS in front of the server")
    zd_path = "server-zd/crates/dexzd-server/src/server_node.rs"
    zd_file = git(checkout, "show", f"refs/tags/{SERVER_ZD_RELEASE}:{zd_path}")
    zd_grpc_impl = zd_file.split("impl GrpcServer {", 1)[1].split("impl Drop for GrpcServer {", 1)[0]
    zd_source = rust_function_source(zd_grpc_impl, "    async fn start(", f"{SERVER_ZD_RELEASE}:{zd_path}")
    require_block_text(zd_source, f"{SERVER_ZD_RELEASE}:{zd_path}", "GrpcServer::start",
                       ("listener: TcpListener", "tonic::transport::Server::builder()", ".serve_with_incoming_shutdown("),
                       (".tls_config(",))


def check_flow_summary_rpc(checkout: Path, server_zd_tag: str) -> None:
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
        "Java, Python, TypeScript, and Rust already have it at their v1.26.0 baselines.",
        "It returns the Flow ID, run ID, Flow type, status, and start time.",
        "A missing Flow returns the SDK's typed Flow-not-found error.",
    )


RELEASE_1_28_TAGGED_STATEMENTS = (
    ("python", "sdk-python/README.md", (
        "`Context.flow_started_at` returns the current Flow run's server start time",
        "`Context.deadline` is the absolute deadline supplied by the Dex Server",
        "accept a registered instance, `StepRef(\"StepType\")`, or\na class.",
        "override\n`get_input_type()` to return a concrete type supported by `CodecRegistry`.",
        "`field(metadata={\"dex_json_name\": \"callId\"})`",
        "`field(default=None, metadata={\"dex_json_omit_default\": True})`",
        "`field(metadata={\"dex_json_format\": \"rfc3339\"})`",
    )),
    ("typescript", "sdk-typescript/README.md", (
        "`Context.deadline` is the absolute deadline supplied by the Dex Server",
        "selections accept a registered instance, `new StepRef(\"StepType\")`, or a class.",
        "`AsyncContext.recordHeartbeat` always reports failures through its returned\nPromise and never throws synchronously.",
    )),
)


def check_release_1_28_statements(checkout: Path, baselines: dict[str, str]) -> None:
    """Checks the Python and TypeScript v1.28.0 README statements that the SDK references repeat."""
    for language, path, needles in RELEASE_1_28_TAGGED_STATEMENTS:
        require_tagged_text(checkout, baselines[language], path, *needles)
    require_tagged_text(
        checkout, baselines["typescript"], "sdk-typescript/src/context.ts",
        "readonly flowStartedAt: Date;",
    )


# Per language: (tagged path, needles) for the Client Attribute read the references describe.
CLIENT_ATTRIBUTE_READ_TAGGED_STATEMENTS = (
    ("go", "sdk-go/README.md", (
        "`GetAttributes` reads selected registered Attributes in one server call, without\ndispatching a Worker.",
        "A read sees committed state, including on a closed run\nuntil it is purged.",
        "An unset Attribute or AttributeMap instance leaves its destination unchanged.",
        "A missing Flow or a run being purged\nreturns `*FlowNotFoundError`",
    )),
    ("go", "sdk-go/dex/client_attributes.go", (
        "func (client *Client) GetAttributes(",
        "// The Client sends all keys in one server request and does not dispatch a Worker.",
        "// A closed run stays readable until it is purged.",
    )),
    ("python", "sdk-python/README.md", (
        "`AsyncClient.get_attributes` is the awaitable form. There is no single-Attribute\nmethod;",
        "so all values come from the same committed state.",
        "closed run stays readable until Dex purges it.",
        "is absent: `values[key]` raises `KeyError`",
    )),
    ("python", "sdk-python/dex/async_client.py", ("    async def get_attributes(",)),
    ("typescript", "sdk-typescript/README.md", (
        "each element is `T | undefined`; `undefined` means the value is not set:",
        "- One FlowService request reads every key from the same committed state. No\n  Worker runs,",
        "A closed\n  run stays readable until Dex purges it.",
    )),
    ("typescript", "sdk-typescript/src/client.ts", ("  public async getAttributes<",)),
)
# Per language without the API: (hand-written source tree, file suffix, search token, declaration pattern).
# A baseline bump that adds a public Client Attribute read anywhere in the tree fails.
CLIENT_ATTRIBUTE_READ_ABSENT = (
    ("java", "sdk-java/src/main/java/io/superdurable/dex", ".java", "getAttributes", re.compile(
        r"\bpublic\b[^;{}()=]*\bgetAttributes\s*\(\s*(?:@\w+\s+)*(?:final\s+)?String\b",
    )),
    ("rust", "sdk-rust/crates/dex-sdk/src", ".rs", "get_attributes", re.compile(
        r"\bpub\s+(?:async\s+)?fn\s+get_attributes\b",
    )),
)
CLIENT_ATTRIBUTE_READ_REFERENCES = (
    ("core/primitives.md", (
        "### Read Attributes from outside a Flow",
        "Go has **Client.GetAttributes** from Go SDK v1.25.0.",
        "The Java and Rust SDKs do not have this API yet; read Flow state there through a typed read-only RPC.",
        "Hard-delete values are no exception: the call returns only committed values.",
        "TypeScript requires the exact registered definition object.",
    )),
    ("core/read-after-write.md", (
        "| Client Attribute read (Go **GetAttributes**, Python **get_attributes**, TypeScript **getAttributes**)",
        "| Step or RPC write or delete of a hard-delete Attribute | Typed read RPC, another Step or RPC, or a retried attempt |",
    )),
    ("go/primitives.md", (
        "## Client Attribute reads",
        "From Go SDK v1.25.0, `Client.GetAttributes(ctx, flowID, reads...)`",
        "`IsSet` and `IsMapInstanceSet` report true only for a definition deeply equal to the one passed to the read",
    )),
    ("python/primitives.md", (
        "## Client Attribute reads",
        "From Python SDK v1.29.0, `Client.get_attributes(flow_id, *reads)` and `await AsyncClient.get_attributes(flow_id, *reads)`",
    )),
    ("typescript/primitives.md", (
        "## Client Attribute reads",
        "From TypeScript SDK v1.29.0, `client.getAttributes(flowId, ...reads)`",
    )),
    ("java/primitives.md", ("The Java SDK does not have the Client Attribute read API yet",)),
    ("rust/primitives.md", ("The Rust SDK does not have the Client Attribute read API yet",)),
)


def tagged_files_containing(checkout: Path, tag: str, tree: str, suffix: str, token: str) -> list[str]:
    """Returns the tagged files under tree with suffix whose content contains token, skipping generated code."""
    result = subprocess.run(
        ["git", "grep", "-l", "-F", "-e", token, f"refs/tags/{tag}", "--", tree],
        cwd=checkout,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode not in (0, 1):
        fail(f"cannot search {tag}:{tree}: {result.stderr.strip()}")
    prefix = f"refs/tags/{tag}:"
    paths = [line.removeprefix(prefix) for line in result.stdout.splitlines()]
    return [path for path in paths if path.endswith(suffix) and "/gen/" not in path]


def check_client_attribute_reads(checkout: Path, baselines: dict[str, str], references: Path) -> None:
    """Checks the Client Attribute read statements against each SDK baseline and the references."""
    for language, path, needles in CLIENT_ATTRIBUTE_READ_TAGGED_STATEMENTS:
        require_tagged_text(checkout, baselines[language], path, *needles)
    for language, tree, suffix, token, declaration in CLIENT_ATTRIBUTE_READ_ABSENT:
        tag = baselines[language]
        for path in tagged_files_containing(checkout, tag, tree, suffix, token):
            if declaration.search(git(checkout, "show", f"refs/tags/{tag}:{path}")):
                fail(f"{tag}:{path} declares a public Client Attribute read; document it for {language}")
    for page, needles in CLIENT_ATTRIBUTE_READ_REFERENCES:
        require_text(references / page, *needles)


def require_ancestor(checkout: Path, ancestor: str, descendant: str, label: str) -> None:
    result = subprocess.run(
        ["git", "merge-base", "--is-ancestor", ancestor, descendant],
        cwd=checkout,
        check=False,
    )
    if result.returncode != 0:
        fail(f"{label} {ancestor} is not an ancestor of {descendant}")


def require_same_tree(checkout: Path, first: str, second: str, paths: tuple[str, ...], label: str) -> None:
    require_tag(checkout, first)
    if git(checkout, "diff", "--stat", f"refs/tags/{first}", f"refs/tags/{second}", "--", *paths):
        fail(f"{label} {first} differs from {second} under {', '.join(paths)}")


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
    web_baseline = (ROOT / "DEX_WEB_BASELINE").read_text().strip()
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
    if server_baseline != SERVER_ZD_LATEST_RELEASE:
        fail(f"DEX_SERVER_BASELINE {server_baseline} must equal {SERVER_ZD_LATEST_RELEASE}")
    require_text(arguments.dex_root / "cli" / "SERVER_ZD_RELEASE", server_baseline.removeprefix("server-zd/"))
    require_same_tree(arguments.dex_root, web_baseline, cli_baseline, ("web", "protos"), "Dex Web baseline")
    require_tag(arguments.dex_root, SERVER_ZD_RELEASE)
    require_tag(arguments.dex_root, SERVER_ZD_LATEST_RELEASE)

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
        "Python and TypeScript FDG 2.0 read `dex:group` and `dex:explanation` comments",
        "[`dex-connector.json`](internal/flowviz/dex-connector.schema.json)",
        "`connector_manifest_missing`",
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
        "zd supports only `type: postgres`",
        "Do not also set\n`attributeStore` in the `--zd-config` file.",
        "server-zd is the only engine.",
        "The Temporal engine was removed.",
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
    check_release_1_24_references(SDK_REFERENCES)
    check_server_zd_statements(arguments.dex_root, SERVER_ZD_LATEST_RELEASE, SERVER_ZD_STATEMENTS)
    check_server_zd_statements(arguments.dex_root, SERVER_ZD_LATEST_RELEASE, SERVER_ZD_LATEST_STATEMENTS)
    check_server_zd_search_tls(arguments.dex_root, SERVER_ZD_RELEASE, SDK_REFERENCES)
    check_flow_summary_rpc(arguments.dex_root, SERVER_ZD_RELEASE)
    check_sdk_flow_closing_errors(arguments.dex_root, sdk_baselines, SDK_REFERENCES)
    check_go_describe_flow(arguments.dex_root, go_sdk_baseline, SDK_REFERENCES)
    check_sdk_describe_flow(arguments.dex_root, sdk_baselines, SDK_REFERENCES)
    check_flow_service_tls(arguments.dex_root, go_sdk_baseline, sdk_baselines, SDK_REFERENCES)
    check_pull_only_workers(arguments.dex_root, sdk_baselines, SDK_REFERENCES)
    check_release_1_28_statements(arguments.dex_root, sdk_baselines)
    check_client_attribute_reads(arguments.dex_root, sdk_baselines, SDK_REFERENCES)
    require_tagged_text(
        arguments.dex_root,
        go_sdk_baseline,
        "sdk-go/README.md",
        "The SDK sends an upstream Pong every 20 seconds while a task runs",
        "keeps one additional idle stream until 30",
    )
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
        arguments.dex_root / "web" / "cmd" / "dex-web" / "config.go",
        '"bind-address", "127.0.0.1"', '"port", web.DefaultPort', '"flow-service-address", "127.0.0.1:8801"',
        '"flow-rendering-dir"', '"tls-ca-file"', '"tls-server-name"', '"flow-service-credential-file"',
        '"DEX_WEB_BIND_ADDRESS"', '"DEX_WEB_HTTP_PORT_NUMBER"', '"DEX_WEB_FLOW_SERVICE_ADDRESS"',
        '"DEX_WEB_FLOW_RENDERING_DIRECTORY"', '"DEX_WEB_TLS"', '"DEX_WEB_TLS_CA_FILE"',
        '"DEX_WEB_TLS_SERVER_NAME"', '"DEX_WEB_FLOW_SERVICE_CREDENTIAL_FILE"',
        "deprecated: has no effect; Dex Web starts Flows on pull Workers",
        "TLS CA file and server name require --tls",
    )
    require_text(
        arguments.dex_root / "web" / "README.md",
        "## Run standalone dex-web",
        "superdurable/dex-web:latest",
        "runs as UID/GID 65532",
        "`/readyz` is readiness and calls FlowService with the configured credential on\nevery probe.",
        "optional `workerTargetPull: true`.",
        "or `workerTargetPull: false`, because Dex routes Flow work to pull Workers.",
        "accepts and ignores `bypassWorkerHealthCheck`",
    )
    require_text(
        arguments.dex_root / "web" / "api" / "v2_start.go",
        "workerTargetAddress is not supported: Dex routes Flow work to pull Workers",
        "workerTargetPull must be true: Dex routes Flow work to pull Workers",
    )
    require_text(
        arguments.dex_root / "cli" / "internal" / "command" / "flow_client_operations.go",
        "workerTarget is not supported: Dex routes Flow work to pull Workers",
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
