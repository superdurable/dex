// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Default-order searches through the SearchOrder index against the full-scan path.

use std::sync::Arc;
use std::sync::atomic::Ordering;

use dexzd_keyspace::{FlowHash, PartitionId, TenantId};
use dexzd_protocol::dex::{self, FlowStatus, IndexType};
use dexzd_protocol::dexzd::v1::{SearchAttributeValue, SearchDocumentRecord};
use dexzd_visibility_query::{FieldRegistry, parse_and_check};

use super::tests::{MemoryState, MemoryViewProvider, fields, next_pseudorandom, record, request};
use super::{BuiltinSearchBackend, merge_pages, pinned_flow_type};
use crate::page_token::PagePosition;
use crate::{SearchBackend, SearchError, SearchPage};

/// Pages through the full-scan path, which every query used before the SearchOrder index.
fn full_scan_page(
    backend: &BuiltinSearchBackend,
    text: &str,
    token: Option<&str>,
    page_size: usize,
    fields: &FieldRegistry,
) -> Result<SearchPage, SearchError> {
    let query = parse_and_check(text, fields).expect("checked query");
    let cursor = token
        .map(|token| PagePosition::decode(token, TenantId(7), text))
        .transpose()?;
    let view = backend.view_provider.committed_view()?;
    let partitions = backend.scan_all_partitions(
        view.as_ref(),
        TenantId(7),
        pinned_flow_type(query.expression()),
        &query,
        cursor.as_ref(),
    )?;
    Ok(merge_pages(
        partitions,
        &query,
        TenantId(7),
        text,
        page_size,
    ))
}

fn bulk_record(
    index: u32,
    flow_type: &str,
    status: FlowStatus,
    start_micros: i64,
) -> SearchDocumentRecord {
    let mut run_id = [0; 16];
    run_id[12..].copy_from_slice(&index.to_be_bytes());
    let close_micros = if status == FlowStatus::Running {
        0
    } else {
        start_micros + 5
    };
    SearchDocumentRecord {
        version: 1,
        tenant_id: 7,
        flow_id: format!("bulk-{index}"),
        run_id: run_id.to_vec(),
        flow_type: flow_type.into(),
        status: status.into(),
        started_at_micros: start_micros,
        closed_at_micros: close_micros,
        ..Default::default()
    }
}

fn bulk_state(document_count: u32, partition_count: u32) -> MemoryState {
    let mut state = MemoryState::default();
    for index in 0..document_count {
        let status = if index % 7 == 0 {
            FlowStatus::Running
        } else {
            FlowStatus::Completed
        };
        let flow_type = if index % 3 == 0 {
            "Billing"
        } else {
            "Checkout"
        };
        let partition = PartitionId(u16::try_from(index % partition_count).expect("partition"));
        let flow_hash = FlowHash((u64::from(partition.0) << 48) | u64::from(index));
        let start = i64::from(index % 1_000);
        state.insert_run(
            partition,
            flow_hash,
            &bulk_record(index, flow_type, status, start),
        );
    }
    state
}

