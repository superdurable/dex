// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Writes synchronized Attribute batches into Postgres tables with the Go Server's Attribute Store contract and a per-cluster fencing token.

mod connection;
mod error;
mod map_table;
mod row_key;
mod schema;
mod settings;
mod statements;
mod store;
mod value_conversion;

#[cfg(test)]
mod error_tests;
#[cfg(test)]
mod postgres_integration_tests;
#[cfg(test)]
mod row_key_tests;
#[cfg(test)]
mod statements_tests;
#[cfg(test)]
mod value_conversion_tests;

pub use error::{AttributeStoreError, AttributeStoreFailureScope};
pub use settings::{
    AttributeMapTableSettings, AttributeStoreSettings, AttributeSyncItem, ColumnNaming,
    DEFAULT_FENCING_TABLE_NAME, DEFAULT_SCHEMA_SYNC_INTERVAL, DEFAULT_TRANSACTION_TIMEOUT,
    FencingToken, UnsupportedColumnNaming,
};
pub use store::AttributeStore;
