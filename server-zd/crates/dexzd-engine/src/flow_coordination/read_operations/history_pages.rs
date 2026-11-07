// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! GetHistoryEvents across uploaded segments and local rows; see the crate README.

use std::collections::BTreeSet;

use dexzd_apply::LoadedHistorySegments;
use dexzd_apply::read_model::SegmentedHistoryPage;
use dexzd_keyspace::{RunId, TenantId};
use dexzd_protocol::dex::{GetHistoryEventsRequest, GetHistoryEventsResponse};
use dexzd_protocol::dexzd::v1::HistorySegmentRecord;
use futures::future::join_all;

use crate::flow_coordination::history_reads::{HistorySegmentReadError, SegmentLocation};
use crate::flow_coordination::read_operations::read_model_adapter;
use crate::flow_coordination::{FlowAddress, FlowCoordinationError, FlowCoordinatorRegistry};

impl FlowCoordinatorRegistry {
    /// Returns a page of history events of a run through apply's read model.
    ///
    /// Covered events come from object storage, the rest locally.
    ///
    /// Returns `UNAVAILABLE` when object storage fails or lacks an uploaded segment.
    pub async fn get_history_events(
        &self,
        tenant_id: TenantId,
        request: GetHistoryEventsRequest,
    ) -> Result<GetHistoryEventsResponse, FlowCoordinationError> {
        self.check_running()?;
        let flow = FlowAddress::new(tenant_id, request.flow_id.clone());
        let mut run_id_text = request.run_id.clone();
        let mut loaded = LoadedHistorySegments::default();
        let mut missing_objects = BTreeSet::new();
        let max_rounds = self
            .inner
            .dependencies
            .history_segment_reader
            .max_page_rounds();
        for round in 1..=max_rounds {
            let is_last_round = round == max_rounds;
            let (run_id, needed) = match self.read_history_round(
                &flow,
                &run_id_text,
                &request,
                &loaded,
                is_last_round,
            )? {
                (_, SegmentedHistoryPage::Page(page)) => {
                    if missing_objects.is_empty() && loaded.is_empty() {
                        self.inner
                            .dependencies
                            .history_segment_reader
                            .record_local_page();
                    }
                    return Ok(page);
                }
                (run_id, SegmentedHistoryPage::NeedsSegments(needed)) if !needed.is_empty() => {
                    (run_id, needed)
                }
                // The catalog budget ended an empty page; no fetch can help.
                (_, SegmentedHistoryPage::NeedsSegments(_)) => break,
            };
            // Later rounds read the same run even if the Flow starts a new one.
            run_id_text = run_id.to_string();
            self.load_history_segments(&flow, run_id, &needed, &mut loaded, &mut missing_objects)
                .await?;
        }
        Err(FlowCoordinationError::Unavailable {
            detail: format!(
                "the history page of flow {} made no progress in {max_rounds} rounds of segment \
                 reads; retry",
                flow.flow_id
            ),
        })
    }

    /// Runs the read model on a new committed view, which is released before this returns.
    fn read_history_round(
        &self,
        flow: &FlowAddress,
        run_id_text: &str,
        request: &GetHistoryEventsRequest,
        loaded: &LoadedHistorySegments,
        is_last_round: bool,
    ) -> Result<(RunId, SegmentedHistoryPage), FlowCoordinationError> {
        let committed = self.inner.dependencies.committer.committed_view()?;
        read_model_adapter::read_history_page(
            committed.as_ref(),
            self.flow_context(flow),
            flow,
            run_id_text,
            request,
            self.inner.config.max_history_events_response_bytes,
            loaded,
            is_last_round,
        )
    }

    /// Fetches the `needed` segments concurrently and adds them to `loaded`.
    ///
    /// A segment whose object is missing twice fails the read; once, the next view explains it.
    async fn load_history_segments(
        &self,
        flow: &FlowAddress,
        run_id: RunId,
        needed: &[HistorySegmentRecord],
        loaded: &mut LoadedHistorySegments,
        missing_objects: &mut BTreeSet<(u64, u64)>,
    ) -> Result<(), FlowCoordinationError> {
        let context = self.flow_context(flow);
        let reader = &self.inner.dependencies.history_segment_reader;
        let reads = needed.iter().map(|segment| {
            reader.read_records(SegmentLocation {
                tenant_id: flow.tenant_id,
                flow_hash: context.flow_hash,
                run_id,
                segment,
            })
        });
        for (segment, result) in needed.iter().zip(join_all(reads).await) {
            let range = (segment.first_seq, segment.last_seq);
            match result.map_err(|error| segment_read_error(flow, error))? {
                Some(records) => loaded.insert(segment, records)?,
                None if missing_objects.insert(range) => {}
                None => {
                    tracing::error!(
                        flow_id = %flow.flow_id,
                        %run_id,
                        first_seq = segment.first_seq,
                        last_seq = segment.last_seq,
                        "an uploaded history segment object is missing from object storage"
                    );
                    return Err(FlowCoordinationError::Unavailable {
                        detail: format!(
                            "history segment {}..={} of flow {} is missing from object storage",
                            segment.first_seq, segment.last_seq, flow.flow_id
                        ),
                    });
                }
            }
        }
        Ok(())
    }
}

fn segment_read_error(flow: &FlowAddress, error: HistorySegmentReadError) -> FlowCoordinationError {
    if matches!(error, HistorySegmentReadError::Mismatch { .. }) {
        tracing::error!(flow_id = %flow.flow_id, %error, "an uploaded history segment cannot be read");
    }
    FlowCoordinationError::Unavailable {
        detail: error.to_string(),
    }
}