#[tokio::test]
async fn default_order_pages_match_the_full_scan_path() {
    let mut state = MemoryState::default();
    let mut random_state = 0x51a7_e0c4_2b19_d863_u64;
    for ordinal in 1..=150_u8 {
        let status = match next_pseudorandom(&mut random_state) % 4 {
            0 => FlowStatus::Running,
            1 => FlowStatus::Failed,
            _ => FlowStatus::Completed,
        };
        let flow_type = if next_pseudorandom(&mut random_state).is_multiple_of(3) {
            "Billing"
        } else {
            "Checkout"
        };
        let start = 1 + (next_pseudorandom(&mut random_state) % 6) as i64;
        let close = if status == FlowStatus::Running {
            0
        } else {
            start + (next_pseudorandom(&mut random_state) % 3) as i64
        };
        let mut document = record(7, ordinal, flow_type, status, start, close);
        if next_pseudorandom(&mut random_state).is_multiple_of(2) {
            document.attributes.push(SearchAttributeValue {
                index_key: "Color".into(),
                index_type: IndexType::Keyword.into(),
                value: Some(dex::Value {
                    kind: Some(dex::value::Kind::StringValue("red".into())),
                }),
            });
        }
        let partition = (next_pseudorandom(&mut random_state) % 4) as u16;
        state.insert(partition, 7, ordinal, document);
    }
    state.insert(
        2,
        8,
        200,
        record(8, 200, "Checkout", FlowStatus::Running, 9, 0),
    );
    let backend =
        BuiltinSearchBackend::new(MemoryViewProvider::new(Arc::new(state)), 4).expect("backend");
    let fields = fields();
    for text in [
        "",
        "FlowType = 'Checkout'",
        "FlowType = 'Billing' AND Color = 'red'",
        "Color = 'red' AND FlowType = 'Checkout'",
        "ExecutionStatus = 'Running'",
        "NOT Color = 'red'",
        "FlowType = 'Missing'",
    ] {
        for page_size in [1, 4, 9, 1_000] {
            let mut token: Option<String> = None;
            let mut pages = 0;
            loop {
                let indexed = backend
                    .search(request(text, token.as_deref(), page_size, &fields))
                    .await
                    .expect(text);
                let scanned = full_scan_page(&backend, text, token.as_deref(), page_size, &fields)
                    .expect(text);
                assert_eq!(indexed.entries, scanned.entries, "{text} page {pages}");
                assert_eq!(
                    indexed.next_page_token, scanned.next_page_token,
                    "{text} page {pages}"
                );
                pages += 1;
                token = indexed.next_page_token;
                if token.is_none() {
                    break;
                }
            }
            assert!(pages >= 1);
        }
    }
}

#[tokio::test]
async fn default_order_search_reads_about_one_page_of_documents() {
    let state = Arc::new(bulk_state(4_000, 8));
    let backend =
        BuiltinSearchBackend::new(MemoryViewProvider::new(state.clone()), 8).expect("backend");
    let fields = fields();
    for (text, page_size, expected_reads) in [
        ("", 20, 21),
        ("FlowType = 'Checkout'", 20, 21),
        (
            "ExecutionStatus = 'Running' AND FlowType = 'Billing'",
            10,
            11,
        ),
    ] {
        state.document_reads.store(0, Ordering::Relaxed);
        let page = backend
            .search(request(text, None, page_size, &fields))
            .await
            .expect(text);
        assert_eq!(page.entries.len(), page_size, "{text}");
        assert!(page.next_page_token.is_some(), "{text}");
        assert_eq!(
            state.document_reads.load(Ordering::Relaxed),
            expected_reads,
            "{text}"
        );
    }
    state.document_reads.store(0, Ordering::Relaxed);
    let full = full_scan_page(&backend, "", None, 20, &fields).expect("full scan");
    assert_eq!(full.entries.len(), 20);
    assert_eq!(state.document_reads.load(Ordering::Relaxed), 4_000);
}

#[tokio::test]
async fn default_order_search_returns_a_page_beyond_the_old_scan_budget() {
    let state = Arc::new(bulk_state(200_000, 4));
    let backend =
        BuiltinSearchBackend::new(MemoryViewProvider::new(state.clone()), 4).expect("backend");
    let fields = fields();
    assert!(matches!(
        full_scan_page(&backend, "", None, 50, &fields),
        Err(SearchError::ResourceExhausted(_))
    ));
    for text in ["", "FlowType = 'Billing'"] {
        state.document_reads.store(0, Ordering::Relaxed);
        let page = backend
            .search(request(text, None, 50, &fields))
            .await
            .expect(text);
        assert_eq!(page.entries.len(), 50, "{text}");
        assert!(
            page.entries
                .iter()
                .all(|entry| entry.flow_status == FlowStatus::Running as i32),
            "{text}"
        );
        assert_eq!(state.document_reads.load(Ordering::Relaxed), 51, "{text}");
        state.document_reads.store(0, Ordering::Relaxed);
        let second = backend
            .search(request(text, page.next_page_token.as_deref(), 50, &fields))
            .await
            .expect(text);
        assert_eq!(second.entries.len(), 50, "{text}");
        assert_eq!(state.document_reads.load(Ordering::Relaxed), 51, "{text}");
    }
    assert!(matches!(
        backend
            .search(request("ORDER BY StartTime DESC", None, 50, &fields))
            .await,
        Err(SearchError::ResourceExhausted(_))
    ));
}
