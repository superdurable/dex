// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::any::TypeId;
use std::time::{Duration, SystemTime};

use dex_protocol::dex::{
    CloneForkFlowRequest, CloneForkFlowResponse, FlowAlreadyStartedOptions, FlowCloneForkPointType,
    FlowConfig as ProtoFlowConfig,
};
use tonic::Status;
use uuid::Uuid;

use super::{
    Client, invalid, map_flow_config, map_id_reuse_policy, map_pull_flow_config, optional_seconds,
    require_name, rfc3339, task_queue_name,
};
use crate::sdk_error::{ErrorSubStatus, FlowTargetRequirement, ServiceError};
use crate::start_flow_options::InitialAttribute;
use crate::time_travel_options::{SubFlowResetPolicy, TimeTravelPoint};
use crate::{
    Attribute, AttributeMap, Flow, FlowConfig, IdReusePolicy, SdkError, SdkResult, Step,
    StepExecutionId, TimeTravelStepMethod, Value,
};

#[derive(Clone)]
/// Configures [`Client::clone_fork_flow`]: the copied point of the source run and the start of
/// the new Flow.
///
/// [`CloneForkFlowOptions::new`] copies the latest committed state of the source Flow's latest
/// run. It keeps the source run's Flow configuration, task queue, and Flow timeout duration, and
/// the Client sends a new generated request ID. The other constructors select an earlier point,
/// as the [`crate::TimeTravelOptions`] constructor of the same name selects the point that time
/// travel returns to. Every point other than the current one must be inside the source run's
/// history window, and the state at the point must be running.
///
/// ```
/// use dex_sdk::{CloneForkFlowOptions, SubFlowResetPolicy};
/// use std::time::Duration;
///
/// let options = CloneForkFlowOptions::from_event_id(42)
///     .request_id("branch-session-1")
///     .ignore_already_started(true)
///     .task_queue("branches")
///     .timeout(Duration::from_secs(3_600))
///     .reason("explore another answer")
///     .sub_flow_reset_policy(SubFlowResetPolicy::AttachElseFail);
/// ```
///
/// Since 1.45.2.
pub struct CloneForkFlowOptions {
    point: Option<TimeTravelPoint>,
    source_run_id: String,
    source_run_attempt: u32,
    id_reuse_policy: IdReusePolicy,
    ignore_already_started: bool,
    request_id: Option<String>,
    config_override: Option<FlowConfig>,
    task_queue: String,
    attributes: Vec<InitialAttribute>,
    timeout: Option<Duration>,
    reason: Option<String>,
    sub_flow_reset_policy: Option<SubFlowResetPolicy>,
}

impl CloneForkFlowOptions {
    /// Copies the latest committed state of the source run's current run attempt.
    ///
    /// The copy includes every change that committed before the call read the source, and no
    /// change in flight.
    pub fn new() -> Self {
        Self::at(None)
    }

    /// Copies the state right after the source run started.
    pub fn from_beginning() -> Self {
        Self::at(Some(TimeTravelPoint::Beginning))
    }

    /// Copies the first point at or after `event_time`.
    pub fn from_history_event_time(event_time: SystemTime) -> Self {
        Self::at(Some(TimeTravelPoint::HistoryEventTime(event_time)))
    }

    /// Copies the point before the first execution of the selected Step type.
    pub fn from_step<SomeStep: Step>(step: &SomeStep) -> Self {
        Self::at(Some(TimeTravelPoint::StepType(step.step_type())))
    }

    /// Copies the point before one method of the exact Step execution `step_execution`.
    pub fn from_step_execution(
        step_execution: StepExecutionId,
        method: TimeTravelStepMethod,
    ) -> Self {
        Self::at(Some(TimeTravelPoint::StepExecution(step_execution, method)))
    }

    /// Copies the point right after the change that recorded the history event `event_id`.
    ///
    /// A non-positive `event_id` makes [`Client::clone_fork_flow`] return
    /// [`SdkError::InvalidArgument`] before it sends the request.
    pub fn from_event_id(event_id: i64) -> Self {
        Self::at(Some(TimeTravelPoint::EventId(event_id)))
    }

    /// Selects the source run of the source Flow ID; without it, the latest run is the source.
    ///
    /// The source run may be closed.
    pub fn source_run_id(mut self, run_id: impl Into<String>) -> Self {
        self.source_run_id = run_id.into();
        self
    }

    /// Selects the run attempt of the source run whose history holds the point.
    ///
    /// Zero, the default, selects the current run attempt. The current point accepts only zero;
    /// [`Client::clone_fork_flow`] returns [`SdkError::InvalidArgument`] otherwise.
    pub fn source_run_attempt(mut self, run_attempt: u32) -> Self {
        self.source_run_attempt = run_attempt;
        self
    }

