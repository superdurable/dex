// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! AdminService responses and error statuses for each describe and step down result.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use dexzd_protocol::dex::admin_service_server::AdminService;
use dexzd_protocol::dex::step_down_active_node_response::LeaseHolderAfterStepDown as LeaseHolderMessage;
use dexzd_protocol::dex::{
    DescribeActiveNodeRequest, ErrorSubStatus, NodeLeaseHolder as NodeLeaseHolderMessage,
    NodeLeaseHolderUnknown, ServiceErrorResponse, StepDownActiveNodeRequest,
    StepDownActiveNodeResponse,
};
use prost::Message;
use tokio::sync::Notify;
use tonic::{Code, Request, Status};

use super::{
    ActiveNodeAdministration, ActiveNodeDescription, ActiveNodeDescriptionError,
    ActiveNodeStepDownError, ActiveNodeStepDownOutcome, DexAdminService, LeaseHolderAfterStepDown,
    NodeLeaseHolder, StepDownPrecondition, saturating_protobuf_duration,
};
use crate::flow_operation_error::GoogleRpcStatus;

/// Returns scripted results and records the step downs it was asked for.
pub(crate) struct ScriptedActiveNodeAdministration {
    description: Result<ActiveNodeDescription, ActiveNodeDescriptionError>,
    step_down_result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
    describe_count: AtomicUsize,
    preconditions: Mutex<Vec<StepDownPrecondition>>,
}

impl ScriptedActiveNodeAdministration {
    pub(crate) fn returning(
        step_down_result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
    ) -> Arc<Self> {
        Self::describing(Ok(active_node_description()), step_down_result)
    }

    fn describing(
        description: Result<ActiveNodeDescription, ActiveNodeDescriptionError>,
        step_down_result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
    ) -> Arc<Self> {
        Arc::new(Self {
            description,
            step_down_result,
            describe_count: AtomicUsize::new(0),
            preconditions: Mutex::new(Vec::new()),
        })
    }

    pub(crate) fn describe_count(&self) -> usize {
        self.describe_count.load(Ordering::SeqCst)
    }

    pub(crate) fn step_down_count(&self) -> usize {
        self.preconditions().len()
    }

    /// Returns the precondition of every step down, in call order.
    pub(crate) fn preconditions(&self) -> Vec<StepDownPrecondition> {
        self.preconditions
            .lock()
            .expect("the precondition lock")
            .clone()
    }
}

#[async_trait]
impl ActiveNodeAdministration for ScriptedActiveNodeAdministration {
    async fn describe_active_node(
        &self,
    ) -> Result<ActiveNodeDescription, ActiveNodeDescriptionError> {
        self.describe_count.fetch_add(1, Ordering::SeqCst);
        self.description.clone()
    }

    async fn step_down_active_node(
        &self,
        precondition: StepDownPrecondition,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError> {
        self.preconditions
            .lock()
            .expect("the precondition lock")
            .push(precondition);
        self.step_down_result.clone()
    }
}

/// Signals when a step down starts and finishes it only after the test releases it.
struct BlockingActiveNodeAdministration {
    started: Notify,
    release: Notify,
    finished: Notify,
}

#[async_trait]
impl ActiveNodeAdministration for BlockingActiveNodeAdministration {
    async fn describe_active_node(
        &self,
    ) -> Result<ActiveNodeDescription, ActiveNodeDescriptionError> {
        Ok(active_node_description())
    }

    async fn step_down_active_node(
        &self,
        _precondition: StepDownPrecondition,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError> {
        self.started.notify_one();
        self.release.notified().await;
        self.finished.notify_one();
        Ok(outcome_with_takeover())
    }
}

/// Panics in every step down, as a bug in the serve loop would.
struct PanickingActiveNodeAdministration;

#[async_trait]
impl ActiveNodeAdministration for PanickingActiveNodeAdministration {
    async fn describe_active_node(
        &self,
    ) -> Result<ActiveNodeDescription, ActiveNodeDescriptionError> {
        Ok(active_node_description())
    }

