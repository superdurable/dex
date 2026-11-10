# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector operations as Stepwise agent tools."""

from __future__ import annotations

import importlib
import importlib.resources
import json
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from datetime import timedelta
from enum import StrEnum
from typing import Any, Literal, cast

from dex import AsyncContext, CodecRegistry, RetryPolicy, Value, WireKind
from dex_connectors.sdk import (
    Call,
    ConnectionRef,
    Mutation,
    MutationDefinition,
    OperationRef,
    Query,
    RetryError,
    default_connector_step_execute_retry_policy,
    derive_call_id,
    raise_dex_retry,
    run_mutation,
    run_query,
)
from dex_connectors.sdk.singledispatch import Marker, State

from dex_agents.connectors._schema import check_input_schema, find_input_violation
from dex_agents.connectors._step_context import ConnectorStepContext
from dex_agents.runner import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolCallStepOptions,
    ToolExecutionError,
    ToolExecutionResult,
    ToolOutcomeUnknownError,
    ToolPolicy,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)
from dex_agents.values import AgentToolCall, ToolEffect

#: Default ``source_name`` of a ``ConnectorToolSource``.
DEFAULT_CONNECTOR_SOURCE_NAME = "connectors"

#: Default bound, in bytes of UTF-8 text, on the result one connector tool call gives the model.
DEFAULT_CONNECTOR_RESULT_MAX_BYTES = 64 * 1024

#: Note appended to a truncated result; ``{total_bytes}`` is the size of the full result.
CONNECTOR_RESULT_TRUNCATED_NOTE = "[Dex] truncated, {total_bytes} bytes"

#: Error text of a single-attempt write whose fence proved no send before the connector's Retry.
CONNECTOR_NOT_SENT_RETRY_LATER = "not sent; the provider asked to retry later"

#: Name of the manifest file every Connector language package ships.
CONNECTOR_MANIFEST_FILE_NAME = "dex-connector.json"

# The most CallTool attempts of any tool; a connector's own policy can only narrow it.
_MAXIMUM_TOOL_ATTEMPTS = 3
_DEFECT_BRANCH = "defect"
_UNCERTAIN_BRANCH = "uncertain"
_CODECS = CodecRegistry()

#: Decides from a call's decoded JSON arguments whether that call waits for approval.
ApprovalPredicate = Callable[[Mapping[str, Any]], bool]


class ConnectorBranchOutcome(StrEnum):
    """Name what a connector tool call records for the branch its operation selected.

    Attributes:
        SUCCEEDED: A required branch, such as ``sent`` or ``refunded``. The
            record is ``SUCCEEDED`` and the model reads the result value.
        REJECTED: A branch the manifest lists as ``rejected`` under
            ``agent.branches``, such as ``providerRejected``. Nothing took
            effect. The record is ``FAILED`` with an error the model reads, and
            the session continues.
        FAILED: ``defect``, or an optional branch of a Query that
            ``agent.branches`` does not list. The record is ``FAILED``.
        OUTCOME_UNKNOWN: ``uncertain``, a branch listed as ``unknown``, or an
            optional branch of a Mutation that ``agent.branches`` does not list.
            The write may have taken effect. The record is ``OUTCOME_UNKNOWN``.
    """

    SUCCEEDED = "succeeded"
    REJECTED = "rejected"
    FAILED = "failed"
    OUTCOME_UNKNOWN = "outcome_unknown"


def classify_connector_branch(
    *,
    kind: Literal["query", "mutation"],
    branch: str,
    required_branches: frozenset[str],
    agent_branches: Mapping[str, str],
) -> ConnectorBranchOutcome:
    """Return what a connector tool call records for one selected branch.

    The rule fails closed: an optional branch of a Mutation that the manifest
    does not classify is ``OUTCOME_UNKNOWN``, because the write may have
    happened.

    Args:
        kind: Whether the operation is a Query or a Mutation.
        branch: The branch the operation selected.
        required_branches: The operation's branches that are not optional.
        agent_branches: The manifest's ``agent.branches``: branch ID to
            ``"rejected"`` or ``"unknown"``.

    Returns:
        The outcome to record.
    """
    if branch == _DEFECT_BRANCH:
        return ConnectorBranchOutcome.FAILED
    if branch == _UNCERTAIN_BRANCH:
        return ConnectorBranchOutcome.OUTCOME_UNKNOWN
    if branch in required_branches:
        return ConnectorBranchOutcome.SUCCEEDED
    listed = agent_branches.get(branch)
    if listed == "rejected":
        return ConnectorBranchOutcome.REJECTED
    if listed == "unknown" or kind == "mutation":
        return ConnectorBranchOutcome.OUTCOME_UNKNOWN
    return ConnectorBranchOutcome.FAILED


