# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Sustainable Use License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass, replace
from datetime import datetime, timedelta
from enum import Enum
from typing import Any, TypeVar, overload

from dex._task_queue_names import target_task_queue_name_error
from dex._utils import require_map_instance
from dex.attribute import Attribute, AttributeLock, AttributeMap, AttributeMapLoad
from dex.channel import Channel, ChannelMap, ChannelMapLoad
from dex.flow_config import FlowConfig
from dex.step import RetryPolicy, StepDurability, StepOptions, StepTarget

ValueT = TypeVar("ValueT")


class IdReusePolicy(Enum):
    """Control whether ``start_flow`` may reuse an existing Flow ID.

    Attributes:
        DEFAULT: Reject the start whenever the Flow ID has a run, the same as
            ``DISALLOW``. The server does not make this default configurable.
        ALLOW_IF_PREVIOUS_FAILED: Start a new run only when the latest run
            closed abnormally: failed, canceled, or terminated. A Flow timeout
            closes a run as failed or canceled under the fail or cancel
            timeout policy. Under the handler policy, the timeout handler's
            outcome decides. A running or completed latest run rejects the
            start.
        ALLOW_IF_NOT_RUNNING: Reuse after any closed run.
        ALLOW_TERMINATE_IF_RUNNING: Terminate an active run and start the new run.
        DISALLOW: Reject every previously used Flow ID.
    """

    DEFAULT = "default"
    ALLOW_IF_PREVIOUS_FAILED = "allow_if_previous_failed"
    ALLOW_IF_NOT_RUNNING = "allow_if_not_running"
    ALLOW_TERMINATE_IF_RUNNING = "allow_terminate_if_running"
    DISALLOW = "disallow"


class FlowTimeoutPolicy(Enum):
    """Control how Dex responds when a positive soft Flow timeout expires.

    Attributes:
        DEFAULT: Invoke ``Flow.handle_timeout`` when overridden; otherwise fail.
        FAIL: Fail with ``FlowErrorType.FLOW_TIMEOUT`` and permit Flow retries.
        CANCEL: Cancel without retrying the Flow.
        HANDLER: Start one retryable ``Flow.handle_timeout`` execution after its timer.
    """

    DEFAULT = "default"
    FAIL = "fail"
    CANCEL = "cancel"
    HANDLER = "handler"


def _resolve_flow_timeout_policy(
    flow_name: str,
    has_timeout_handler: bool,
    timeout: timedelta | None,
    policy: FlowTimeoutPolicy,
) -> FlowTimeoutPolicy:
    if timeout is None or timeout.total_seconds() <= 0:
        if policy is not FlowTimeoutPolicy.DEFAULT:
            raise ValueError("Flow timeout policy requires a positive timeout")
        return FlowTimeoutPolicy.DEFAULT
    if policy is FlowTimeoutPolicy.DEFAULT:
        return (
            FlowTimeoutPolicy.HANDLER if has_timeout_handler else FlowTimeoutPolicy.FAIL
        )
    if policy is FlowTimeoutPolicy.HANDLER and not has_timeout_handler:
        raise ValueError(f"Flow {flow_name} has no handle_timeout override")
    return policy


def _require_valid_target_task_queue(task_queue: str) -> None:
    task_queue_error = target_task_queue_name_error(task_queue)
    if task_queue_error is not None:
        raise ValueError(f"task_queue {task_queue!r} is invalid: {task_queue_error}")


@dataclass(frozen=True)
class _AttributeInitialization:
    definition: Attribute[Any] | AttributeMap[Any]
    instance: str | None
    value: object


