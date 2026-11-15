# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from enum import Enum
from typing import Literal

import grpc


class ErrorSubStatus(Enum):
    """Classify a FlowService error more precisely than its gRPC status.

    Attributes:
        UNCATEGORIZED: Dex returned no specific classification.
        FLOW_ALREADY_STARTED: The requested Flow ID conflicts with an existing run.
        FLOW_NOT_EXISTS: No Flow with the requested ID exists.
        WORKER_API_ERROR: An application Worker rejected or failed an invocation.
        LONG_POLL_TIMEOUT: A wait ended without observing its condition.
        CHANNEL_MESSAGE_NOT_FOUND: A pending Channel message ID no longer exists.
        REQUEST_TIMEOUT: A caller-defined request budget expired.
        FLOW_CLOSING: Dex rejected an update-class request during Flow close cleanup.
    """

    UNCATEGORIZED = "uncategorized"
    FLOW_ALREADY_STARTED = "flow_already_started"
    FLOW_NOT_EXISTS = "flow_not_exists"
    WORKER_API_ERROR = "worker_api_error"
    LONG_POLL_TIMEOUT = "long_poll_timeout"
    CHANNEL_MESSAGE_NOT_FOUND = "channel_message_not_found"
    REQUEST_TIMEOUT = "request_timeout"
    FLOW_CLOSING = "flow_closing"


class FlowErrorType(Enum):
    """Identify the terminal failure category reported for a Flow.

    Attributes:
        STEP_DECISION_FAILED: A Step decision could not be applied.
        CLIENT_API_FAILED: A Client-originated operation failed the Flow.
        WORKER_API_FAILED: A Worker handler or dispatch failed.
        INVALID_USER_FLOW_CODE: Application Flow definitions or results were invalid.
        INTERNAL: Dex encountered an internal invariant or infrastructure failure.
        FLOW_TIMEOUT: A Dex soft Flow timeout expired under the fail policy.
        SUB_FLOW_ABANDONED: The parent Flow stopped waiting for this SubFlow child.
            A Time Travel restored a parent Step that waited on a SubFlow
            condition, and its SubFlow reset policy chose to fail that
            condition. Dex terminates the child run if it still runs. The
            condition then completes with a ``FlowStatus.FAILED`` result that
            carries this error type. Only a SubFlow result read in Execute
            carries it; the child run itself never closes with it. Handle it
            like any other failed child: retry, compensate, or fail the parent.
            The child's output is not available.

            .. versionadded:: 1.45.3

    A category that this SDK version does not recognize reads as ``None``
    from ``FlowResult.error_type``.

    Examples:
        >>> result = SubFlow.get_condition_results(context)
        >>> if result.error_type is FlowErrorType.SUB_FLOW_ABANDONED:
        ...     raise RuntimeError("child abandoned by a Time Travel")
    """

    STEP_DECISION_FAILED = "step_decision_failed"
    CLIENT_API_FAILED = "client_api_failed"
    WORKER_API_FAILED = "worker_api_failed"
    INVALID_USER_FLOW_CODE = "invalid_user_flow_code"
    INTERNAL = "internal"
    FLOW_TIMEOUT = "flow_timeout"
    SUB_FLOW_ABANDONED = "sub_flow_abandoned"


class DexServiceError(RuntimeError):
    """Represent a structured error returned by a Dex FlowService operation.

    Catch the narrowest SDK error that the operation documents. Inspect code and
    sub_status for other service failures; do not match human-readable detail.
    UNKNOWN or UNCATEGORIZED metadata does not imply success or a safe retry.
    A failed transport call can have an unknown commit outcome, so repeating a
    mutation requires application idempotency. ValueMappingError and definition
    validation failures describe local mapping or schema problems instead.

    Servers may reject excess Attribute index keys or oversized indexed Keyword
    values. This SDK exposes those rejections as DexServiceError with
    UNCATEGORIZED sub_status; the outer status and detail remain available.

    Examples:
        >>> import grpc
        >>> from dex import DexServiceError, FlowAlreadyStartedError
        >>> try:
        ...     run_id = client.start_flow(orders, "order-42", order_input)
        ... except FlowAlreadyStartedError:
        ...     raise
        ... except DexServiceError as error:
        ...     if error.code == grpc.StatusCode.INVALID_ARGUMENT:
        ...         raise ValueError(error.detail) from error
        ...     raise

    Attributes:
        code: The outer gRPC status code.
        sub_status: The stable Dex-specific error classification.
        detail: The human-readable service detail.
        operation: The Client operation that failed.
        flow_id: The targeted Flow ID, or ``None`` for service-wide calls.
    """

    def __init__(
        self,
        code: grpc.StatusCode,
        sub_status: ErrorSubStatus,
        detail: str,
        operation: str,
        flow_id: str | None,
    ) -> None:
        """Create a structured FlowService error.

        Args:
            code: The outer gRPC status code.
            sub_status: The Dex-specific classification.
            detail: Human-readable error detail.
            operation: The Client operation name.
            flow_id: The targeted Flow ID, if any.
        """
        super().__init__(detail)
        self.code = code
        self.sub_status = sub_status
        self.detail = detail
        self.operation = operation
        self.flow_id = flow_id