@dataclass(frozen=True, kw_only=True)
class ConnectorTool:
    """Describe one Connector operation offered to a Stepwise agent as a tool.

    Create it with ``connector_tool``, which reads the operation's entry in its
    package's ``dex-connector.json``. Pass it to ``ConnectorToolSource``.

    Attributes:
        name: Tool name the model calls.
        description: Tool description for the model.
        input_schema: JSON Schema of the arguments, the manifest's ``inputSchema``.
        connector_id: The Connector ID, such as ``stripe``.
        operation_id: The operation ID, such as ``createRefund``.
        kind: ``"query"`` or ``"mutation"``.
        effect: ``READ`` for a Query, ``IDEMPOTENT_WRITE`` for a Mutation with
            ``agent.retry: safe``, and ``NON_IDEMPOTENT_WRITE`` for any other
            Mutation.
        is_approval_recommended: Whether the manifest says
            ``agent.approval: recommended``.
        requires_approval: The application's choice: ``True`` or ``False``
            overrides the manifest, a predicate decides per call, and ``None``
            follows the manifest.
        max_result_bytes: Bound on the result the model reads, or ``None`` for
            the source's bound.
        connection: The logical connection the operation runs with.
        operation: The connector operation.
        input_type: The operation's input type, which arguments decode into.
        agent_branches: The manifest's ``agent.branches``.
        required_branches: The operation's branches that are not optional.
        execute_method_timeout: The operation's Execute timeout, or ``None``.
        retry_policy: The operation's Execute retry policy, which bounds a
            ``CallTool`` call's retries.
    """

    name: str
    description: str
    input_schema: Mapping[str, Any]
    connector_id: str
    operation_id: str
    kind: Literal["query", "mutation"]
    effect: ToolEffect
    is_approval_recommended: bool
    requires_approval: bool | ApprovalPredicate | None
    max_result_bytes: int | None
    connection: ConnectionRef
    operation: Query[Any, Any] | Mutation[Any, Any] = field(repr=False)
    input_type: type[Any] = field(repr=False)
    agent_branches: Mapping[str, str] = field(default_factory=dict)
    required_branches: frozenset[str] = frozenset()
    execute_method_timeout: timedelta | None = None
    retry_policy: RetryPolicy | None = None