@dataclass(frozen=True)
class FlowTimeoutHandlerOptions:
    """Configure timeout-handler execution and selective state loading.

    Ordinary Attributes and Channel size metadata load automatically. AttributeMap
    values and pending Channel messages require an explicit selection. One logical
    handler execution may contain multiple retry attempts.
    See StepOptions for shared snapshot, lock, heartbeat, durability, and retry
    semantics. Timing begins after the soft Flow timeout fires, so the handler
    can outlive that original Flow deadline. method_timeout defaults to two hours;
    heartbeat_timeout defaults to one minute. Durations use whole seconds.

    Attributes:
        method_timeout: Maximum duration of one handler attempt.
        heartbeat_timeout: Progress deadline for one regular attempt.
        retry: Optional retry policy; ``None`` uses server defaults.
        durability: Durability for successful handler side effects.
        lock_attributes: Attribute locks held during the handler invocation.
        load_attribute_maps: AttributeMaps whose current instances load.
        load_attribute_map_instances: Exact AttributeMap instances to load.
        load_channels: Channels whose pending messages load.
        load_channel_maps: ChannelMaps whose current instance messages load.
        load_channel_map_instances: Exact ChannelMap instances to load.

    Examples:
        >>> options = FlowTimeoutHandlerOptions(
        ...     method_timeout=timedelta(seconds=30),
        ...     retry=RetryPolicy(maximum_attempts=3),
        ...     load_channels=(commands,),
        ... ).on_failure_proceed_to(TimeoutRecovery)
    """

    method_timeout: timedelta | None = None
    heartbeat_timeout: timedelta | None = None
    retry: RetryPolicy | None = None
    durability: StepDurability = StepDurability.DEFAULT
    lock_attributes: tuple[AttributeLock, ...] = ()
    load_attribute_maps: tuple[AttributeMap[Any], ...] = ()
    load_attribute_map_instances: tuple[AttributeMapLoad, ...] = ()
    load_channels: tuple[Channel[Any], ...] = ()
    load_channel_maps: tuple[ChannelMap[Any], ...] = ()
    load_channel_map_instances: tuple[ChannelMapLoad, ...] = ()
    _failure_target: StepTarget[None] | None = None
    _failure_options: StepOptions | None = None

    def on_failure_proceed_to(
        self,
        step: StepTarget[None],
        options: StepOptions | None = None,
    ) -> FlowTimeoutHandlerOptions:
        """Return a copy routing exhausted retries to a registered no-input Step.

        The recovery Step receives ``None`` and reads the final handler failure from
        ``Context.recovery_error``. Select it by class, registered instance, or
        StepRef. Use an instance or StepRef when the Flow registers more than one
        instance of the recovery Step class. The target is validated when Client
        or SubFlow start options are mapped, not when this method is called.

        .. versionchanged:: 1.28.0
           Targets accept registered instances and StepRef as well as unambiguous classes.

        Args:
            step: Recovery class, registered instance, or StepRef. It must belong to
                the timed-out Flow and use ``None`` input.
            options: Optional execution overrides for the recovery movement.

        Returns:
            A new options value with failure routing configured.

        Examples:
            >>> options = FlowTimeoutHandlerOptions(
            ...     retry=RetryPolicy(maximum_attempts=1),
            ... ).on_failure_proceed_to(StepRef("TimeoutRecovery"))
        """
        return replace(self, _failure_target=step, _failure_options=options)


