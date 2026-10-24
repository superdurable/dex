// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use crate::{AttributeStoreError, AttributeStoreFailureScope};

fn database_error(sqlstate: Option<&str>) -> AttributeStoreError {
    AttributeStoreError::Database {
        store: "orders".to_owned(),
        operation: "write table \"public\".\"orders\"".to_owned(),
        message: "test failure".to_owned(),
        sqlstate: sqlstate.map(str::to_owned),
    }
}

#[test]
fn sqlstates_blaming_the_batch_content_hold_back_only_that_batch() {
    for sqlstate in [
        "21000", // ON CONFLICT affects a row twice
        "22001", // value too long for the column type
        "22P02", // invalid text for an enum or numeric column
        "23502", // NULL in a NOT NULL column
        "23505", // unique violation on a secondary index
        "23514", // CHECK violation
        "42703", // undefined column
        "42804", // datatype mismatch
        "44000", // WITH CHECK OPTION violation
        "54000", // index row too large
        "P0001", // a trigger raised an exception
    ] {
        let error = database_error(Some(sqlstate));
        assert!(error.is_retryable(), "{sqlstate}");
        assert_eq!(
            error.failure_scope(),
            AttributeStoreFailureScope::Batch,
            "{sqlstate}"
        );
    }
}

#[test]
fn connection_unavailability_and_store_wide_sqlstates_hold_back_the_store() {
    for sqlstate in [
        None,          // transport failure or closed connection
        Some("08006"), // connection failure
        Some("28P01"), // password authentication failed
        Some("3D000"), // the database does not exist
        Some("40001"), // serialization failure
        Some("40P01"), // deadlock detected
        Some("42501"), // insufficient privilege
        Some("53300"), // too many connections
        Some("57014"), // statement timeout
        Some("57P01"), // administrator shutdown
        Some("58030"), // I/O error
        Some("XX000"), // internal error
    ] {
        let error = database_error(sqlstate);
        assert!(error.is_retryable(), "{sqlstate:?}");
        assert_eq!(
            error.failure_scope(),
            AttributeStoreFailureScope::Store,
            "{sqlstate:?}"
        );
    }
    let timeout = AttributeStoreError::Timeout {
        store: "orders".to_owned(),
        operation: "write a batch".to_owned(),
        timeout: Duration::from_secs(1),
    };
    assert!(timeout.is_retryable());
    assert_eq!(timeout.failure_scope(), AttributeStoreFailureScope::Store);
    let fenced = AttributeStoreError::Fenced {
        store: "orders".to_owned(),
        cluster_id: "c1".to_owned(),
        scope: "node:a".to_owned(),
        token: 3,
    };
    assert!(!fenced.is_retryable());
    assert_eq!(fenced.failure_scope(), AttributeStoreFailureScope::Store);
}
