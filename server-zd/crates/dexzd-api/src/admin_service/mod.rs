// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The operator-facing `dex.AdminService`.

mod active_node_administration;

#[cfg(test)]
pub(crate) mod dex_admin_service_tests;

pub use active_node_administration::{
    ActiveNodeAdministration, ActiveNodeDescription, ActiveNodeDescriptionError,
    ActiveNodeStepDownError, ActiveNodeStepDownOutcome, LeaseHolderAfterStepDown, NodeLeaseHolder,
    StepDownPrecondition,
};

use std::sync::Arc;
use std::time::Duration;

use dexzd_protocol::dex::admin_service_server::{AdminService, AdminServiceServer};
use dexzd_protocol::dex::step_down_active_node_response::LeaseHolderAfterStepDown as LeaseHolderMessage;
use dexzd_protocol::dex::{
    DescribeActiveNodeRequest, DescribeActiveNodeResponse,
    NodeLeaseHolder as NodeLeaseHolderMessage, NodeLeaseHolderUnknown, StepDownActiveNodeRequest,
    StepDownActiveNodeResponse,
};
use tokio::task::JoinError;
use tonic::{Request, Response, Status};

use crate::FlowOperationError;
use crate::authentication::{AdminCallerLogFields, AuthenticatedService, PublicApiAuthenticator};

/// Largest duration that `google.protobuf.Duration` allows, about 10,000 years.
const MAX_PROTOBUF_DURATION_SECONDS: u64 = 315_576_000_000;

/// The `dex.AdminService` of server-zd, served only behind admin authentication.
pub struct DexAdminService {
    active_node_administration: Arc<dyn ActiveNodeAdministration>,
}

impl DexAdminService {
    /// Returns AdminService wrapped in admin authentication, ready to add to a tonic server.
    ///
    /// With an `authenticator`, a call needs a JWT from a `jwt` authenticator whose
    /// `admin_audiences` names one of the token's audiences. Static tokens never qualify. With
    /// `None`, as on a development server, every call passes unchecked, so anyone who can reach the
    /// listener can step the node down.
    pub fn authenticated_server(
        active_node_administration: Arc<dyn ActiveNodeAdministration>,
        authenticator: Option<Arc<PublicApiAuthenticator>>,
    ) -> AuthenticatedService<AdminServiceServer<Self>> {
        AuthenticatedService::for_admin_service(
            Self::new(active_node_administration).into_server(),
            authenticator,
        )
    }

    pub(crate) fn new(active_node_administration: Arc<dyn ActiveNodeAdministration>) -> Self {
        Self {
            active_node_administration,
        }
    }

    pub(crate) fn into_server(self) -> AdminServiceServer<Self> {
        AdminServiceServer::new(self)
    }
}

#[tonic::async_trait]
impl AdminService for DexAdminService {
    async fn describe_active_node(
        &self,
        request: Request<DescribeActiveNodeRequest>,
    ) -> Result<Response<DescribeActiveNodeResponse>, Status> {
        let caller = AdminCallerLogFields::of_request(&request);
        match self.active_node_administration.describe_active_node().await {
            Ok(description) => {
                tracing::info!(
                    is_caller_authenticated = caller.is_authenticated(),
                    caller_issuer = caller.issuer(),
                    caller_subject = caller.subject(),
                    node_id = %description.node_id,
                    node_epoch = description.node_epoch,
                    "described the active server process"
                );
                Ok(Response::new(DescribeActiveNodeResponse {
                    node_id: description.node_id,
                    node_epoch: description.node_epoch,
                    holder_process_id: description.holder_process_id,
                }))
            }
            Err(error) => {
                tracing::info!(
                    is_caller_authenticated = caller.is_authenticated(),
                    caller_issuer = caller.issuer(),
                    caller_subject = caller.subject(),
                    %error,
                    "did not describe the active server process"
                );
                Err(describe_error(error).into_status())
            }
        }
    }

