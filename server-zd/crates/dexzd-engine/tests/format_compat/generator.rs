// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs;
use std::path::PathBuf;

use crate::golden_replay::SnapshotUse;
use crate::older_backend_error::replay_with_older_backend_errors;
use crate::recording::record_workload;
use crate::state_dump::render_state_dump;
use crate::testdata_tree::testdata_root;

/// Directory the generator writes; it must not exist yet or be empty.
const OUTPUT_DIRECTORY_VARIABLE: &str = "DEXZD_FORMAT_COMPAT_OUTPUT";

#[tokio::test(start_paused = true)]
#[ignore = "writes a new testdata version; set DEXZD_FORMAT_COMPAT_OUTPUT and run explicitly"]
async fn generate_format_compat_testdata() {
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
            "refusing to overwrite {}; recorded versions are immutable",
            output_directory.display()
        );
    }
    fs::create_dir_all(&output_directory).expect("create the output directory");
    record_workload().await.write_to(&output_directory).await;
}

#[tokio::test]
#[ignore = "writes replay expectations; set DEXZD_FORMAT_COMPAT_OUTPUT and run explicitly"]
async fn generate_pre_offload_proto_removal_expectations() {
    let output_directory = PathBuf::from(
        std::env::var_os(OUTPUT_DIRECTORY_VARIABLE)
            .unwrap_or_else(|| panic!("set {OUTPUT_DIRECTORY_VARIABLE} to a new directory")),
    );
    fs::create_dir_all(&output_directory).expect("create the output directory");
    assert!(
        fs::read_dir(&output_directory)
            .expect("read the output directory")
            .next()
            .is_none(),
        "the output directory must be empty"
    );
    let directory = testdata_root().join("pre-offload-choice-log");
    for (name, snapshot_use) in [
        ("restore", SnapshotUse::Restore),
        ("ignore", SnapshotUse::Ignore),
    ] {
        let (state, _) = replay_with_older_backend_errors(&directory, snapshot_use).await;
        fs::write(
            output_directory.join(format!("{name}.txt")),
            render_state_dump(&state),
        )
        .expect("write the replay expectation");
    }
}