@dataclass(frozen=True)
class StartFlowOptions:
    """Configure creation of a new Flow execution.

    Durations are :class:`datetime.timedelta` values. ``None`` uses the registered
    Flow or server default. Use :meth:`with_attribute` to add initial Attribute writes.

    To retry one logical start safely, for example after a lost response, set a
    stable ``request_id`` and ``ignore_already_started=True`` together. Reuse the
    same values on every retry.

    Attributes:
        timeout: Optional durable soft timeout. ``None`` or zero disables it.
        timeout_policy: Action taken when a positive timeout expires.
        timeout_handler_options: Execute policy used when timeout policy is ``HANDLER``.
        start_delay: Optional delay before the starting Step becomes eligible.
            The current Server rejects a positive value with INVALID_ARGUMENT.
            Use a Timer wait in the starting Step instead.
        id_reuse_policy: Flow ID reuse policy; defaults to ``DEFAULT``.
        retry_policy: Optional Flow-level retry policy. The current Server rejects
            it with INVALID_ARGUMENT. Use StepOptions retry policies instead.
        config_override: Optional FlowConfig applied over registered defaults.
        ignore_already_started: Return the existing run rather than raising
            ``FlowAlreadyStartedError`` when supported by the server. It applies
            only when ``request_id`` matches the request ID that started the
            existing run. A different or generated request ID still raises
            ``FlowAlreadyStartedError``.
        request_id: Optional non-empty idempotency key. ``None`` generates one.
            A retried start returns the existing run only when
            ``ignore_already_started`` is ``True`` and this request ID matches
            the request that started that run. Without that option, the retry
            raises ``FlowAlreadyStartedError`` even when the first attempt
            created the Flow. The match uses the latest run of the Flow ID and
            takes precedence over the ID reuse policy. A retry never starts a
            second run or terminates the run it started. Dex compares only
            request IDs, not input or other options, so never reuse a request
            ID for a different logical start.
        task_queue: The named task queue that runs this Flow. The empty
            default uses the default task queue, the same as a start without
            this option.

            Only Workers whose ``PullWorkerOptions.task_queue`` is this name,
            and that register this Flow type, receive the Flow's Steps and
            RPCs. ``dex.default`` is the exception: it selects the default
            task queue, which Workers with an empty ``task_queue`` serve;
            Workers cannot register ``dex.default`` itself. Its SubFlows inherit the task queue unless their
            ``SubFlowOptions.task_queue`` selects another. The task queue is
            fixed when the Flow starts; ``update_flow_config`` cannot change
            it. While no Worker polls the task queue, the Flow's Steps wait
            and RPCs fail once the Server stops waiting for a Worker.

            A name has 1 to 63 ASCII letters, digits, dots, underscores, or
            hyphens. Names starting with ``dex.`` are reserved, except
            ``dex.default``, which selects the default task queue explicitly.
            An invalid name raises ``ValueError`` when the options are
            constructed.

            A non-empty value requires Dex Server protocol version 5 or later.
            ``start_flow`` raises ``RuntimeError`` without sending the request
            when the Server is older.

            .. versionadded:: 1.45.1

    Examples:
        >>> options = (
        ...     StartFlowOptions(timeout=timedelta(hours=1))
        ...     .with_attribute(status, "pending")
        ...     .with_attribute(balances, "merchant-7", 1200)
        ... )
        >>> retry_safe_options = StartFlowOptions(
        ...     ignore_already_started=True,
        ...     request_id=f"start-order-{order_id}",
        ... )
        >>> run_id = client.start_flow(
        ...     order_flow, f"order-{order_id}", order_input, retry_safe_options
        ... )
        >>> client.start_flow(
        ...     render_flow, "render-7", render_input, StartFlowOptions(task_queue="gpu")
        ... )
    """

    timeout: timedelta | None = None
    timeout_policy: FlowTimeoutPolicy = FlowTimeoutPolicy.DEFAULT
    timeout_handler_options: FlowTimeoutHandlerOptions | None = None
    start_delay: timedelta | None = None
    id_reuse_policy: IdReusePolicy = IdReusePolicy.DEFAULT
    retry_policy: RetryPolicy | None = None
    _attribute_initializations: tuple[_AttributeInitialization, ...] = ()
    config_override: FlowConfig | None = None
    ignore_already_started: bool = False
    request_id: str | None = None
    task_queue: str = ""

    def __post_init__(self) -> None:
        _require_valid_target_task_queue(self.task_queue)

    @overload
    def with_attribute(
        self,
        attribute: Attribute[ValueT],
        value: ValueT,
        /,
    ) -> StartFlowOptions: ...

    @overload
    def with_attribute(
        self,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        /,
    ) -> StartFlowOptions: ...

    def with_attribute(
        self,
        attribute: Attribute[Any] | AttributeMap[Any],
        /,
        *args: object,
    ) -> StartFlowOptions:
        """Return a copy with one initial Attribute value appended.

        Singleton Attributes take ``(attribute, value)``; AttributeMaps take
        ``(attribute, instance, value)``. Calls preserve initialization order.

        Args:
            attribute: A registered Attribute or AttributeMap definition.
            *args: The value, or the AttributeMap instance followed by its value.

        Returns:
            A new options object containing the additional initialization.

        Raises:
            TypeError: If arguments do not match either supported form.
            ValueError: If an AttributeMap instance is empty or contains ``/``.
        """
        if isinstance(attribute, Attribute) and len(args) == 1:
            initialization = _AttributeInitialization(attribute, None, args[0])
        elif isinstance(attribute, AttributeMap) and len(args) == 2:
            instance = args[0]
            if not isinstance(instance, str):
                raise TypeError("attribute-map instance must be a string")
            require_map_instance(instance)
            initialization = _AttributeInitialization(attribute, instance, args[1])
        else:
            raise TypeError("with_attribute received invalid arguments")
        return replace(
            self,
            _attribute_initializations=self._attribute_initializations
            + (initialization,),
        )