    async fn step_down_active_node(
        &self,
        request: Request<StepDownActiveNodeRequest>,
    ) -> Result<Response<StepDownActiveNodeResponse>, Status> {
        let caller = AdminCallerLogFields::of_request(&request);
        let precondition = step_down_precondition(request.into_inner())
            .map_err(FlowOperationError::into_status)?;
        tracing::info!(
            is_caller_authenticated = caller.is_authenticated(),
            caller_issuer = caller.issuer(),
            caller_subject = caller.subject(),
            expected_node_id = ?precondition.expected_node_id,
            expected_node_epoch = ?precondition.expected_node_epoch,
            "received a request to step down the active server process"
        );
        let active_node_administration = Arc::clone(&self.active_node_administration);
        // A dropped call detaches this task, which still collects and logs the serve loop's result.
        let step_down = tokio::spawn(async move {
            active_node_administration
                .step_down_active_node(precondition)
                .await
        });
        match step_down.await {
            Ok(Ok(outcome)) => {
                tracing::info!(
                    is_caller_authenticated = caller.is_authenticated(),
                    caller_issuer = caller.issuer(),
                    caller_subject = caller.subject(),
                    node_id = %outcome.node_id,
                    released_node_epoch = outcome.released_node_epoch,
                    lease_holder_after_step_down = ?outcome.lease_holder_after_step_down,
                    "the active server process stepped down"
                );
                Ok(Response::new(step_down_response(outcome)))
            }
            Ok(Err(error)) => {
                tracing::warn!(
                    is_caller_authenticated = caller.is_authenticated(),
                    caller_issuer = caller.issuer(),
                    caller_subject = caller.subject(),
                    %error,
                    "the active server process did not step down"
                );
                Err(step_down_error(error).into_status())
            }
            Err(join_error) => {
                tracing::error!(
                    is_caller_authenticated = caller.is_authenticated(),
                    caller_issuer = caller.issuer(),
                    caller_subject = caller.subject(),
                    %join_error,
                    "the step down task did not finish; the state of this server process is unknown"
                );
                Err(step_down_task_error(&join_error).into_status())
            }
        }
    }
}

/// Validates the request's untrusted precondition fields.
fn step_down_precondition(
    request: StepDownActiveNodeRequest,
) -> Result<StepDownPrecondition, FlowOperationError> {
    if request.expected_node_id.as_deref() == Some("") {
        return Err(FlowOperationError::invalid_argument(
            "expected_node_id must not be empty when set",
        ));
    }
    Ok(StepDownPrecondition {
        expected_node_id: request.expected_node_id,
        expected_node_epoch: request.expected_node_epoch,
    })
}

fn describe_error(error: ActiveNodeDescriptionError) -> FlowOperationError {
    match error {
        ActiveNodeDescriptionError::NotActive { .. } => {
            FlowOperationError::failed_precondition(error.to_string())
        }
    }
}

/// Maps a step down failure; none is UNAVAILABLE, because a retry would step down the successor.
fn step_down_error(error: ActiveNodeStepDownError) -> FlowOperationError {
    match error {
        ActiveNodeStepDownError::NotActive { .. }
        | ActiveNodeStepDownError::PreconditionMismatch { .. }
        | ActiveNodeStepDownError::AlreadySteppingDown => {
            FlowOperationError::failed_precondition(error.to_string())
        }
        ActiveNodeStepDownError::LeaseReleaseUnconfirmed { .. } => FlowOperationError::Internal {
            detail: format!(
                "{error}; the node lease may stay held until it expires, and this process stopped renewing it"
            ),
        },
        ActiveNodeStepDownError::Internal { .. } => FlowOperationError::Internal {
            detail: error.to_string(),
        },
    }
}

/// Maps a panicked or cancelled step down task without claiming what the process does next.
fn step_down_task_error(join_error: &JoinError) -> FlowOperationError {
    let cause = if join_error.is_panic() {
        "the step down panicked"
    } else {
        "the step down task was cancelled, for example because the server is shutting down"
    };
    FlowOperationError::Internal {
        detail: format!("{cause}; the state of this server process is unknown"),
    }
}

fn step_down_response(outcome: ActiveNodeStepDownOutcome) -> StepDownActiveNodeResponse {
    StepDownActiveNodeResponse {
        node_id: outcome.node_id,
        released_node_epoch: outcome.released_node_epoch,
        lease_reacquisition_hold_off: Some(saturating_protobuf_duration(
            outcome.lease_reacquisition_hold_off,
        )),
        lease_holder_after_step_down: Some(match outcome.lease_holder_after_step_down {
            LeaseHolderAfterStepDown::TakenOverByAnotherProcess(holder) => {
                LeaseHolderMessage::TakenOverByAnotherProcess(node_lease_holder_message(holder))
            }
            LeaseHolderAfterStepDown::TakenBackByThisProcess(holder) => {
                LeaseHolderMessage::TakenBackByThisProcess(node_lease_holder_message(holder))
            }
            LeaseHolderAfterStepDown::Unknown { detail } => {
                LeaseHolderMessage::LeaseHolderUnknown(NodeLeaseHolderUnknown { detail })
            }
        }),
    }
}

fn node_lease_holder_message(holder: NodeLeaseHolder) -> NodeLeaseHolderMessage {
    NodeLeaseHolderMessage {
        node_epoch: holder.node_epoch,
        holder_process_id: holder.holder_process_id,
    }
}

/// Converts `duration`, capped at the protobuf maximum so a completed step down never fails to encode.
fn saturating_protobuf_duration(duration: Duration) -> prost_types::Duration {
    if duration.as_secs() >= MAX_PROTOBUF_DURATION_SECONDS {
        return prost_types::Duration {
            seconds: i64::try_from(MAX_PROTOBUF_DURATION_SECONDS)
                .expect("the protobuf duration maximum fits in i64"),
            nanos: 0,
        };
    }
    prost_types::Duration::try_from(duration)
        .expect("a duration below the protobuf maximum converts to prost_types::Duration")
}