    /// Decides whether the call may start a run for a Flow ID that already has a run.
    ///
    /// It works as [`crate::StartFlowOptions::id_reuse_policy`] does for a start.
    pub fn id_reuse_policy(mut self, policy: IdReusePolicy) -> Self {
        self.id_reuse_policy = policy;
        self
    }

    /// Returns the existing run instead of an error when the new Flow ID already has a run.
    ///
    /// It applies only when [`Self::request_id`] matches the request that created that run, so a
    /// retry with the same request ID returns [`CloneForkFlowResult`] for the run it created.
    pub fn ignore_already_started(mut self, value: bool) -> Self {
        self.ignore_already_started = value;
        self
    }

    /// Sets the request ID that identifies one logical clone fork.
    ///
    /// Without it, the Client generates a new UUID for every call. Set a stable ID, together
    /// with [`Self::ignore_already_started`], to retry a call whose outcome is unknown. A retry
    /// with the request ID of the new Flow's latest run copies nothing again. An empty request
    /// ID returns [`SdkError::InvalidArgument`].
    pub fn request_id(mut self, value: impl Into<String>) -> Self {
        self.request_id = Some(value.into());
        self
    }

    /// Changes the Flow configuration copied from the source run.
    ///
    /// Settings that `value` leaves unset keep the source run's values.
    pub fn config_override(mut self, value: FlowConfig) -> Self {
        self.config_override = Some(value);
        self
    }

    /// Runs the new Flow on a named task queue.
    ///
    /// Without it, the new Flow keeps the source run's task queue; `dex.default` selects the
    /// default task queue. Set a task queue when Workers pick their Flows by task queue, so that
    /// the source run's Workers do not run the new Flow's Steps. Names follow
    /// [`crate::StartFlowOptions::task_queue`].
    pub fn task_queue(mut self, value: impl Into<String>) -> Self {
        self.task_queue = value.into();
        self
    }

    /// Writes a declared Attribute after the copy, in the change that creates the new Flow.
    pub fn initial_attribute<T: Value>(mut self, attribute: &Attribute<T>, value: T) -> Self {
        self.attributes
            .push(InitialAttribute::for_attribute(attribute, value));
        self
    }

    /// Writes one Attribute-map instance after the copy, in the change that creates the new Flow.
    pub fn initial_attribute_map<T: Value>(
        mut self,
        attribute: &AttributeMap<T>,
        instance: &str,
        value: T,
    ) -> Self {
        self.attributes.push(InitialAttribute::for_attribute_map(
            attribute, instance, value,
        ));
        self
    }

    /// Sets the new Flow's Flow timeout, counted from the call, in whole seconds.
    ///
    /// Without it, or with zero, the new Flow keeps the source run's Flow timeout duration, also
    /// counted from the call. A source run without a Flow timeout gives a new Flow without one.
    /// The new Flow keeps the source run's timeout policy and handler options.
    pub fn timeout(mut self, value: Duration) -> Self {
        self.timeout = Some(value);
        self
    }

    /// Adds a reason that Dex records with the new Flow's start.
    pub fn reason(mut self, reason: impl Into<String>) -> Self {
        self.reason = Some(reason.into());
        self
    }

    /// Decides what happens to the SubFlow conditions that copied Steps wait on.
    ///
    /// Without this option, Dex uses [`SubFlowResetPolicy::AttachElseRestart`]. A child that
    /// ended attaches; a child that still runs takes the policy's second action.
    pub fn sub_flow_reset_policy(mut self, policy: SubFlowResetPolicy) -> Self {
        self.sub_flow_reset_policy = Some(policy);
        self
    }

    fn at(point: Option<TimeTravelPoint>) -> Self {
        Self {
            point,
            source_run_id: String::new(),
            source_run_attempt: 0,
            id_reuse_policy: IdReusePolicy::Default,
            ignore_already_started: false,
            request_id: None,
            config_override: None,
            task_queue: String::new(),
            attributes: Vec::new(),
            timeout: None,
            reason: None,
            sub_flow_reset_policy: None,
        }
    }
}

