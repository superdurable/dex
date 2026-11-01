// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Instant;

use dexzd_cluster_metadata::LeaseValidityDeadline;
use tonic::{Request, Response, Status};
use tonic_health::ServingStatus;
use tonic_health::pb::health_server::{Health, HealthServer};
use tonic_health::pb::{HealthCheckRequest, HealthCheckResponse, health_check_response};
use tonic_health::server::{HealthReporter, HealthService};

/// Service names that `grpc.health.v1` reports; `dexcli dev` waits until all are SERVING.
pub const HEALTH_SERVICE_NAMES: [&str; 3] = ["", "dex.FlowService", "dex.WorkerTaskService"];
/// Health name SERVING only on a hot standby within its lag threshold; for rollouts, never readiness.
pub const HOT_STANDBY_HEALTH_SERVICE_NAME: &str = "dex.HotStandby";

/// Controls the `grpc.health.v1` status of every Dex readiness name together, and of the hot standby name.
#[derive(Clone)]
pub struct HealthStatus {
    reporter: HealthReporter,
    lease_validity_deadline: LeaseValidityDeadline,
}

impl HealthStatus {
    /// Creates the health service with every name NOT_SERVING; readiness also follows `lease_validity_deadline`.
    pub async fn new(
        lease_validity_deadline: LeaseValidityDeadline,
    ) -> (Self, HealthServer<impl Health>) {
        let (reporter, _server) = tonic_health::server::health_reporter();
        let status = Self {
            reporter,
            lease_validity_deadline,
        };
        status.set(ServingStatus::NotServing).await;
        status.set_hot_standby(false).await;
        let server = status.server();
        (status, server)
    }

    /// Returns another health service over the same statuses, for a new gRPC server instance.
    pub fn server(&self) -> HealthServer<impl Health + use<>> {
        HealthServer::new(LeaseCheckedHealth {
            reported: HealthService::from_health_reporter(self.reporter.clone()),
            lease_validity_deadline: self.lease_validity_deadline.clone(),
        })
    }

    /// Reports whether this process is a hot standby within its lag threshold.
    pub async fn set_hot_standby(&self, is_hot_standby: bool) {
        let status = if is_hot_standby {
            ServingStatus::Serving
        } else {
            ServingStatus::NotServing
        };
        self.reporter
            .set_service_status(HOT_STANDBY_HEALTH_SERVICE_NAME, status)
            .await;
    }

    /// Reports SERVING for every readiness name.
    pub async fn set_serving(&self) {
        self.set(ServingStatus::Serving).await;
    }

    /// Reports NOT_SERVING for every readiness name, for example during shutdown.
    pub async fn set_not_serving(&self) {
        self.set(ServingStatus::NotServing).await;
    }

    async fn set(&self, status: ServingStatus) {
        for service_name in HEALTH_SERVICE_NAMES {
            self.reporter.set_service_status(service_name, status).await;
        }
    }
}

/// Readiness names answer NOT_SERVING while the lease is invalid; Watch streams miss a passing deadline.
struct LeaseCheckedHealth {
    reported: HealthService,
    lease_validity_deadline: LeaseValidityDeadline,
}

#[tonic::async_trait]
impl Health for LeaseCheckedHealth {
    async fn check(
        &self,
        request: Request<HealthCheckRequest>,
    ) -> Result<Response<HealthCheckResponse>, Status> {
        let is_readiness_name = HEALTH_SERVICE_NAMES.contains(&request.get_ref().service.as_str());
        let mut response = self.reported.check(request).await?;
        if is_readiness_name && !self.lease_validity_deadline.is_valid_at(Instant::now()) {
            response.get_mut().status = health_check_response::ServingStatus::NotServing as i32;
        }
        Ok(response)
    }

    type WatchStream = <HealthService as Health>::WatchStream;

    async fn watch(
        &self,
        request: Request<HealthCheckRequest>,
    ) -> Result<Response<Self::WatchStream>, Status> {
        self.reported.watch(request).await
    }
}