class SubFlowReusePolicy(Enum):
    """Control how a generated SubFlow Flow ID resolves an existing execution.

    Attributes:
        ATTACH: Attach to a running execution or return its terminal result.
        RESTART_IF_PREVIOUS_EXITS_ABNORMALLY: Attach while running, return a
            successful result, and restart an abnormal previous execution.
        ALWAYS_RESTART: Replace any different existing execution.
    """

    ATTACH = "attach"
    RESTART_IF_PREVIOUS_EXITS_ABNORMALLY = "restart_if_previous_exits_abnormally"
    ALWAYS_RESTART = "always_restart"


@dataclass(frozen=True)
class SubFlowOptions:
    """Configure one durable SubFlow Condition.

    The SubFlow inherits the parent's effective Flow configuration. ``None`` values
    preserve normal start defaults. Dex generates the Flow ID and request ID.

    The Server validates these options when the ``wait_for`` result returns
    them. A rejected value fails that ``wait_for`` attempt as an invalid
    response. Dex retries it under the ``wait_for`` retry policy and then
    applies the Step's ``wait_for`` failure policy. A retry that returns the
    same options fails again.

    Attributes:
        timeout: Optional maximum SubFlow lifetime.
        timeout_policy: Action taken when a positive timeout expires.
        timeout_handler_options: Execute policy used when timeout policy is ``HANDLER``.
        start_delay: Optional delay before its starting Step. The current Server
            rejects a positive value when it validates the ``wait_for`` result
            that returns this Condition. Use a Timer wait in the child instead.
        retry_policy: Optional whole-Flow retry policy. The current Server rejects
            a supplied policy when it validates the ``wait_for`` result. Use
            StepOptions retry policies in the child instead.
        config_override: Fields applied over the inherited parent configuration.
        reuse_policy: Existing-execution resolution policy.
        condition_id: Stable ID required by ``Wait.any_combination_of``.
            None is sufficient for other Wait forms unless selecting by ID.
        task_queue: The named task queue that runs the SubFlow. The empty
            default inherits the parent Flow's task queue. ``dex.default``
            selects the default task queue. Any other name selects that named
            task queue. The name rules match ``StartFlowOptions.task_queue``,
            and an invalid name raises ``ValueError`` when the options are
            constructed.

            A non-empty value requires Dex Server protocol version 5 or later.
            Against an older Server, returning the Condition fails the Step
            attempt with ``RuntimeError``, and Dex retries it under the Step's
            retry policy.

            Example:
                >>> SubFlowOptions(task_queue="dex.default")

            .. versionadded:: 1.45.1
    """

    timeout: timedelta | None = None
    timeout_policy: FlowTimeoutPolicy = FlowTimeoutPolicy.DEFAULT
    timeout_handler_options: FlowTimeoutHandlerOptions | None = None
    start_delay: timedelta | None = None
    retry_policy: RetryPolicy | None = None
    _attribute_initializations: tuple[_AttributeInitialization, ...] = ()
    config_override: FlowConfig | None = None
    reuse_policy: SubFlowReusePolicy = (
        SubFlowReusePolicy.RESTART_IF_PREVIOUS_EXITS_ABNORMALLY
    )
    condition_id: str | None = None
    task_queue: str = ""

    def __post_init__(self) -> None:
        _require_valid_target_task_queue(self.task_queue)

    @overload
    def with_attribute(
        self, attribute: Attribute[ValueT], value: ValueT, /
    ) -> SubFlowOptions: ...

    @overload
    def with_attribute(
        self,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        /,
    ) -> SubFlowOptions: ...

    def with_attribute(
        self,
        attribute: Attribute[Any] | AttributeMap[Any],
        /,
        *args: object,
    ) -> SubFlowOptions:
        """Return a copy with one target-Flow Attribute initialization appended.

        Args:
            attribute: A singleton Attribute or AttributeMap owned by the SubFlow.
            *args: Its value, or a map instance followed by its value.

        Returns:
            A new immutable options value.

        Raises:
            TypeError: If arguments do not match either supported form.
            ValueError: If an AttributeMap instance is empty or contains ``/``.
        """
        if isinstance(attribute, Attribute) and len(args) == 1:
            initialization = _AttributeInitialization(attribute, None, args[0])
        elif isinstance(attribute, AttributeMap) and len(args) == 2:
            instance = args[0]
            if not isinstance(instance, str):
                raise TypeError("attribute-map instance must be a string")
            require_map_instance(instance)
            initialization = _AttributeInitialization(attribute, instance, args[1])
        else:
            raise TypeError("with_attribute received invalid arguments")
        return replace(
            self,
            _attribute_initializations=self._attribute_initializations
            + (initialization,),
        )