impl Default for CloneForkFlowOptions {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
#[non_exhaustive]
/// Describes the run that [`Client::clone_fork_flow`] created and the copied source point.
///
/// Since 1.45.2.
pub struct CloneForkFlowResult {
    /// Run ID of the new Flow's run.
    pub run_id: String,
    /// Run ID of the source run.
    pub source_run_id: String,
    /// Run attempt of the source run that holds the copied point.
    pub source_run_attempt: u32,
    /// ID of the last source history event that the copied point includes.
    pub source_event_id: i64,
}

impl Client {
    /// Creates a new Flow whose first run starts from a copy of another Flow run's state.
    ///
    /// The source run is the run of `source_flow_id` that [`CloneForkFlowOptions::source_run_id`]
    /// selects, or its latest run. `options` selects the copied point; the default copies the
    /// latest committed state. Dex copies the source run's Attributes, pending Channel messages,
    /// Step executions, waits, and timers at that point. The new Flow gets the Flow ID `flow_id`,
    /// a new run ID, and run attempt 1, and continues on its own from the copied state. The source
    /// run keeps running and never changes. A typical use is a session that branches into a new
    /// session while the old one continues.
    ///
    /// `flow` must be the registered Flow type of the source run, which the new Flow keeps. The
    /// Client uses it to encode [`CloneForkFlowOptions::initial_attribute`] values. `flow_id`
    /// must differ from `source_flow_id`; to return a Flow to an earlier point, use
    /// [`Self::time_travel`].
    ///
    /// Steps that were running at the point start again in the new Flow. Steps that waited keep
    /// waiting, and their timers keep their fire times. The new Flow copies no history, Stream
    /// messages, RPC and Channel publication request IDs, parent Flow, or sticky Worker. Input
    /// that the source run received after the point, such as RPCs and Channel publications, is
    /// not applied. [`CloneForkFlowOptions::sub_flow_reset_policy`] decides what happens to
    /// SubFlow conditions of copied Steps.
    ///
    /// ```no_run
    /// use dex_sdk::{Client, CloneForkFlowOptions, Flow, SdkResult};
    ///
    /// fn branch_session<F: Flow>(client: &Client, flow: &F, session_id: &str) -> SdkResult<String> {
    ///     let branch_id = format!("{session_id}-branch");
    ///     let result = client.clone_fork_flow(
    ///         flow,
    ///         session_id,
    ///         &branch_id,
    ///         CloneForkFlowOptions::new().reason("explore another answer"),
    ///     )?;
    ///     Ok(result.run_id)
    /// }
    /// ```
    ///
    /// The call requires Server protocol 6, which server-zd 0.1.33 and later provide.
    ///
    /// Since 1.45.2.
    ///
    /// # Errors
    ///
    /// Returns [`SdkError::InvalidArgument`] for empty or equal Flow IDs, an invalid point,
    /// request ID, task queue, or timeout, or an unregistered initial Attribute, all before the
    /// request is sent. Returns [`SdkError::FlowDefinition`] for an unregistered Flow type.
    /// Returns [`SdkError::Service`] before sending the request when the Server protocol is older
    /// than 6. Returns [`SdkError::FlowAlreadyStarted`] when the ID reuse policy rejects a run of
    /// `flow_id`, [`SdkError::FlowNotFound`] naming `source_flow_id` for a missing source run, and
    /// a service error with gRPC code `FailedPrecondition` when the point is outside the source
    /// run's history window or its state is not running.
    pub fn clone_fork_flow<SomeFlow: Flow>(
        &self,
        flow: &SomeFlow,
        source_flow_id: &str,
        flow_id: &str,
        options: CloneForkFlowOptions,
    ) -> SdkResult<CloneForkFlowResult> {
        let registered = self.registry.flow(flow.flow_type())?;
        if registered.type_id != TypeId::of::<SomeFlow>() {
            return Err(SdkError::FlowDefinition {
                message: format!(
                    "Flow {} does not match the registered Rust type",
                    flow.flow_type()
                ),
            });
        }
        let mut request = map_clone_fork_flow_request(source_flow_id, flow_id, &options)?;
        request.attributes = options
            .attributes
            .iter()
            .map(|attribute| {
                let definition = registered
                    .persistence
                    .get(&attribute.attribute_name)
                    .ok_or_else(|| {
                        invalid(format!(
                            "initial Attribute is not registered: {}",
                            attribute.key
                        ))
                    })?;
                attribute.encode_attribute_write(definition)
            })
            .collect::<SdkResult<Vec<_>>>()?;
        self.require_server_protocol("clone_fork_flow")?;
        self.negotiated_protocol_version
            .require_clone_fork_flow_support()
            .map_err(|error| SdkError::Service {
                service: ServiceError::local("clone_fork_flow", error),
            })?;
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            match service.clone_fork_flow(request).await {
                Ok(response) => map_clone_fork_flow_result(response.into_inner()),
                Err(status) => Err(translate_clone_fork_flow_error(
                    status,
                    source_flow_id,
                    flow_id,
                )),
            }
        })
    }
}

