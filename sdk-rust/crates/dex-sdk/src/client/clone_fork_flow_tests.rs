// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::{Duration, SystemTime};

use dex_protocol::dex::{
    CloneForkFlowResponse, ErrorSubStatus as ProtoErrorSubStatus, FlowCloneForkPointType,
    FlowResetStepMethod, FlowResetType, IdReusePolicy as ProtoIdReusePolicy, ServiceErrorResponse,
    SubFlowResetPolicy as ProtoSubFlowResetPolicy,
};
use prost::Message;
use tonic::{Code, Status};

use super::{
    CloneForkFlowOptions, CloneForkFlowResult, map_clone_fork_flow_request,
    map_clone_fork_flow_result, translate_clone_fork_flow_error,
};
use crate::client::map_reset_flow_request;
use crate::{
    Context, FlowConfig, HandlerResult, IdReusePolicy, SdkError, Step, StepDecision,
    StepExecutionId, SubFlowResetPolicy, TimeTravelOptions, TimeTravelStepMethod,
};

struct AnswerQuestion;

impl Step for AnswerQuestion {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

#[test]
fn defaults_copy_the_current_point_with_a_generated_request_id() {
    let request = map_clone_fork_flow_request("source-1", "clone-1", &CloneForkFlowOptions::new())
        .expect("map default clone fork");
    assert_eq!(request.source_flow_id, "source-1");
    assert_eq!(request.flow_id, "clone-1");
    assert!(request.source_run_id.is_empty());
    uuid::Uuid::parse_str(&request.request_id).expect("generated UUID request ID");
    assert_eq!(request.point_type, FlowCloneForkPointType::Current as i32);
    assert_eq!(request.step_method, FlowResetStepMethod::Unspecified as i32);
    assert_eq!(
        request.sub_flow_reset_policy,
        ProtoSubFlowResetPolicy::Unspecified as i32
    );
    assert_eq!(
        request.id_reuse_policy,
        ProtoIdReusePolicy::Unspecified as i32
    );
    assert_eq!(request.flow_config_override, None);
    assert_eq!(request.flow_already_started_options, None);
    assert_eq!(request.flow_timeout_seconds, 0);
    assert_eq!(request.run_attempt, 0);
    assert_eq!(request.event_id, 0);
    assert!(request.reason.is_empty());

    let second = map_clone_fork_flow_request("source-1", "clone-1", &CloneForkFlowOptions::new())
        .expect("map second clone fork");
    assert_ne!(request.request_id, second.request_id);
}

#[test]
fn maps_every_option() {
    let options = CloneForkFlowOptions::from_event_id(42)
        .source_run_id("source-run-9")
        .source_run_attempt(3)
        .id_reuse_policy(IdReusePolicy::TerminateIfRunning)
        .ignore_already_started(true)
        .request_id("branch-request")
        .config_override(FlowConfig::new().history_window_events(64))
        .task_queue("tenant-b")
        .timeout(Duration::from_secs(90))
        .reason("branch the session")
        .sub_flow_reset_policy(SubFlowResetPolicy::AttachElseFail);
    let request = map_clone_fork_flow_request("source-1", "clone-1", &options)
        .expect("map every clone fork option");
    assert_eq!(request.source_run_id, "source-run-9");
    assert_eq!(request.run_attempt, 3);
    assert_eq!(request.point_type, FlowCloneForkPointType::EventId as i32);
    assert_eq!(request.event_id, 42);
    assert_eq!(
        request.id_reuse_policy,
        ProtoIdReusePolicy::AllowTerminateIfRunning as i32
    );
    assert!(
        request
            .flow_already_started_options
            .expect("already-started options")
            .ignore_already_started_error
    );
    assert_eq!(request.request_id, "branch-request");
    let config = request.flow_config_override.expect("config override");
    assert_eq!(config.history_window_events, Some(64));
    assert_eq!(config.step_durability, None);
    let target = config.worker_target.expect("Worker target");
    assert!(target.is_pull_mode);
    assert_eq!(target.task_queue, "tenant-b");
    assert_eq!(request.flow_timeout_seconds, 90);
    assert_eq!(request.reason, "branch the session");
    assert_eq!(
        request.sub_flow_reset_policy,
        ProtoSubFlowResetPolicy::AttachElseFail as i32
    );
}

#[test]
fn config_override_without_task_queue_keeps_the_source_worker_target() {
    let request = map_clone_fork_flow_request(
        "source-1",
        "clone-1",
        &CloneForkFlowOptions::new().config_override(FlowConfig::new().offload_history(true)),
    )
    .expect("map config override");
    let config = request.flow_config_override.expect("config override");
    assert_eq!(config.offload_history, Some(true));
    assert_eq!(config.worker_target, None);
}

#[test]
fn maps_every_point_type() {
    let event_time = SystemTime::UNIX_EPOCH + Duration::from_secs(1_763_202_600);
    let cases = [
        (
            CloneForkFlowOptions::from_beginning().source_run_attempt(1),
            FlowCloneForkPointType::Beginning,
        ),
        (
            CloneForkFlowOptions::from_history_event_time(event_time),
            FlowCloneForkPointType::HistoryEventTime,
        ),
        (
            CloneForkFlowOptions::from_step(&AnswerQuestion),
            FlowCloneForkPointType::StepType,
        ),
        (
            CloneForkFlowOptions::from_step_execution(
                StepExecutionId::of(&AnswerQuestion).execution_number(2),
                TimeTravelStepMethod::WaitFor,
            ),
            FlowCloneForkPointType::StepExecutionId,
        ),
        (
            CloneForkFlowOptions::from_event_id(7),
            FlowCloneForkPointType::EventId,
        ),
    ];
    for (options, point_type) in cases {
        let request = map_clone_fork_flow_request("source-1", "clone-1", &options)
            .expect("map clone fork point");
        assert_eq!(request.point_type, point_type as i32);
        match point_type {
            FlowCloneForkPointType::Beginning => assert_eq!(request.run_attempt, 1),
            FlowCloneForkPointType::HistoryEventTime => {
                assert_eq!(request.history_event_time, "2025-11-15T10:30:00Z");
            }
            FlowCloneForkPointType::StepType => assert_eq!(request.step_type, "AnswerQuestion"),
            FlowCloneForkPointType::StepExecutionId => {
                assert_eq!(request.step_execution_id, "AnswerQuestion-2");
                assert_eq!(request.step_method, FlowResetStepMethod::WaitFor as i32);
            }
            _ => assert_eq!(request.event_id, 7),
        }
        if point_type != FlowCloneForkPointType::StepExecutionId {
            assert_eq!(request.step_method, FlowResetStepMethod::Unspecified as i32);
        }
    }
}

#[test]
fn rejects_invalid_requests_before_sending() {
    let cases = [
        ("", "clone-1", CloneForkFlowOptions::new(), "source Flow ID"),
        ("source-1", "", CloneForkFlowOptions::new(), "Flow ID"),
        ("same", "same", CloneForkFlowOptions::new(), "must differ"),
        (
            "source-1",
            "clone-1",
            CloneForkFlowOptions::new().source_run_attempt(1),
            "only source run attempt zero",
        ),
        (
            "source-1",
            "clone-1",
            CloneForkFlowOptions::from_event_id(0),
            "must be positive",
        ),
        (
            "source-1",
            "clone-1",
            CloneForkFlowOptions::new().request_id(""),
            "request ID must not be empty",
        ),
        (
            "source-1",
            "clone-1",
            CloneForkFlowOptions::new().task_queue("dex.other"),
            "reserved",
        ),
        (
            "source-1",
            "clone-1",
            CloneForkFlowOptions::new().timeout(Duration::from_millis(1_500)),
            "whole seconds",
        ),
    ];
    for (source_flow_id, flow_id, options, expected) in cases {
        let error = map_clone_fork_flow_request(source_flow_id, flow_id, &options)
            .expect_err("invalid clone fork request");
        assert!(
            matches!(&error, SdkError::InvalidArgument { message } if message.contains(expected)),
            "{error:?} does not mention {expected}"
        );
    }
}

#[test]
fn maps_the_response() {
    let result = map_clone_fork_flow_result(CloneForkFlowResponse {
        run_id: "clone-run-1".to_string(),
        source_run_id: "source-run-1".to_string(),
        source_run_attempt: 2,
        source_event_id: 17,
    })
    .expect("map response");
    assert_eq!(
        result,
        CloneForkFlowResult {
            run_id: "clone-run-1".to_string(),
            source_run_id: "source-run-1".to_string(),
            source_run_attempt: 2,
            source_event_id: 17,
        }
    );
    assert!(map_clone_fork_flow_result(CloneForkFlowResponse::default()).is_err());
}

#[test]
fn translates_missing_source_and_existing_target() {
    let not_found = translate_clone_fork_flow_error(
        dex_status(Code::NotFound, ProtoErrorSubStatus::FlowNotExists),
        "source-1",
        "clone-1",
    );
    match not_found {
        SdkError::FlowNotFound { service } => assert_eq!(service.flow_id(), Some("source-1")),
        other => panic!("expected FlowNotFound, got {other:?}"),
    }
    let already_started = translate_clone_fork_flow_error(
        dex_status(Code::AlreadyExists, ProtoErrorSubStatus::FlowAlreadyStarted),
        "source-1",
        "clone-1",
    );
    match already_started {
        SdkError::FlowAlreadyStarted { service } => assert_eq!(service.flow_id(), Some("clone-1")),
        other => panic!("expected FlowAlreadyStarted, got {other:?}"),
    }
}

#[test]
fn time_travel_maps_event_id_and_sub_flow_reset_policy() {
    let request = map_reset_flow_request(
        "flow-1",
        TimeTravelOptions::from_event_id(12)
            .sub_flow_reset_policy(SubFlowResetPolicy::AlwaysReject),
    )
    .expect("map event ID time travel");
    assert_eq!(request.reset_type, FlowResetType::EventId as i32);
    assert_eq!(request.event_id, 12);
    assert_eq!(
        request.sub_flow_reset_policy,
        ProtoSubFlowResetPolicy::AlwaysReject as i32
    );
    assert_eq!(request.step_method, FlowResetStepMethod::Unspecified as i32);

    let beginning = map_reset_flow_request("flow-1", TimeTravelOptions::from_beginning())
        .expect("map beginning time travel");
    assert_eq!(beginning.reset_type, FlowResetType::Beginning as i32);
    assert_eq!(
        beginning.sub_flow_reset_policy,
        ProtoSubFlowResetPolicy::Unspecified as i32
    );
    assert_eq!(beginning.event_id, 0);

    assert!(matches!(
        map_reset_flow_request("flow-1", TimeTravelOptions::from_event_id(-1)),
        Err(SdkError::InvalidArgument { .. })
    ));
}

#[test]
fn sub_flow_reset_policies_map_to_wire_values() {
    let cases = [
        (None, ProtoSubFlowResetPolicy::Unspecified),
        (
            Some(SubFlowResetPolicy::AlwaysRestart),
            ProtoSubFlowResetPolicy::AlwaysRestart,
        ),
        (
            Some(SubFlowResetPolicy::AlwaysFail),
            ProtoSubFlowResetPolicy::AlwaysFail,
        ),
        (
            Some(SubFlowResetPolicy::AlwaysReject),
            ProtoSubFlowResetPolicy::AlwaysReject,
        ),
        (
            Some(SubFlowResetPolicy::AttachElseRestart),
            ProtoSubFlowResetPolicy::AttachElseRestart,
        ),
        (
            Some(SubFlowResetPolicy::AttachElseFail),
            ProtoSubFlowResetPolicy::AttachElseFail,
        ),
        (
            Some(SubFlowResetPolicy::AttachElseReject),
            ProtoSubFlowResetPolicy::AttachElseReject,
        ),
    ];
    for (policy, expected) in cases {
        assert_eq!(SubFlowResetPolicy::wire_value(policy), expected as i32);
    }
}

#[derive(Clone, PartialEq, Message)]
struct GoogleRpcStatus {
    #[prost(int32, tag = "1")]
    code: i32,
    #[prost(string, tag = "2")]
    message: String,
    #[prost(message, repeated, tag = "3")]
    details: Vec<prost_types::Any>,
}

fn dex_status(code: Code, sub_status: ProtoErrorSubStatus) -> Status {
    let response = ServiceErrorResponse {
        detail: "service detail".to_string(),
        sub_status: sub_status as i32,
        ..ServiceErrorResponse::default()
    };
    let status = GoogleRpcStatus {
        code: code as i32,
        message: "gRPC detail".to_string(),
        details: vec![prost_types::Any {
            type_url: "type.googleapis.com/dex.ServiceErrorResponse".to_string(),
            value: response.encode_to_vec(),
        }],
    };
    Status::with_details(code, "gRPC detail", status.encode_to_vec().into())
}