class TimeTravelType(Enum):
    """Select the historical point from which time travel re-runs the Flow.

    Attributes:
        BEGINNING: Restart from the beginning of the Flow.
        HISTORY_EVENT_TIME: Resume at the first event at or after a timestamp.
        STEP_TYPE: Resume at the latest execution of a Step type.
        STEP_EXECUTION_ID: Resume at one exact Step execution ID.
        EVENT_ID: Resume right after the change that recorded one history event.
            Set ``event_id`` to the event's ID, as the Server's history events
            report it. Events recorded together, such as a Step completion and
            the Steps it starts, share one point, so the restored state includes
            them all. An event inside a Step attempt resolves to the next point
            where the Flow can stop. The event must still be in the run's
            history window.

            .. versionadded:: 1.45.3
        CURRENT: The latest committed state of the source run's current run
            attempt. Only ``clone_fork_flow`` accepts it, and it is the default
            of :class:`CloneForkFlowOptions`. ``time_travel`` raises
            ``ValueError`` for it, because a Flow cannot return to the state it
            is in.

            .. versionadded:: 1.45.3
    """

    BEGINNING = "beginning"
    HISTORY_EVENT_TIME = "history_event_time"
    STEP_TYPE = "step_type"
    STEP_EXECUTION_ID = "step_execution_id"
    EVENT_ID = "event_id"
    CURRENT = "current"


class TimeTravelStepMethod(Enum):
    """Select the Step method used as a Step execution time travel boundary.

    Attributes:
        WAIT_FOR: Rerun WaitFor and everything after it.
        EXECUTE: Keep the WaitFor result and rerun Execute and everything after it.
    """

    WAIT_FOR = "wait_for"
    EXECUTE = "execute"


class SubFlowResetPolicy(Enum):
    """Decide what happens to a SubFlow condition that a restored Step waits on.

    ``time_travel`` and ``clone_fork_flow`` restore Steps from an earlier state.
    A restored Step whose WaitFor waits on SubFlow conditions names, in each
    condition, the child Flow that the Step started. The policy decides, per
    condition, whether the restored wait attaches to that child, restarts the
    child, fails the condition, or rejects the whole call.

    Attaching keeps the condition on its original child. When the child run
    has ended, its result completes the condition. When the child still runs,
    the condition keeps waiting for it. A child whose run no longer exists
    cannot be attached; the ``ATTACH_ELSE`` policies then take their second
    action. Under ``clone_fork_flow``, the source run's children report only to
    the source run, so a child that still runs cannot be attached either.

    Restarting starts the child again with the start it originally received.
    Under ``time_travel``, the child keeps its Flow ID and a still-running run
    of it is terminated first. Under ``clone_fork_flow``, the restart starts a
    new child Flow whose Flow ID derives from the new Flow ID, and the source
    run's child never changes. A child whose run no longer exists cannot be
    restarted, and the call fails.

    Failing completes the condition with a failed child result whose error
    type is ``FlowErrorType.SUB_FLOW_ABANDONED``; the Step's Execute receives it
    like any failed child. Under ``time_travel``, a child that still runs is
    terminated.

    Rejecting fails the call with ``DexServiceError`` whose code is
    ``FAILED_PRECONDITION``, and nothing changes. Its detail names each Step
    execution and child Flow ID that caused it.

    Attributes:
        DEFAULT: Leave the choice to the Server, which uses
            ``ATTACH_ELSE_RESTART``.
        ALWAYS_RESTART: Restart every child, whether it ended or still runs.
        ALWAYS_FAIL: Fail every condition, whether its child ended or still runs.
        ALWAYS_REJECT: Reject the call when any restored Step waits on a SubFlow
            condition.
        ATTACH_ELSE_RESTART: Attach each condition to its child; restart a child
            that cannot be attached.
        ATTACH_ELSE_FAIL: Attach each condition to its child; fail a condition
            whose child cannot be attached.
        ATTACH_ELSE_REJECT: Attach each condition to its child; reject the call
            when a child cannot be attached.

    Examples:
        >>> client.time_travel(
        ...     "order-123",
        ...     TimeTravelOptions(
        ...         type=TimeTravelType.STEP_TYPE,
        ...         step_type="ShipOrder",
        ...         sub_flow_reset_policy=SubFlowResetPolicy.ATTACH_ELSE_FAIL,
        ...     ),
        ... )

    .. versionadded:: 1.45.3
    """

    DEFAULT = "default"
    ALWAYS_RESTART = "always_restart"
    ALWAYS_FAIL = "always_fail"
    ALWAYS_REJECT = "always_reject"
    ATTACH_ELSE_RESTART = "attach_else_restart"
    ATTACH_ELSE_FAIL = "attach_else_fail"
    ATTACH_ELSE_REJECT = "attach_else_reject"


