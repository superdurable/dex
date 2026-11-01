// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! AdminService responses and error statuses for each step down result.

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use async_trait::async_trait;
use dexzd_protocol::dex::admin_service_server::AdminService;
use dexzd_protocol::dex::{
    ErrorSubStatus, ServiceErrorResponse, StepDownActiveNodeRequest, StepDownActiveNodeResponse,
};
use prost::Message;
use tonic::{Code, Request, Status};

use super::{
    ActiveNodeStepDown, ActiveNodeStepDownError, ActiveNodeStepDownOutcome, ActiveNodeTakeover,
    DexAdminService,
};
use crate::flow_operation_error::GoogleRpcStatus;

/// Returns one scripted result and counts the step downs it was asked for.
pub(crate) struct ScriptedActiveNodeStepDown {
    result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
    step_down_count: AtomicUsize,
}

impl ScriptedActiveNodeStepDown {
    pub(crate) fn returning(
        result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
    ) -> Arc<Self> {
        Arc::new(Self {
            result,
            step_down_count: AtomicUsize::new(0),
        })
    }

    pub(crate) fn step_down_count(&self) -> usize {
        self.step_down_count.load(Ordering::SeqCst)
    }
}

#[async_trait]
impl ActiveNodeStepDown for ScriptedActiveNodeStepDown {
    async fn step_down_active_node(
        &self,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError> {
        self.step_down_count.fetch_add(1, Ordering::SeqCst);
        self.result.clone()
    }
}

pub(crate) fn outcome_with_takeover() -> ActiveNodeStepDownOutcome {
    ActiveNodeStepDownOutcome {
        node_id: "node-a".to_owned(),
        released_node_epoch: 7,
        observed_takeover: Some(ActiveNodeTakeover {
            node_epoch: 8,
            holder_process_id: "standby-process".to_owned(),
        }),
        lease_reacquisition_hold_off: Duration::from_millis(5_500),
    }
}

async fn step_down(
    result: Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>,
) -> (Result<StepDownActiveNodeResponse, Status>, usize) {
    let step_down = ScriptedActiveNodeStepDown::returning(result);
    let service = DexAdminService::new(Arc::clone(&step_down) as Arc<dyn ActiveNodeStepDown>);
    let response = service
        .step_down_active_node(Request::new(StepDownActiveNodeRequest {}))
        .await
        .map(tonic::Response::into_inner);
    (response, step_down.step_down_count())
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
async fn a_completed_step_down_reports_the_released_epoch_and_the_observed_takeover() {
    let (response, step_down_count) = step_down(Ok(outcome_with_takeover())).await;
    assert_eq!(step_down_count, 1);
    let response = response.expect("the step down succeeds");
    assert_eq!(response.node_id, "node-a");
    assert_eq!(response.released_node_epoch, 7);
    let takeover = response
        .observed_takeover
        .expect("the takeover is reported");
    assert_eq!(takeover.node_epoch, 8);
    assert_eq!(takeover.holder_process_id, "standby-process");
    assert_eq!(
        response.lease_reacquisition_hold_off,
        Some(prost_types::Duration {
            seconds: 5,
            nanos: 500_000_000,
        })
    );
}

#[tokio::test]
async fn a_step_down_without_a_takeover_leaves_the_takeover_unset() {
    let (response, _) = step_down(Ok(ActiveNodeStepDownOutcome {
        observed_takeover: None,
        ..outcome_with_takeover()
    }))
    .await;
    let response = response.expect("the step down succeeds");
    assert_eq!(response.released_node_epoch, 7);
    assert_eq!(response.observed_takeover, None);
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
            ActiveNodeStepDownError::AlreadySteppingDown,
            Code::FailedPrecondition,
            "a step down of this server process is already in progress",
        ),
        (
            ActiveNodeStepDownError::LeaseReleaseUnconfirmed {
                detail: "object storage returned 503".to_owned(),
            },
            Code::Internal,
            "the node lease release was not confirmed: object storage returned 503; the node lease stays held until it expires",
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
        let (response, step_down_count) = step_down(Err(error)).await;
        assert_eq!(step_down_count, 1);
        let status = response.expect_err("the step down fails");
        assert_refused(&status, code, message);
    }
}
