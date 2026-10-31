// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::sync::Arc;

use dexzd_api::FlowOperationError;
use dexzd_cluster_metadata::LeaseStatus;
use dexzd_keyspace::PartitionId;
use dexzd_protocol::dex::admin_service_server::AdminService;
use dexzd_protocol::dex::{
    ErrorSubStatus, GetSearchProjectionStatusResponse, RebuildSearchProjectionRequest,
    RebuildSearchProjectionResponse, SearchProjectionPartitionStatus,
};
use tokio::sync::watch;
use tonic::{Code, Request, Response, Status};

use super::CommittedSearchFrames;

#[derive(Clone)]
pub(crate) struct SearchProjectionAdmin {
    frames: Option<Arc<CommittedSearchFrames>>,
    partition_count: u32,
    lease: watch::Receiver<LeaseStatus>,
}

impl SearchProjectionAdmin {
    pub(crate) fn new(
        frames: Option<Arc<CommittedSearchFrames>>,
        partition_count: u32,
        lease: watch::Receiver<LeaseStatus>,
    ) -> Self {
        Self {
            frames,
            partition_count,
            lease,
        }
    }

    fn active_frames(&self) -> Result<&CommittedSearchFrames, Status> {
        if !matches!(*self.lease.borrow(), LeaseStatus::ValidUntil(deadline) if deadline > tokio::time::Instant::now())
        {
            return Err(rejected(
                Code::Unavailable,
                "search administration requires the active partition owner",
            ));
        }
        self.frames.as_deref().ok_or_else(|| {
            rejected(
                Code::FailedPrecondition,
                "search administration requires a PostgreSQL or ParadeDB search backend",
            )
        })
    }
}

#[tonic::async_trait]
impl AdminService for SearchProjectionAdmin {
    async fn rebuild_search_projection(
        &self,
        request: Request<RebuildSearchProjectionRequest>,
    ) -> Result<Response<RebuildSearchProjectionResponse>, Status> {
        let frames = self.active_frames()?;
        let request = request.into_inner();
        let selection_count = u8::from(!request.partition_ids.is_empty())
            + u8::from(request.all_marked_partitions)
            + u8::from(request.all_partitions);
        if selection_count != 1 {
            return Err(rejected(
                Code::InvalidArgument,
                "select partition_ids, all_marked_partitions, or all_partitions exactly once",
            ));
        }
        let partitions: BTreeSet<_> = if request.all_partitions {
            (0..self.partition_count).collect()
        } else if request.all_marked_partitions {
            frames
                .rebuild_status()
                .0
                .keys()
                .map(|partition| u32::from(partition.0))
                .collect()
        } else {
            request.partition_ids.into_iter().collect()
        };
        let partitions: Vec<_> = partitions
            .into_iter()
            .map(|partition| {
                if partition >= self.partition_count {
                    return Err(rejected(
                        Code::InvalidArgument,
                        format!(
                            "partition {partition} exceeds configured partition count {}",
                            self.partition_count
                        ),
                    ));
                }
                Ok(PartitionId(
                    u16::try_from(partition).expect("validated partition fits u16"),
                ))
            })
            .collect::<Result<_, _>>()?;
        let scheduled = frames.schedule_rebuilds(&partitions);
        Ok(Response::new(RebuildSearchProjectionResponse {
            scheduled_partition_ids: scheduled
                .into_iter()
                .map(|partition| u32::from(partition.0))
                .collect(),
        }))
    }

    async fn get_search_projection_status(
        &self,
        _request: Request<()>,
    ) -> Result<Response<GetSearchProjectionStatusResponse>, Status> {
        let frames = self.active_frames()?;
        let (needed, scheduled, active) = frames.rebuild_status();
        let partitions: BTreeSet<_> = needed
            .keys()
            .chain(&scheduled)
            .chain(&active)
            .copied()
            .collect();
        let partitions = partitions
            .into_iter()
            .map(|partition| SearchProjectionPartitionStatus {
                partition_id: u32::from(partition.0),
                rebuild_reasons: needed
                    .get(&partition)
                    .into_iter()
                    .flatten()
                    .map(|reason| reason.metric_label_value().to_owned())
                    .collect(),
                is_scheduled: scheduled.contains(&partition),
                is_in_progress: active.contains(&partition),
            })
            .collect();
        Ok(Response::new(GetSearchProjectionStatusResponse {
            partitions,
        }))
    }
}

fn rejected(code: Code, detail: impl Into<String>) -> Status {
    FlowOperationError::rejected(code, ErrorSubStatus::Uncategorized, detail).into_status()
}