def _validate_history_point(
    operation: str,
    point_type: TimeTravelType,
    history_event_time: datetime | None,
    step_type: str | None,
    step_execution_id: str | None,
    step_method: TimeTravelStepMethod | None,
    event_id: int | None,
) -> None:
    selectors = {
        TimeTravelType.HISTORY_EVENT_TIME: history_event_time is not None,
        TimeTravelType.STEP_TYPE: bool(step_type),
        TimeTravelType.STEP_EXECUTION_ID: bool(step_execution_id),
        TimeTravelType.EVENT_ID: event_id is not None,
    }
    has_step_method = step_method is not None
    others = [
        selector_type
        for selector_type, is_set in selectors.items()
        if is_set and selector_type is not point_type
    ]
    if point_type not in selectors:
        if others or has_step_method:
            raise ValueError(
                f"{operation} to {point_type.value} cannot include another selector"
            )
        return
    if not selectors[point_type]:
        raise ValueError(f"{operation} {point_type.value} selector must be set")
    if others or (
        has_step_method and point_type is not TimeTravelType.STEP_EXECUTION_ID
    ):
        raise ValueError(
            f"{operation} {point_type.value} cannot be combined with another selector"
        )
    if point_type is TimeTravelType.STEP_EXECUTION_ID and not has_step_method:
        raise ValueError(f"{operation} step_method must be WAIT_FOR or EXECUTE")
    if point_type is TimeTravelType.EVENT_ID and (
        isinstance(event_id, bool) or not isinstance(event_id, int) or event_id <= 0
    ):
        raise ValueError(f"{operation} event_id must be a positive integer")


@dataclass(frozen=True)
class TimeTravelOptions:
    """Configure a time travel that re-runs a Flow from a point in its history.

    Exactly one selector matching ``type`` must be set. The Client
    validates combinations before sending the request.
    Time travel keeps the run ID, and a re-run can reuse Step execution IDs.

    Attributes:
        type: The time travel point selector kind.
        history_event_time: Timestamp used by ``HISTORY_EVENT_TIME``.
        step_type: Registered Step type used by ``STEP_TYPE``.
        step_execution_id: Exact execution ID used by ``STEP_EXECUTION_ID``.
        step_method: WaitFor or Execute boundary required by ``STEP_EXECUTION_ID``.
        reason: Optional operator-readable time travel reason.
        skip_writes_reapply: Do not replay RPCs, Channel publications, or Attribute
            writes after the selected point.
        event_id: Positive history event ID used by ``EVENT_ID``; leave it
            ``None`` for the other types.

            .. versionadded:: 1.45.3
        sub_flow_reset_policy: What happens to the SubFlow conditions that
            restored Steps wait on. ``DEFAULT`` uses ``ATTACH_ELSE_RESTART``.

            .. versionadded:: 1.45.3
    """

    type: TimeTravelType
    history_event_time: datetime | None = None
    step_type: str | None = None
    step_execution_id: str | None = None
    step_method: TimeTravelStepMethod | None = None
    reason: str | None = None
    skip_writes_reapply: bool = False
    event_id: int | None = None
    sub_flow_reset_policy: SubFlowResetPolicy = SubFlowResetPolicy.DEFAULT


