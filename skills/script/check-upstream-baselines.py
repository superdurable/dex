#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SDK_REFERENCES = ROOT / "dex-sdk" / "references"
# The server-zd release whose close-barrier guidance core/operations.md cites.
SERVER_ZD_RELEASE = "server-zd/v0.1.5"
WORKFLOW_UPDATER_PATH = "server/service/interpreter/workflowUpdater.go"
API_SERVICE_PATH = "server/service/api/service.go"
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
    require_tagged_text(
        arguments.dex_root,
        SERVER_ZD_RELEASE,
        "server-zd/README.md",
        "Only `type: postgres` is supported.",
        "Synchronization is asynchronous.",
        "Rows normally appear within a second of the commit.",
        "(default `dexzd_attribute_sync_fencing`)",
        "dex-server-zd never creates or alters tables",
        "`--attribute-store-config` reads only the `attributeStore` section",
        "which must define at least one store",
        "StopFlow closes the run as soon as it commits.",
        "server-zd v0.1.4 or later waits, like the Go Server.",
        "GetFlowSummary and SearchFlows",
        "for every\n  transactional or locking RPC",
        "SetAttributes, PublishToChannel, and non-transactional RPC results are",
        "cleanup fail with FAILED_PRECONDITION and FLOW_CLOSING",
        "`syncRetryPolicy.totalDuration` bounds each pending entry's retry window",
        "Entries of a store that is no longer configured",
        "StopFlow (CANCEL or FAIL), Terminate",
        "`processCrashSafe` (default)",
        "Delete the whole data directory to start",
        "A directory that an earlier release stopped gracefully keeps working in every mode.",
        "A directory that an earlier release left after an abrupt exit is refused in the default and",
        "After the first start of a release with this rule, the directory is stamped, and any mode works.",
        "takes it over at once",
    )
    require_tagged_text(
        arguments.dex_root,
        SERVER_ZD_RELEASE,
        "docs/content/production/server-zd-operations.mdx",
        "server-zd v0.1.4 or later waits, like the Go Server.",
        "Transactional or locking RPCs, WaitForStepCompletion, WaitForAttribute, and",
        "**syncRetryPolicy.totalDuration** limits how long each pending write is",
        "Pending writes for a store that is no longer configured are dropped at once",
        "**processCrashSafe**",
        "every write erases a currently stored hard-delete Attribute with null",
    )
    require_tagged_text(
        arguments.dex_root,
        SERVER_ZD_RELEASE,
        "server-zd/crates/dexzd-worker-dispatch/src/worker_registry.rs",
        "CancelTask(",
    )
    require_tagged_text(
        arguments.dex_root,
        SERVER_ZD_RELEASE,
        "server-zd/crates/dexzd-engine/src/effects/dispatch/method_attempt.rs",
        "session.cancel();",
    )
    require_tagged_text(
        arguments.dex_root,
        SERVER_ZD_RELEASE,
        "server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs",
        "is_closed_hard_delete_erase",
        "is_hard_delete_on_null",
        "request.action_permission_mappings.is_some()",
    )
    check_go_server_close_cleanup(arguments.dex_root, server_baseline)
    require_tagged_text(
        arguments.dex_root,
        server_baseline,
        "server/config/config.go",
        'yaml:"useTemporalSynchronousUpdateForAllRPCs"',
    )
    require_tagged_text(
        arguments.dex_root,
        server_baseline,
        "server/service/interpreter/terminalCoordinator.go",
        "inflightUpdateOperations == 0",
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