def connector_tool(
    operation: Query[Any, Any] | Mutation[Any, Any],
    connection: ConnectionRef,
    *,
    name: str | None = None,
    description: str | None = None,
    requires_approval: bool | ApprovalPredicate | None = None,
    max_result_bytes: int | None = None,
    manifest: Mapping[str, Any] | None = None,
) -> ConnectorTool:
    """Offer one reviewed Connector operation to a Stepwise agent as a tool.

    The tool's name, description, and argument schema come from the
    operation's entry in its package's ``dex-connector.json``. Only an
    operation whose entry says ``agent.exposed: true`` can become a tool;
    nothing is exposed by default, and the application still names each
    operation it offers.

    The manifest decides how the call runs. A Query is ``READ`` and runs in
    ``CallTool``. A Mutation with ``agent.retry: safe`` is
    ``IDEMPOTENT_WRITE`` and runs in ``CallTool``, whose retries stay inside
    the operation's own Execute retry policy and timeout. Any other Mutation is
    ``NON_IDEMPOTENT_WRITE`` and runs once in ``CallSingleAttemptTool``. The
    effect class always comes from the manifest; a session's
    ``tool_policies`` entry for the tool changes only its approval.

    Approval: a call waits for ``resolve_interrupt`` when the manifest says
    ``agent.approval: recommended``. ``requires_approval`` overrides that.
    Pass a bool for every call, or a predicate that decides per call from the
    decoded JSON arguments, such as refunds above a limit. A predicate that
    raises requires approval. Arguments that break the schema never run, so
    they never wait for approval.

    Example:
        >>> stripe = new_project_connection(project, "billing-stripe")
        >>> refund = connector_tool(
        ...     stripe.operations.create_refund(),
        ...     stripe.reference,
        ...     requires_approval=lambda arguments: arguments["amount"] > 5000,
        ... )
        >>> source = ConnectorToolSource([refund])

    Args:
        operation: The connector operation, such as
            ``connection.operations.create_refund()``.
        connection: The logical connection reference the call runs with.
            Only the reference is stored; the Worker resolves credentials.
        name: Tool name; defaults to ``<connectorId>_<operation in snake case>``,
            such as ``stripe_create_refund``.
        description: Model-facing description; defaults to the manifest's.
        requires_approval: The application's approval choice, as above.
        max_result_bytes: Bound on the result the model reads; defaults to the
            source's ``max_result_bytes``.
        manifest: The package's parsed ``dex-connector.json``. By default it is
            read as package data: from the package that defines ``operation``
            or one of its parents, and then from ``dex_connectors.<connector>``,
            the official package, which covers the packages' mock operations.
            ``inputType`` is looked up in the same package.

    Returns:
        The tool.

    Raises:
        ValueError: If the manifest has no entry for the operation, the entry
            is not ``agent.exposed: true``, its kind differs from the
            operation's, its ``agent`` block holds an unknown value, or its
            ``inputSchema`` uses a keyword the agent core cannot check.
        LookupError: If no manifest is found, or its ``inputType`` is not
            exported by the package.
    """
    definition = operation.definition()
    kind: Literal["query", "mutation"] = (
        "mutation" if isinstance(definition, MutationDefinition) else "query"
    )
    operation_ref = definition.operation
    loaded = (
        (dict(manifest), _package_of(operation))
        if manifest is not None
        else _read_manifest(operation, operation_ref.connector_id)
    )
    document, package = loaded
    label = f"{operation_ref.connector_id} {operation_ref.operation_id}"
    if document.get("connectorId") != operation_ref.connector_id:
        raise ValueError(f"{label}: the manifest describes Connector {document.get('connectorId')}")
    factory = next(
        (
            entry
            for entry in document.get("factories", [])
            if entry.get("operationId") == operation_ref.operation_id
        ),
        None,
    )
    if factory is None:
        raise ValueError(f"{label}: the manifest has no entry for this operation")
    agent = factory.get("agent")
    if not isinstance(agent, Mapping) or agent.get("exposed") is not True:
        raise ValueError(
            f"{label} is not exposed to agents: its manifest lacks agent.exposed: true"
        )
    if factory.get("kind") != kind:
        raise ValueError(
            f"{label}: the manifest says {factory.get('kind')}, the operation is a {kind}"
        )
    approval = agent.get("approval")
    retry = agent.get("retry")
    branches = agent.get("branches", {})
    if approval not in (None, "recommended"):
        raise ValueError(f"{label}: unknown agent.approval {approval!r}")
    if retry not in (None, "safe") or (retry is not None and kind == "query"):
        raise ValueError(f"{label}: unknown agent.retry {retry!r}")
    if not isinstance(branches, Mapping) or any(
        value not in ("rejected", "unknown") for value in branches.values()
    ):
        raise ValueError(f"{label}: agent.branches values must be rejected or unknown")
    optional_branches = {branch.id for branch in definition.branches or () if branch.optional}
    if any(branch not in optional_branches for branch in branches):
        raise ValueError(f"{label}: agent.branches may list only optional branches")
    if kind == "query" and "unknown" in branches.values():
        raise ValueError(f"{label}: agent.branches unknown is for Mutations only")
    schema = factory.get("inputSchema")
    if not isinstance(schema, Mapping):
        raise ValueError(f"{label}: the manifest has no inputSchema")
    check_input_schema(schema)
    input_type = _input_type(package, factory.get("inputType"), label)
    if max_result_bytes is not None and max_result_bytes < 1:
        raise ValueError("max_result_bytes must be positive")
    effect = (
        ToolEffect.READ
        if kind == "query"
        else ToolEffect.IDEMPOTENT_WRITE
        if retry == "safe"
        else ToolEffect.NON_IDEMPOTENT_WRITE
    )
    step_defaults = definition.step_defaults
    return ConnectorTool(
        name=name or f"{operation_ref.connector_id}_{_snake_case(operation_ref.operation_id)}",
        description=description or str(factory.get("description") or ""),
        input_schema=schema,
        connector_id=operation_ref.connector_id,
        operation_id=operation_ref.operation_id,
        kind=kind,
        effect=effect,
        is_approval_recommended=approval == "recommended",
        requires_approval=requires_approval,
        max_result_bytes=max_result_bytes,
        connection=connection,
        operation=operation,
        input_type=input_type,
        agent_branches=dict(branches),
        required_branches=frozenset(
            branch.id for branch in definition.branches or () if not branch.optional
        ),
        execute_method_timeout=step_defaults.execute_method_timeout or None,
        retry_policy=_bounded_retry_policy(step_defaults),
    )