/// Returns the request for everything except the initial Attributes, or InvalidArgument.
pub(crate) fn map_clone_fork_flow_request(
    source_flow_id: &str,
    flow_id: &str,
    options: &CloneForkFlowOptions,
) -> SdkResult<CloneForkFlowRequest> {
    require_name(source_flow_id, "source Flow ID")?;
    require_name(flow_id, "Flow ID")?;
    if source_flow_id == flow_id {
        return Err(invalid(
            "clone_fork_flow Flow ID must differ from the source Flow ID; use time_travel to \
             return a Flow to an earlier point",
        ));
    }
    if options.request_id.as_deref() == Some("") {
        return Err(invalid("clone_fork_flow request ID must not be empty"));
    }
    let run_attempt = i32::try_from(options.source_run_attempt)
        .map_err(|_| invalid("source run attempt exceeds int32"))?;
    let (point_type, fields) = match &options.point {
        None => {
            if run_attempt != 0 {
                return Err(invalid(
                    "clone_fork_flow at the current point accepts only source run attempt zero",
                ));
            }
            (FlowCloneForkPointType::Current, Default::default())
        }
        Some(point) => {
            let point_type = match point {
                TimeTravelPoint::Beginning => FlowCloneForkPointType::Beginning,
                TimeTravelPoint::HistoryEventTime(_) => FlowCloneForkPointType::HistoryEventTime,
                TimeTravelPoint::StepType(_) => FlowCloneForkPointType::StepType,
                TimeTravelPoint::StepExecution(..) => FlowCloneForkPointType::StepExecutionId,
                TimeTravelPoint::EventId(_) => FlowCloneForkPointType::EventId,
            };
            (point_type, point.fields(rfc3339)?)
        }
    };
    task_queue_name::validate_flow_task_queue(&options.task_queue).map_err(invalid)?;
    Ok(CloneForkFlowRequest {
        source_flow_id: source_flow_id.to_string(),
        source_run_id: options.source_run_id.clone(),
        flow_id: flow_id.to_string(),
        request_id: options
            .request_id
            .clone()
            .unwrap_or_else(|| Uuid::new_v4().to_string()),
        id_reuse_policy: map_id_reuse_policy(options.id_reuse_policy),
        flow_already_started_options: options.ignore_already_started.then_some(
            FlowAlreadyStartedOptions {
                ignore_already_started_error: true,
            },
        ),
        flow_config_override: map_clone_fork_flow_config_override(
            options.config_override.as_ref(),
            &options.task_queue,
        )?,
        attributes: Vec::new(),
        flow_timeout_seconds: optional_seconds(options.timeout)?,
        reason: options.reason.clone().unwrap_or_default(),
        point_type: point_type as i32,
        history_event_time: fields.history_event_time,
        step_type: fields.step_type,
        step_execution_id: fields.step_execution_id,
        step_method: fields.step_method,
        event_id: fields.event_id,
        run_attempt,
        sub_flow_reset_policy: SubFlowResetPolicy::wire_value(options.sub_flow_reset_policy),
    })
}

/// Leaves the copied FlowConfig untouched when neither a configuration nor a task queue is set.
fn map_clone_fork_flow_config_override(
    config: Option<&FlowConfig>,
    task_queue: &str,
) -> SdkResult<Option<ProtoFlowConfig>> {
    match (config, task_queue.is_empty()) {
        (None, true) => Ok(None),
        (config, true) => map_flow_config(config).map(Some),
        (config, false) => map_pull_flow_config(config, task_queue).map(Some),
    }
}

pub(crate) fn map_clone_fork_flow_result(
    response: CloneForkFlowResponse,
) -> SdkResult<CloneForkFlowResult> {
    if response.run_id.is_empty() {
        return Err(SdkError::Service {
            service: ServiceError::local("clone_fork_flow", "response has no run ID"),
        });
    }
    let source_run_attempt =
        u32::try_from(response.source_run_attempt).map_err(|_| SdkError::Service {
            service: ServiceError::local(
                "clone_fork_flow",
                format!(
                    "response has a negative source run attempt {}",
                    response.source_run_attempt
                ),
            ),
        })?;
    Ok(CloneForkFlowResult {
        run_id: response.run_id,
        source_run_id: response.source_run_id,
        source_run_attempt,
        source_event_id: response.source_event_id,
    })
}

/// Attributes a missing Flow to the source Flow and every other error to the new Flow.
pub(crate) fn translate_clone_fork_flow_error(
    status: Status,
    source_flow_id: &str,
    flow_id: &str,
) -> SdkError {
    let translated = SdkError::from_status(
        status.clone(),
        "clone_fork_flow",
        Some(flow_id),
        FlowTargetRequirement::None,
    );
    match translated {
        SdkError::Service { service } if service.sub_status() == ErrorSubStatus::FlowNotExists => {
            SdkError::from_status(
                status,
                "clone_fork_flow",
                Some(source_flow_id),
                FlowTargetRequirement::Existing,
            )
        }
        other => other,
    }
}

#[cfg(test)]
#[path = "clone_fork_flow_tests.rs"]
mod tests;