@dataclass(frozen=True)
class CloneForkFlowOptions:
    """Configure ``clone_fork_flow``: the copied point and the new Flow's start.

    The default copies the latest committed state of the source Flow's latest
    run, keeps the source run's Flow configuration, task queue, and Flow
    timeout duration, and sends a new SDK-generated request ID.

    The point fields work as in :class:`TimeTravelOptions`; ``type`` selects
    which one Dex reads. ``CURRENT``, the default, takes no selector.
    ``BEGINNING`` selects the state right after the source run started. The
    other types read ``history_event_time``, ``step_type``,
    ``step_execution_id`` with ``step_method``, or ``event_id``. Every point
    other than the current one must be inside the source run's history window,
    and the state at the point must be running. Invalid combinations raise
    ``ValueError`` when the options are constructed.

    Attributes:
        type: The copied point. Defaults to ``CURRENT``.
        history_event_time: Timestamp used by ``HISTORY_EVENT_TIME``.
        step_type: Registered Step type used by ``STEP_TYPE``.
        step_execution_id: Exact execution ID used by ``STEP_EXECUTION_ID``.
        step_method: WaitFor or Execute boundary required by
            ``STEP_EXECUTION_ID``.
        event_id: Positive history event ID used by ``EVENT_ID``.
        source_run_id: The run of the source Flow ID to copy. The empty default
            selects the latest run. The source run may be closed.
        source_run_attempt: The run attempt of the source run whose history
            holds the point. Zero, the default, selects the current run attempt.
            ``CURRENT`` accepts only zero.
        id_reuse_policy: Whether the call may start a run for a Flow ID that
            already has a run, as for ``start_flow``.
        ignore_already_started: Return the existing run instead of raising
            ``FlowAlreadyStartedError`` when ``request_id`` matches the request
            that created it, as for ``start_flow``.
        request_id: Optional non-empty idempotency key. ``None`` generates one.
            Set a stable ID, together with ``ignore_already_started``, to retry
            a call whose outcome is unknown: a retry with the request ID of the
            new Flow's latest run copies nothing again.
        config_override: Optional FlowConfig whose set fields change the
            configuration copied from the source run. Unset fields keep the
            source run's values.
        task_queue: The task queue of the new Flow. The empty default keeps the
            source run's task queue; ``dex.default`` selects the default task
            queue. Set a task queue when Workers pick their Flows by task
            queue, so that the source run's Workers do not run the new Flow's
            Steps. Names follow ``StartFlowOptions.task_queue``.
        timeout: The new Flow's Flow timeout in whole seconds, counted from the
            call. ``None`` or zero keeps the source run's Flow timeout
            duration, also counted from the call; a source run without a Flow
            timeout gives a new Flow without one. The new Flow keeps the source
            run's timeout policy and handler options.
        reason: Optional text that explains the clone fork. Dex records it with
            the new Flow's start.
        sub_flow_reset_policy: What happens to the SubFlow conditions that
            copied Steps wait on. ``DEFAULT`` uses ``ATTACH_ELSE_RESTART``. A
            child that ended attaches; a child that still runs takes the
            policy's second action.

    Use :meth:`with_attribute` to add Attribute writes that Dex applies after
    the copy, in the same change that creates the new Flow.

    Examples:
        >>> options = CloneForkFlowOptions(
        ...     type=TimeTravelType.EVENT_ID,
        ...     event_id=42,
        ...     request_id=f"branch-{session_id}",
        ...     reason="try another answer",
        ... ).with_attribute(label, "branch")
        >>> result = client.clone_fork_flow(
        ...     session_flow, session_id, f"{session_id}-branch", options
        ... )

    .. versionadded:: 1.45.3
    """

    type: TimeTravelType = TimeTravelType.CURRENT
    history_event_time: datetime | None = None
    step_type: str | None = None
    step_execution_id: str | None = None
    step_method: TimeTravelStepMethod | None = None
    event_id: int | None = None
    source_run_id: str = ""
    source_run_attempt: int = 0
    id_reuse_policy: IdReusePolicy = IdReusePolicy.DEFAULT
    ignore_already_started: bool = False
    request_id: str | None = None
    config_override: FlowConfig | None = None
    task_queue: str = ""
    timeout: timedelta | None = None
    reason: str | None = None
    sub_flow_reset_policy: SubFlowResetPolicy = SubFlowResetPolicy.DEFAULT
    _attribute_initializations: tuple[_AttributeInitialization, ...] = ()

    def __post_init__(self) -> None:
        _validate_history_point(
            "clone fork",
            self.type,
            self.history_event_time,
            self.step_type,
            self.step_execution_id,
            self.step_method,
            self.event_id,
        )
        if self.source_run_attempt < 0:
            raise ValueError("clone fork source_run_attempt must not be negative")
        if self.type is TimeTravelType.CURRENT and self.source_run_attempt != 0:
            raise ValueError(
                "clone fork to the current point accepts only source_run_attempt 0"
            )
        if self.timeout is not None and self.timeout.total_seconds() < 0:
            raise ValueError("clone fork timeout must not be negative")
        if self.request_id == "":
            raise ValueError("clone fork request_id must not be empty")
        if self.task_queue:
            _require_valid_target_task_queue(self.task_queue)

    @overload
    def with_attribute(
        self,
        attribute: Attribute[ValueT],
        value: ValueT,
        /,
    ) -> CloneForkFlowOptions: ...

    @overload
    def with_attribute(
        self,
        attribute: AttributeMap[ValueT],
        instance: str,
        value: ValueT,
        /,
    ) -> CloneForkFlowOptions: ...

    def with_attribute(
        self,
        attribute: Attribute[Any] | AttributeMap[Any],
        /,
        *args: object,
    ) -> CloneForkFlowOptions:
        """Return a copy with one Attribute write appended.

        Singleton Attributes take ``(attribute, value)``; AttributeMaps take
        ``(attribute, instance, value)``. Dex applies the writes in order after
        the copy.

        Args:
            attribute: An Attribute or AttributeMap definition of the Flow.
            *args: The value, or the AttributeMap instance followed by its value.

        Returns:
            A new options object containing the additional write.

        Raises:
            TypeError: If arguments do not match either supported form.
            ValueError: If an AttributeMap instance is empty or contains ``/``.
        """
        return replace(
            self,
            _attribute_initializations=self._attribute_initializations
            + (_attribute_initialization(attribute, args),),
        )


