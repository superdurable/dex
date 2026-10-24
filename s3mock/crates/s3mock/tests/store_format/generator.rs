// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs;
use std::path::PathBuf;

use s3mock::{CURRENT_STORE_FORMAT_VERSION, Durability};

use crate::contents::read_store_contents;
use crate::recorded_store::{
    EXPECTED_CONTENTS_FILE_NAME, MANIFEST_FILE_NAME, RecordedContents, RecordedManifest,
    STORE_DIRECTORY_NAME, assert_workload_shape, write_json,
};
use crate::store_files::{compact_metadata, open_store};
use crate::workload::{CREDENTIAL_PARENT, write_workload};

/// Directory the generator writes; it must not exist yet or be empty.
const OUTPUT_DIRECTORY_VARIABLE: &str = "S3MOCK_STORE_FORMAT_OUTPUT";

#[test]
#[ignore = "writes a new recorded store; set S3MOCK_STORE_FORMAT_OUTPUT and run explicitly"]
fn generate_recorded_store() {
    let output_directory = PathBuf::from(
        std::env::var_os(OUTPUT_DIRECTORY_VARIABLE)
            .unwrap_or_else(|| panic!("set {OUTPUT_DIRECTORY_VARIABLE} to a new directory")),
    );
    if output_directory.exists() {
        let is_empty = fs::read_dir(&output_directory)
            .expect("read the output directory")
            .next()
            .is_none();
        assert!(
            is_empty,
            "refusing to overwrite {}; recorded stores are immutable",
            output_directory.display()
        );
    }
    let store_directory = output_directory.join(STORE_DIRECTORY_NAME);
    let store = open_store(&store_directory, Durability::Strict);
    let aborted_uploads = write_workload(&store);
    let contents = read_store_contents(&store, CREDENTIAL_PARENT);
    assert_workload_shape(&contents);
    drop(store);
    compact_metadata(&store_directory);
    write_json(
        &output_directory.join(MANIFEST_FILE_NAME),
        &RecordedManifest {
            store_format_version: CURRENT_STORE_FORMAT_VERSION,
            s3mock_version: env!("CARGO_PKG_VERSION").to_owned(),
        },
    );
    write_json(
        &output_directory.join(EXPECTED_CONTENTS_FILE_NAME),
        &RecordedContents {
            store: contents,
            aborted_uploads,
        },
    );
}