class ConnectorToolSource:
    """Offer reviewed Connector operations to a Stepwise agent as tools.

    Put it in ``AgentDefinition.tool_sources``, as ``McpToolSource`` is. It
    lists exactly the tools the application passes; no Connector is exposed by
    default. Each call runs one operation invocation in its own tool Step,
    through the Connector SDK's ``run_query`` or ``run_mutation``, so the
    connector's Call ID, idempotency key, credential handling, and
    single-dispatch fence apply. The Call ID is derived from the Flow ID and
    the tool Step's execution ID, so it is the same on every attempt of that
    Step.

    Before anything runs, the model's arguments are checked against the
    manifest's ``inputSchema`` and then decoded into the operation's input
    type. Either failure is a ``FAILED`` call the model can correct; the
    operation never runs.

    Connector writes of one model response run one at a time in the model's
    order, and reads run in parallel. A write whose approval is needed waits
    for it only when its turn comes. Once a write does not succeed, the later
    writes of that response are recorded ``FAILED`` with
    ``AgentToolCall.skipped`` set.

    The branch the operation selects maps as ``classify_connector_branch``
    describes. A connector retry raises through the Step, so the tool Step's
    retry policy applies. A single-attempt write that asks to retry ends
    ``OUTCOME_UNKNOWN``, unless its single-dispatch fence saved a ``notSent`` or
    ``canResend`` marker in this attempt: that proves the provider did not
    process the request, so the call records ``FAILED`` with
    ``CONNECTOR_NOT_SENT_RETRY_LATER``. Either way it stops the later writes.
    A result longer than ``max_result_bytes`` reaches the model truncated,
    with ``CONNECTOR_RESULT_TRUNCATED_NOTE``, and the record's
    ``truncated_output_bytes`` holds the full size.

    Example:
        >>> slack = slack_new_project_connection(project, "support-slack")
        >>> stripe = stripe_new_project_connection(project, "billing-stripe")
        >>> agent = AgentDefinition(
        ...     name="support",
        ...     instructions="You help customers.",
        ...     model=LiteLLMModelClient("openai/gpt-5-mini"),
        ...     tool_sources=[
        ...         ConnectorToolSource([
        ...             connector_tool(slack.post_thread_reply(), slack.reference),
        ...             connector_tool(stripe.operations.create_refund(), stripe.reference),
        ...         ]),
        ...     ],
        ... )

    Args:
        tools: The tools to offer, in the order the model sees them.
        source_name: Stable name stored with each call; keep it across deploys.
        max_result_bytes: Default bound on each call's result, in bytes of
            UTF-8 text. Defaults to ``DEFAULT_CONNECTOR_RESULT_MAX_BYTES``.

    Raises:
        ValueError: If two tools share a name, ``source_name`` is blank, or
            ``max_result_bytes`` is not positive.
    """

    def __init__(
        self,
        tools: Sequence[ConnectorTool],
        *,
        source_name: str = DEFAULT_CONNECTOR_SOURCE_NAME,
        max_result_bytes: int = DEFAULT_CONNECTOR_RESULT_MAX_BYTES,
    ) -> None:
        if not source_name.strip():
            raise ValueError("source_name must not be blank")
        if max_result_bytes < 1:
            raise ValueError("max_result_bytes must be positive")
        self._tools = {tool.name: tool for tool in tools}
        if len(self._tools) != len(tools):
            raise ValueError("two connector tools share a name")
        self._source_name = source_name
        self._max_result_bytes = max_result_bytes
        self._definitions = tuple(
            _ConnectorToolDefinition(tool.name, tool.description, tool.input_schema)
            for tool in tools
        )

    @property
    def source_name(self) -> str:
        """Return the source's stable name."""
        return self._source_name

    async def list_tools(self) -> Sequence[_ConnectorToolDefinition]:
        """Return the tools; the list is fixed when the source is built."""
        return self._definitions

    def cached_tools(self) -> Sequence[_ConnectorToolDefinition]:
        """Return the tools without I/O."""
        return self._definitions

    def declared_tool_names(self) -> frozenset[str]:
        """Return every tool name, so ``AgentDefinition`` refuses a collision."""
        return frozenset(self._tools)

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: Any,
    ) -> DeferredToolCall:
        """Build the call with the manifest's effect class and the call's approval.

        Args:
            turn: The ``RunModel`` context. A ``tool_policies`` entry for the
                tool changes only whether the call waits for approval.
            tool_call_id: Model tool call ID.
            tool_name: The tool name.
            arguments: Tool arguments as JSON text.
            listed_tool: The listed definition.

        Returns:
            The deferred call, ordered when it is a write.
        """
        tool = self._tools[tool_name]
        session_policy = turn.tool_policies.get(tool_name)
        if session_policy is not None and session_policy.requires_approval is not None:
            requires_approval = session_policy.requires_approval
        else:
            requires_approval = _requires_approval(tool, arguments)
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=ToolPolicy(tool.effect, requires_approval),
            tool_source_reference=json.dumps({"tool": tool.name}),
            ordered=tool.effect is not ToolEffect.READ,
        )

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        """Return the operation's timeout and retry policy for the call's tool Step.

        Args:
            tool_call: The record whose tool Step starts.

        Returns:
            The options, or ``None`` when this source no longer has the tool.
        """
        tool = self._tools.get(_reference_tool_name(tool_call))
        if tool is None:
            return None
        return ToolCallStepOptions(
            execute_method_timeout=tool.execute_method_timeout, execute_retry=tool.retry_policy
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        """Check, decode, and run one call, then map the selected branch.

        Args:
            call: The approved call, with the tool Step's live context.

        Returns:
            The result value as JSON text, bounded by ``max_result_bytes``.

        Raises:
            ToolExecutionError: The arguments break the schema or do not
                decode, the operation selected a ``rejected`` or failed branch,
                or a single-attempt write asked to retry after its fence
                recorded ``notSent`` or ``canResend`` in this attempt, which
                proves the provider did not process it
                (``CONNECTOR_NOT_SENT_RETRY_LATER``).
            ToolOutcomeUnknownError: The operation selected an unknown branch.
            LookupError: This source no longer has the tool, so the effect class decides.
            RuntimeError: The call has no Step context.
            Exception: A connector retry, raised for the tool Step's retry policy.
        """
        record = call.tool_call
        tool = self._tools.get(_reference_tool_name(record))
        if tool is None:
            raise LookupError(f"connector tool {record.tool_name} is not configured on this Worker")
        values = _parse_arguments(tool.name, record.arguments)
        problem = find_input_violation(values, tool.input_schema)
        if problem is not None:
            raise ToolExecutionError(f"invalid arguments for {tool.name}: {problem}")
        try:
            operation_input = _CODECS.resolve(tool.input_type).decode(
                Value(WireKind.JSON, json.dumps(values))
            )
        except Exception:
            # A fixed text: codec messages differ between languages and versions.
            raise ToolExecutionError(
                f"invalid arguments for {tool.name}: arguments do not decode"
            ) from None
        if call.context is None:
            raise RuntimeError("a connector tool call needs the tool Step's context")
        connector_call = _connector_call(tool, call.context, call)
        try:
            if tool.kind == "mutation":
                result: Any = await run_mutation(
                    cast(Mutation[Any, Any], tool.operation), connector_call, operation_input
                )
            else:
                result = await run_query(
                    cast(Query[Any, Any], tool.operation), connector_call, operation_input
                )
        except RetryError as error:
            if tool.effect is ToolEffect.NON_IDEMPOTENT_WRITE and _is_proven_not_sent(
                connector_call
            ):
                # One attempt only, so a Retry would record outcome_unknown though nothing was sent.
                raise ToolExecutionError(_not_sent_detail(tool, error.failure)) from None
            raise_dex_retry(error)
        outcome = classify_connector_branch(
            kind=tool.kind,
            branch=result.branch,
            required_branches=tool.required_branches,
            agent_branches=tool.agent_branches,
        )
        if outcome is ConnectorBranchOutcome.SUCCEEDED:
            return bound_connector_result(
                _encode_value(result.value), tool.max_result_bytes or self._max_result_bytes
            )
        detail = _branch_detail(tool, result.branch, result.failure)
        if outcome is ConnectorBranchOutcome.OUTCOME_UNKNOWN:
            raise ToolOutcomeUnknownError(detail)
        raise ToolExecutionError(detail)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Refuse a poll: connector tools never start tool tasks.

        Raises:
            LookupError: Always.
        """
        raise LookupError(f"connector tool {poll.tool_call.tool_name} starts no task")


def bound_connector_result(output: str, max_result_bytes: int) -> ToolExecutionResult:
    """Return ``output`` whole, or its first ``max_result_bytes`` bytes and the truncation note.

    Args:
        output: The full result text.
        max_result_bytes: The bound, in bytes of UTF-8 text, on the kept part.

    Returns:
        The result, with ``truncated_output_bytes`` set when it was cut.
    """
    encoded = output.encode("utf-8")
    if len(encoded) <= max_result_bytes:
        return ToolExecutionResult(output=output)
    kept = encoded[:max_result_bytes].decode("utf-8", errors="ignore")
    note = CONNECTOR_RESULT_TRUNCATED_NOTE.format(total_bytes=len(encoded))
    return ToolExecutionResult(output=f"{kept}\n{note}", truncated_output_bytes=len(encoded))


@dataclass(frozen=True)
class _ConnectorToolDefinition:
    name: str
    description: str
    input_schema: Mapping[str, Any]


def _connector_call(
    tool: ConnectorTool, context: AsyncContext, call: DeferredToolExecution
) -> Call:
    operation_ref = OperationRef(tool.connector_id, tool.operation_id)
    step_context = ConnectorStepContext(
        context,
        previous_checkpoint=call.heartbeat_checkpoint,
        heartbeat=call.heartbeat,
        save_checkpoint=call.save_heartbeat_checkpoint,
    )
    return Call(
        derive_call_id(context.flow_id, context.step_execution_id, operation_ref),
        tool.connection,
        operation_ref,
        context=cast(AsyncContext, step_context),
    )


def _requires_approval(tool: ConnectorTool, arguments: str) -> bool:
    if tool.requires_approval is None:
        return tool.is_approval_recommended
    if isinstance(tool.requires_approval, bool):
        return tool.requires_approval
    try:
        values = json.loads(arguments or "{}")
    except json.JSONDecodeError:
        return False
    if not isinstance(values, dict) or find_input_violation(values, tool.input_schema):
        # The tool Step refuses these arguments before the operation runs.
        return False
    try:
        return bool(tool.requires_approval(values))
    except Exception:
        return True


def _parse_arguments(tool_name: str, arguments: str) -> dict[str, Any]:
    try:
        values = json.loads(arguments or "{}")
    except json.JSONDecodeError:
        raise ToolExecutionError(
            f"invalid arguments for {tool_name}: arguments are not valid JSON"
        ) from None
    if not isinstance(values, dict):
        raise ToolExecutionError(f"invalid arguments for {tool_name}: expected a JSON object")
    return values


def _encode_value(value: object) -> str:
    if value is None:
        return "null"
    try:
        encoded = _CODECS.resolve(type(value)).encode(value)
    except Exception:
        return format_connector_result(json.dumps(value))
    if encoded.kind is WireKind.JSON and isinstance(encoded.data, str):
        return format_connector_result(encoded.data)
    return format_connector_result(json.dumps(encoded.data))


def format_connector_result(json_text: str) -> str:
    """Return the text the model reads for one result value, the same in every core.

    Keys are sorted, separators carry no spaces, text stays unescaped UTF-8, and
    a number with no fractional part is written as an integer.

    Args:
        json_text: The result value as JSON text.

    Returns:
        The canonical text.
    """
    return json.dumps(
        _integral_floats_as_ints(json.loads(json_text)),
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    )


def _integral_floats_as_ints(value: Any) -> Any:
    if isinstance(value, float) and value.is_integer() and abs(value) < 2**53:
        return int(value)
    if isinstance(value, dict):
        return {key: _integral_floats_as_ints(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_integral_floats_as_ints(item) for item in value]
    return value


def _is_proven_not_sent(call: Call) -> bool:
    """Report whether this attempt's last saved fence marker proves the write was not sent."""
    context = call.context
    if not isinstance(context, ConnectorStepContext):
        return False
    marker = context._saved_heartbeat_value
    return (
        isinstance(marker, Marker)
        and marker.call_id == call.id
        and marker.attempt == context.attempt
        and marker.state in (State.NOT_SENT, State.CAN_RESEND)
        and not marker.provider_id
    )


def _not_sent_detail(tool: ConnectorTool, failure: Any) -> str:
    detail = f"{tool.connector_id} {tool.operation_id} {CONNECTOR_NOT_SENT_RETRY_LATER}"
    return f"{detail}: {failure.kind}: {failure.message}" if failure is not None else detail


def _branch_detail(tool: ConnectorTool, branch: str, failure: Any) -> str:
    detail = f"{tool.connector_id} {tool.operation_id} returned {branch}"
    if failure is not None:
        detail += f": {failure.kind}: {failure.message}"
    return detail


def _reference_tool_name(record: AgentToolCall) -> str:
    try:
        reference = json.loads(record.tool_source_reference or "")
    except json.JSONDecodeError:
        return record.tool_name
    name = reference.get("tool") if isinstance(reference, dict) else None
    return name if isinstance(name, str) else record.tool_name


def _bounded_retry_policy(step_defaults: Any) -> RetryPolicy:
    policy = step_defaults.execute_retry or default_connector_step_execute_retry_policy()
    attempts = policy.maximum_attempts
    return RetryPolicy(
        initial_interval=policy.initial_interval,
        backoff_coefficient=policy.backoff_coefficient,
        maximum_interval=policy.maximum_interval,
        maximum_attempts=(
            _MAXIMUM_TOOL_ATTEMPTS if attempts <= 0 else min(attempts, _MAXIMUM_TOOL_ATTEMPTS)
        ),
        total_duration=policy.total_duration,
    )


def _package_of(operation: object) -> str:
    return type(operation).__module__


def _read_manifest(operation: object, connector_id: str) -> tuple[dict[str, Any], str]:
    module = type(operation).__module__
    parts = module.split(".")
    # A mock operation is defined in the SDK, so the official package name comes last.
    candidates = [".".join(parts[:length]) for length in range(len(parts), 0, -1)]
    candidates.append(f"dex_connectors.{connector_id.replace('-', '_')}")
    for package in candidates:
        try:
            resource = importlib.resources.files(package).joinpath(CONNECTOR_MANIFEST_FILE_NAME)
        except (ModuleNotFoundError, TypeError):
            continue
        if resource.is_file():
            document: dict[str, Any] = json.loads(resource.read_text(encoding="utf-8"))
            if document.get("connectorId") == connector_id:
                return document, package
    raise LookupError(
        f"no {CONNECTOR_MANIFEST_FILE_NAME} of Connector {connector_id} found for {module}; "
        "pass manifest="
    )


def _input_type(package: str, input_type_name: object, label: str) -> type[Any]:
    if not isinstance(input_type_name, str) or not input_type_name:
        raise ValueError(f"{label}: the manifest has no inputType")
    parts = package.split(".")
    for length in range(len(parts), 0, -1):
        prefix = ".".join(parts[:length])
        for module_name in (prefix, f"{prefix}.models"):
            try:
                module = importlib.import_module(module_name)
            except ModuleNotFoundError:
                continue
            found = getattr(module, input_type_name, None)
            if isinstance(found, type):
                return found
    raise LookupError(f"{label}: package {package} does not export {input_type_name}")


def _snake_case(identifier: str) -> str:
    return "".join(f"_{c.lower()}" if c.isupper() else c for c in identifier).lstrip("_")
