// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The operator-facing `dex.AdminService`.

mod active_node_step_down;

#[cfg(test)]
pub(crate) mod dex_admin_service_tests;

pub use active_node_step_down::{
    ActiveNodeStepDown, ActiveNodeStepDownError, ActiveNodeStepDownOutcome, ActiveNodeTakeover,
};

use std::sync::Arc;

use dexzd_protocol::dex::admin_service_server::{AdminService, AdminServiceServer};
use dexzd_protocol::dex::{
    ActiveNodeTakeover as ActiveNodeTakeoverMessage, StepDownActiveNodeRequest,
    StepDownActiveNodeResponse,
};
use tonic::{Request, Response, Status};

use crate::FlowOperationError;

/// The `dex.AdminService` of server-zd.
///
/// Wrap it with [`crate::AuthenticatedService::for_admin_service`] so admin credentials are required.
pub struct DexAdminService {
    active_node_step_down: Arc<dyn ActiveNodeStepDown>,
}

impl DexAdminService {
    /// Creates the endpoint.
    pub fn new(active_node_step_down: Arc<dyn ActiveNodeStepDown>) -> Self {
        Self {
            active_node_step_down,
        }
    }

    /// Wraps the endpoint in a tonic server.
    pub fn into_server(self) -> AdminServiceServer<Self> {
        AdminServiceServer::new(self)
    }
}

#[tonic::async_trait]
impl AdminService for DexAdminService {
    async fn step_down_active_node(
        &self,
        _request: Request<StepDownActiveNodeRequest>,
    ) -> Result<Response<StepDownActiveNodeResponse>, Status> {
        tracing::info!("received a request to step down the active server process");
        let outcome = self
            .active_node_step_down
            .step_down_active_node()
            .await
            .map_err(|error| {
                tracing::warn!(%error, "the active server process did not step down");
                step_down_error(error).into_status()
            })?;
        Ok(Response::new(step_down_response(outcome)?))
    }
}

/// Maps a step down failure; none is UNAVAILABLE, because a retry would step down the successor.
fn step_down_error(error: ActiveNodeStepDownError) -> FlowOperationError {
    match error {
        ActiveNodeStepDownError::NotActive { .. }
        | ActiveNodeStepDownError::AlreadySteppingDown => {
            FlowOperationError::failed_precondition(error.to_string())
        }
        ActiveNodeStepDownError::LeaseReleaseUnconfirmed { .. } => FlowOperationError::Internal {
            detail: format!("{error}; the node lease stays held until it expires"),
        },
        ActiveNodeStepDownError::Internal { .. } => FlowOperationError::Internal {
            detail: error.to_string(),
        },
    }
}

fn step_down_response(
    outcome: ActiveNodeStepDownOutcome,
) -> Result<StepDownActiveNodeResponse, Status> {
    let lease_reacquisition_hold_off =
        prost_types::Duration::try_from(outcome.lease_reacquisition_hold_off).map_err(|error| {
            FlowOperationError::Internal {
                detail: format!("the lease reacquisition hold-off is not representable: {error}"),
            }
            .into_status()
        })?;
    Ok(StepDownActiveNodeResponse {
        node_id: outcome.node_id,
        released_node_epoch: outcome.released_node_epoch,
        observed_takeover: outcome
            .observed_takeover
            .map(|takeover| ActiveNodeTakeoverMessage {
                node_epoch: takeover.node_epoch,
                holder_process_id: takeover.holder_process_id,
            }),
        lease_reacquisition_hold_off: Some(lease_reacquisition_hold_off),
    })
}
