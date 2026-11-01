// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use tonic_health::ServingStatus;
use tonic_health::pb::health_server::{Health, HealthServer};
use tonic_health::server::{HealthReporter, HealthService};

/// Service names that `grpc.health.v1` reports; `dexcli dev` waits until all are SERVING.
pub const HEALTH_SERVICE_NAMES: [&str; 3] = ["", "dex.FlowService", "dex.WorkerTaskService"];
/// Service name that reports SERVING only while this process is a hot standby within its lag threshold.
///
/// Readiness probes must not check it; rollouts check it to wait for a hot standby.
pub const HOT_STANDBY_HEALTH_SERVICE_NAME: &str = "dex.HotStandby";

/// Controls the `grpc.health.v1` status of every Dex readiness name together, and of the hot standby name.
#[derive(Clone)]
pub struct HealthStatus {
    reporter: HealthReporter,
}

impl HealthStatus {
    /// Creates the health service with every name NOT_SERVING.
    pub async fn new() -> (Self, HealthServer<impl Health>) {
        let (reporter, server) = tonic_health::server::health_reporter();
        let status = Self { reporter };
        status.set(ServingStatus::NotServing).await;
        status.set_hot_standby(false).await;
        (status, server)
    }

    /// Returns another health service over the same statuses, for a new gRPC server instance.
    pub fn server(&self) -> HealthServer<impl Health> {
        HealthServer::new(HealthService::from_health_reporter(self.reporter.clone()))
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