    async fn step_down_active_node(
        &self,
        _precondition: StepDownPrecondition,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError> {
        panic!("a scripted step down panic");
    }
}

pub(crate) fn active_node_description() -> ActiveNodeDescription {
    ActiveNodeDescription {
        node_id: "node-a".to_owned(),
        node_epoch: 7,
        holder_process_id: "active-process".to_owned(),
    }
}

pub(crate) fn outcome_with_takeover() -> ActiveNodeStepDownOutcome {
    ActiveNodeStepDownOutcome {
        node_id: "node-a".to_owned(),
        released_node_epoch: 7,
        lease_reacquisition_hold_off: Duration::from_millis(5_500),
        lease_holder_after_step_down: LeaseHolderAfterStepDown::TakenOverByAnotherProcess(
            NodeLeaseHolder {
                node_epoch: 8,
                holder_process_id: "standby-process".to_owned(),
            },
        ),
    }
}

fn expecting(
    expected_node_id: Option<&str>,
    expected_node_epoch: Option<u64>,
) -> StepDownPrecondition {
    StepDownPrecondition {
        expected_node_id: expected_node_id.map(str::to_owned),
        expected_node_epoch,
    }
}

async fn step_down(
    request: StepDownActiveNodeRequest,
    result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
) -> (
    Result<StepDownActiveNodeResponse, Status>,
    Vec<StepDownPrecondition>,
) {
    let administration = ScriptedActiveNodeAdministration::returning(result);
    let service =
        DexAdminService::new(Arc::clone(&administration) as Arc<dyn ActiveNodeAdministration>);
    let response = service
        .step_down_active_node(Request::new(request))
        .await
        .map(tonic::Response::into_inner);
    (response, administration.preconditions())
}

fn unconditional() -> StepDownActiveNodeRequest {
    StepDownActiveNodeRequest {
        expected_node_epoch: None,
        expected_node_id: None,
    }
}

fn conditional(expected_node_id: &str, expected_node_epoch: u64) -> StepDownActiveNodeRequest {
    StepDownActiveNodeRequest {
        expected_node_epoch: Some(expected_node_epoch),
        expected_node_id: Some(expected_node_id.to_owned()),
    }
}

fn assert_refused(status: &Status, code: Code, message: &str) {
    assert_eq!(status.code(), code);
    assert_eq!(status.message(), message);
    let details =
        GoogleRpcStatus::decode(status.details()).expect("the details are a google.rpc.Status");
    assert_eq!(details.code, code as i32);
    let response = ServiceErrorResponse::decode(details.details[0].value.as_slice())
        .expect("the detail is a ServiceErrorResponse");
    assert_eq!(response.sub_status, ErrorSubStatus::Uncategorized as i32);
    assert_eq!(response.detail, message);
}

#[tokio::test]
async fn describe_reports_the_lease_of_the_active_process() {
    let administration = ScriptedActiveNodeAdministration::returning(Ok(outcome_with_takeover()));
    let service =
        DexAdminService::new(Arc::clone(&administration) as Arc<dyn ActiveNodeAdministration>);
    let response = service
        .describe_active_node(Request::new(DescribeActiveNodeRequest {}))
        .await
        .expect("the active process describes itself")
        .into_inner();
    assert_eq!(response.node_id, "node-a");
    assert_eq!(response.node_epoch, 7);
    assert_eq!(response.holder_process_id, "active-process");
    assert_eq!(administration.describe_count(), 1);
    assert_eq!(administration.step_down_count(), 0);
}

#[tokio::test]
async fn describe_on_a_process_that_is_not_active_fails_the_precondition() {
    let administration = ScriptedActiveNodeAdministration::describing(
        Err(ActiveNodeDescriptionError::NotActive {
            detail: "the process is a standby".to_owned(),
        }),
        Ok(outcome_with_takeover()),
    );
    let service = DexAdminService::new(administration as Arc<dyn ActiveNodeAdministration>);
    let status = service
        .describe_active_node(Request::new(DescribeActiveNodeRequest {}))
        .await
        .expect_err("a standby does not describe itself as active");
    assert_refused(
        &status,
        Code::FailedPrecondition,
        "this server process is not the active process of its node ID: the process is a standby",
    );
}

#[tokio::test]
async fn each_lease_holder_after_a_step_down_maps_to_its_oneof_case() {
    let cases = [
        (
            LeaseHolderAfterStepDown::TakenOverByAnotherProcess(NodeLeaseHolder {
                node_epoch: 8,
                holder_process_id: "standby-process".to_owned(),
            }),
            LeaseHolderMessage::TakenOverByAnotherProcess(NodeLeaseHolderMessage {
                node_epoch: 8,
                holder_process_id: "standby-process".to_owned(),
            }),
        ),
        (
            LeaseHolderAfterStepDown::TakenBackByThisProcess(NodeLeaseHolder {
                node_epoch: 8,
                holder_process_id: "active-process".to_owned(),
            }),
            LeaseHolderMessage::TakenBackByThisProcess(NodeLeaseHolderMessage {
                node_epoch: 8,
                holder_process_id: "active-process".to_owned(),
            }),
        ),
        (
            LeaseHolderAfterStepDown::Unknown {
                detail: "reading the node lease failed".to_owned(),
            },
            LeaseHolderMessage::LeaseHolderUnknown(NodeLeaseHolderUnknown {
                detail: "reading the node lease failed".to_owned(),
            }),
        ),
    ];
    for (lease_holder, expected_message) in cases {
        let (response, _) = step_down(
            unconditional(),
            Ok(ActiveNodeStepDownOutcome {
                lease_holder_after_step_down: lease_holder,
                ..outcome_with_takeover()
            }),
        )
        .await;
        let response = response.expect("the step down succeeds");
        assert_eq!(response.node_id, "node-a");
        assert_eq!(response.released_node_epoch, 7);
        assert_eq!(
            response.lease_reacquisition_hold_off,
            Some(prost_types::Duration {
                seconds: 5,
                nanos: 500_000_000,
            })
        );
        assert_eq!(
            response.lease_holder_after_step_down,
            Some(expected_message)
        );
    }
}

#[tokio::test]
async fn the_precondition_reaches_the_step_down() {
    let cases = [
        (conditional("node-a", 7), expecting(Some("node-a"), Some(7))),
        (
            StepDownActiveNodeRequest {
                expected_node_epoch: Some(0),
                expected_node_id: None,
            },
            expecting(None, Some(0)),
        ),
        (unconditional(), StepDownPrecondition::default()),
    ];
    for (request, expected_precondition) in cases {
        let (response, preconditions) = step_down(request, Ok(outcome_with_takeover())).await;
        response.expect("the step down succeeds");
        assert_eq!(preconditions, vec![expected_precondition]);
    }
}

#[tokio::test]
async fn an_empty_expected_node_id_is_an_invalid_argument() {
    let (response, preconditions) = step_down(
        StepDownActiveNodeRequest {
            expected_node_epoch: Some(7),
            expected_node_id: Some(String::new()),
        },
        Ok(outcome_with_takeover()),
    )
    .await;
    assert_refused(
        &response.expect_err("an empty node ID is refused"),
        Code::InvalidArgument,
        "expected_node_id must not be empty when set",
    );
    assert!(preconditions.is_empty());
}

#[test]
fn a_precondition_matches_only_the_node_lease_it_names() {
    assert!(StepDownPrecondition::default().is_satisfied_by("node-a", 7));
    assert!(expecting(Some("node-a"), Some(7)).is_satisfied_by("node-a", 7));
    assert!(expecting(None, Some(7)).is_satisfied_by("node-b", 7));
    assert!(expecting(Some("node-a"), None).is_satisfied_by("node-a", 9));
    assert!(!expecting(Some("node-a"), Some(7)).is_satisfied_by("node-a", 8));
    assert!(!expecting(Some("node-a"), Some(7)).is_satisfied_by("node-b", 7));
    assert!(!expecting(Some("node-a"), None).is_satisfied_by("node-b", 7));
}

#[tokio::test]
async fn step_down_failures_map_to_codes_that_do_not_invite_a_retry() {
    let cases = [
        (
            ActiveNodeStepDownError::NotActive {
                detail: "the process is a standby".to_owned(),
            },
            Code::FailedPrecondition,
            "this server process is not the active process of its node ID: the process is a standby",
        ),
        (
            ActiveNodeStepDownError::PreconditionMismatch {
                expected: expecting(Some("node-a"), Some(7)),
                active_node_id: "node-a".to_owned(),
                active_node_epoch: 8,
            },
            Code::FailedPrecondition,
            "the active server process serves node ID \"node-a\" at node epoch 8, not the expected node ID \"node-a\" at node epoch 7",
        ),
        (
            ActiveNodeStepDownError::PreconditionMismatch {
                expected: expecting(Some("node-b"), None),
                active_node_id: "node-a".to_owned(),
                active_node_epoch: 7,
            },
            Code::FailedPrecondition,
            "the active server process serves node ID \"node-a\" at node epoch 7, not the expected node ID \"node-b\"",
        ),
        (
            ActiveNodeStepDownError::AlreadySteppingDown,
            Code::FailedPrecondition,
            "a step down of this server process is already in progress",
        ),
        (
            ActiveNodeStepDownError::LeaseReleaseUnconfirmed {
                detail: "object storage returned 503".to_owned(),
            },
            Code::Internal,
            "the node lease release was not confirmed: object storage returned 503; the node lease may stay held until it expires, and this process stopped renewing it",
        ),
        (
            ActiveNodeStepDownError::Internal {
                detail: "the serve loop stopped".to_owned(),
            },
            Code::Internal,
            "the step down failed: the serve loop stopped",
        ),
    ];
    for (error, code, message) in cases {
        let (response, preconditions) = step_down(conditional("node-a", 7), Err(error)).await;
        assert_eq!(preconditions, vec![expecting(Some("node-a"), Some(7))]);
        let status = response.expect_err("the step down fails");
        assert_refused(&status, code, message);
    }
}

#[tokio::test]
async fn a_dropped_call_does_not_interrupt_a_started_step_down() {
    let administration = Arc::new(BlockingActiveNodeAdministration {
        started: Notify::new(),
        release: Notify::new(),
        finished: Notify::new(),
    });
    let service =
        DexAdminService::new(Arc::clone(&administration) as Arc<dyn ActiveNodeAdministration>);
    let call = service.step_down_active_node(Request::new(unconditional()));
    tokio::select! {
        _ = call => panic!("the step down must still be blocked"),
        () = administration.started.notified() => {}
    }
    administration.release.notify_one();
    // Fails instead of hanging when dropping the call cancelled the step down.
    tokio::time::timeout(Duration::from_secs(30), administration.finished.notified())
        .await
        .expect("the step down finishes after its call was dropped");
}

#[tokio::test]
async fn a_panicked_step_down_reports_an_unknown_process_state() {
    let service = DexAdminService::new(Arc::new(PanickingActiveNodeAdministration));
    let status = service
        .step_down_active_node(Request::new(conditional("node-a", 7)))
        .await
        .expect_err("a panicked step down fails");
    assert_eq!(status.code(), Code::Internal);
    assert_eq!(
        status.message(),
        "the step down panicked; the state of this server process is unknown"
    );
}

#[test]
fn hold_offs_beyond_the_protobuf_range_saturate_instead_of_failing() {
    assert_eq!(
        saturating_protobuf_duration(Duration::MAX),
        prost_types::Duration {
            seconds: 315_576_000_000,
            nanos: 0,
        }
    );
    assert_eq!(
        saturating_protobuf_duration(Duration::from_millis(1_500)),
        prost_types::Duration {
            seconds: 1,
            nanos: 500_000_000,
        }
    );
}