class BlobMessageTooLargeError(DexServiceError):
    """Report a blob value that alone exceeds a gRPC message size limit.

    Dex stores large values as blobs, and the SDK hydrates them with LoadBlobs. The SDK
    first requests every uncached blob in one call. When that response exceeds a gRPC
    limit, it splits the request in half and retries each half, so N blobs take at most
    2N-1 calls. This error means a single blob still does not fit in one response.

    The limit that rejected the response is either ``grpc_max_message_bytes`` on the
    Client or Worker options, or the Dex server's ``api.grpcMaxMessageBytes``. ``detail``
    keeps gRPC's message, which states the response size and the limit. ``code`` is
    always RESOURCE_EXHAUSTED. The chained ``__cause__`` is the gRPC error with its own
    code: RESOURCE_EXHAUSTED for the SDK's receive limit, or OUT_OF_RANGE for the Dex
    server's send limit. Raise the smaller limit above the value size plus headroom, or
    split the value across several Attributes or AttributeMap instances. A Worker
    reports this as a failed Step or RPC.

    Example:
        >>> try:
        ...     values = client.get_attributes("order-42", report)
        ... except BlobMessageTooLargeError as error:
        ...     logger.warning("blob %s of %s is too large", error.blob_id, error.flow_id)

    Attributes:
        blob_id: The Dex blob ID of the value that did not fit.

    .. versionadded:: 1.38.0
    """

    def __init__(self, flow_id: str, blob_id: str, detail: str) -> None:
        """Create the error for one blob.

        Args:
            flow_id: The Flow ID that owns the blob.
            blob_id: The Dex blob ID of the value that did not fit.
            detail: gRPC's size failure message.
        """
        super().__init__(
            grpc.StatusCode.RESOURCE_EXHAUSTED,
            ErrorSubStatus.UNCATEGORIZED,
            f"blob {blob_id!r} of Flow ID {flow_id!r} exceeds the gRPC message "
            f"size limit: {detail}",
            "LoadBlobs",
            flow_id,
        )
        self.blob_id = blob_id


class FlowAlreadyStartedError(DexServiceError):
    """Indicate that ``start_flow`` conflicts with an existing Flow ID."""

    pass


class FlowClosingError(DexServiceError):
    """Report a request rejected while Dex closes a Flow.

    Re-read the retained terminal Flow state before deciding what to do next.
    Catch this error directly instead of comparing the service detail::

        from dex import FlowClosingError, WaitForStepCompletionOptions

        try:
            await client.wait_for_step_completion(
                flow_id, step_execution_id, WaitForStepCompletionOptions()
            )
        except FlowClosingError:
            terminal_result = await client.wait_for_flow(flow_id)
    """

    pass


class FlowNotFoundError(DexServiceError):
    """Indicate that an operation targeted a Flow ID that does not exist."""

    pass


class FlowNotActiveOrNotFoundError(DexServiceError):
    """Report a missing Flow or an operation that cannot use a closed Flow.

    RPC calls use this error for missing targets, including query-only reads.
    A query-only RPC can read a retained terminal execution when it has no
    locks, transactionality, durable effects, or Server-forced Update routing.
    At that confirmed read boundary, handle this error as not found without
    adding a lifecycle probe. Preserve other service and Worker failures.

    Mutations and active-only RPCs also use this error for closed targets.
    The error does not distinguish a missing Flow from a closed Flow or prove
    that the requested action succeeded. The inherited service metadata and
    chained transport cause remain available. For example::

        try:
            client.invoke_rpc(orders.update_order, order_id, update)
        except FlowNotActiveOrNotFoundError:
            raise OrderUnavailableError(order_id)
    """

    pass


class WorkerInvocationError(DexServiceError):
    """Expose both FlowService and nested Worker handler failure details.

    Attributes:
        code: The outer FlowService gRPC status.
        sub_status: Usually ``WORKER_API_ERROR``.
        detail: The outer service detail.
        operation: The Client operation that invoked the Worker.
        flow_id: The targeted Flow ID, if available.
        worker_code: The nested Worker gRPC status, or ``None`` when unavailable.
        worker_error_type: The Worker-reported application error type.
        worker_error_detail: The Worker-reported human-readable detail.
    """

    def __init__(
        self,
        code: grpc.StatusCode,
        sub_status: ErrorSubStatus,
        detail: str,
        operation: str,
        flow_id: str | None,
        worker_code: grpc.StatusCode | None,
        worker_error_type: str,
        worker_error_detail: str,
    ) -> None:
        """Create an error from outer and nested Worker failure metadata.

        Args:
            code: The outer FlowService gRPC status.
            sub_status: The Dex-specific classification.
            detail: The outer service detail.
            operation: The Client operation name.
            flow_id: The targeted Flow ID, if any.
            worker_code: The nested Worker gRPC status, if available.
            worker_error_type: The nested application error type.
            worker_error_detail: The nested human-readable detail.
        """
        super().__init__(code, sub_status, detail, operation, flow_id)
        self.worker_code = worker_code
        self.worker_error_type = worker_error_type
        self.worker_error_detail = worker_error_detail