@dataclass(frozen=True)
class CloneForkFlowResult:
    """Describe the run that ``clone_fork_flow`` created and the copied point.

    Attributes:
        run_id: The run ID of the new Flow's run.
        source_run_id: The run ID of the source run.
        source_run_attempt: The run attempt of the source run that holds the
            copied point.
        source_event_id: The ID of the last source history event that the
            copied point includes.

    .. versionadded:: 1.45.3
    """

    run_id: str
    source_run_id: str
    source_run_attempt: int
    source_event_id: int


def _attribute_initialization(
    attribute: Attribute[Any] | AttributeMap[Any], args: tuple[object, ...]
) -> _AttributeInitialization:
    if isinstance(attribute, Attribute) and len(args) == 1:
        return _AttributeInitialization(attribute, None, args[0])
    if isinstance(attribute, AttributeMap) and len(args) == 2:
        instance = args[0]
        if not isinstance(instance, str):
            raise TypeError("attribute-map instance must be a string")
        require_map_instance(instance)
        return _AttributeInitialization(attribute, instance, args[1])
    raise TypeError("with_attribute received invalid arguments")


class StopType(Enum):
    """Select how an active Flow should close.

    Attributes:
        CANCEL: Request cooperative cancellation.
        TERMINATE: Force immediate termination.
        FAIL: Close the Flow as failed with the reason in :class:`StopFlowOptions`.
    """

    CANCEL = "cancel"
    TERMINATE = "terminate"
    FAIL = "fail"


@dataclass(frozen=True)
class StopFlowOptions:
    """Configure a ``stop_flow`` request.

    Attributes:
        type: The stop behavior; defaults to cooperative cancellation.
        reason: Optional operator-readable reason recorded by Dex.
    """

    type: StopType = StopType.CANCEL
    reason: str | None = None