class RpcLockConflictError(DexServiceError):
    """Indicate that an RPC could not acquire all requested Attribute locks."""

    pass


class LongPollTimeoutError(DexServiceError):
    """Indicate that a retryable long poll ended before its condition was observed."""

    pass


class RequestTimeoutError(DexServiceError):
    """Indicate that a caller-defined request budget expired."""

    pass


class ChannelMessageNotFoundError(DexServiceError):
    """Indicate that a pending Channel message ID no longer exists."""

    pass


class FlowDefinitionError(RuntimeError):
    """Indicate an invalid Flow definition or a definition the Registry lacks.

    Registry construction raises it for an invalid Flow definition. Client calls
    raise it before contacting Dex when an argument is not registered or does not
    match the registered definition. For example, ``get_attributes`` raises it for
    an Attribute whose value type differs from the one a Flow declares.
    """

    pass


class AttributeMapNotLoadedError(RuntimeError):
    """Indicate that an RPC did not load the requested AttributeMap entries."""

    pass


class ChannelMessagesNotLoadedError(RuntimeError):
    """Indicate that an RPC did not load the requested pending Channel messages."""

    pass


class InvalidStepResultError(FlowDefinitionError):
    """Identify a Step or RPC handler result that violates the SDK contract.

    Attributes:
        flow_type: The containing Flow type.
        step_type: The Step type, or ``None`` for an RPC.
        method: The handler method that returned the invalid value.
        detail: A precise description of the contract violation.
    """

    def __init__(
        self,
        flow_type: str,
        step_type: str | None,
        method: Literal["wait_for", "execute", "rpc"],
        detail: str,
    ) -> None:
        """Create an invalid-result definition error.

        Args:
            flow_type: The containing Flow type.
            step_type: The Step type, or ``None`` for an RPC.
            method: ``"wait_for"``, ``"execute"``, or ``"rpc"``.
            detail: A precise description of the invalid result.
        """
        target = (
            f"Flow {flow_type} Step {step_type}"
            if step_type is not None
            else f"RPC in Flow {flow_type}"
        )
        super().__init__(f"{target} {method} returned an invalid result: {detail}")
        self.flow_type = flow_type
        self.step_type = step_type
        self.method = method
        self.detail = detail


class ValueMappingError(RuntimeError):
    """Report an application-value encoding or decoding failure.

    Attributes:
        operation: The mapping operation, such as ``"encode"`` or ``"decode"``.
        detail: The incompatible type, wire kind, or malformed payload detail.
    """

    def __init__(self, operation: str, detail: str) -> None:
        """Create a mapping error with stable operation context.

        Args:
            operation: The attempted mapping operation.
            detail: The human-readable failure detail.
        """
        super().__init__(f"Cannot {operation} Dex Value: {detail}")
        self.operation = operation
        self.detail = detail


class CloneForkFlowUnsupportedError(RuntimeError):
    """Report a ``clone_fork_flow`` call to a Server that has no CloneForkFlow.

    CloneForkFlow requires Dex Server protocol version 6 or later, which
    server-zd 0.1.33 and later releases provide. The Client raises this error
    before it sends the request, so nothing was created. Upgrade the Server to
    resolve it. It is a ``RuntimeError``, like the other Server protocol
    compatibility errors.

    Attributes:
        negotiated_server_protocol: The protocol version agreed with the Server.
        required_server_protocol: The minimum protocol version for CloneForkFlow.

    Examples:
        >>> try:
        ...     client.clone_fork_flow(session_flow, "session-1", "session-1-b")
        ... except CloneForkFlowUnsupportedError as error:
        ...     print(f"upgrade Dex: needs protocol {error.required_server_protocol}")

    .. versionadded:: 1.45.3
    """

    def __init__(
        self,
        negotiated_server_protocol: int,
        required_server_protocol: int,
        required_server_release: str,
    ) -> None:
        """Create the error for a negotiated protocol older than required.

        Args:
            negotiated_server_protocol: The protocol version agreed with the Server.
            required_server_protocol: The minimum protocol version for CloneForkFlow.
            required_server_release: The first server-zd release with that protocol.
        """
        super().__init__(
            "clone_fork_flow requires Dex Server protocol version "
            f"{required_server_protocol} or later ({required_server_release} or "
            f"later), but the negotiated protocol version is "
            f"{negotiated_server_protocol}; upgrade the Server"
        )
        self.negotiated_server_protocol = negotiated_server_protocol
        self.required_server_protocol = required_server_protocol
