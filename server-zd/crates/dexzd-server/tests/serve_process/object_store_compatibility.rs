// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    FlowConfig, FlowStartOptions, GetFlowSummaryRequest, StartFlowRequest, StepOptions,
    WorkerTarget,
};
use s3mock_server::SignatureVerification;
use tempfile::TempDir;

use super::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::{
    FLOW_TYPE, HELD_LEASE_DURATION, NODE_ID, PRIMARY_BUCKET, SHUTDOWN_BUDGET, string_value,
};

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn object_store_serve_recovers_after_warm_and_cold_restart() {
    let external_endpoint = std::env::var("DEX_ZD_S3_TEST_ENDPOINT").ok();
    let emulator = if external_endpoint.is_some() {
        None
    } else {
        Some(S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await)
    };
    let endpoint = external_endpoint.unwrap_or_else(|| {
        emulator
            .as_ref()
            .expect("the local emulator is running")
            .endpoint()
    });
    let directory = TempDir::new().expect("the temporary test directory is created");
    let log_directory = std::env::var_os("DEX_ZD_TEST_LOG_DIRECTORY")
        .map(std::path::PathBuf::from)
        .unwrap_or_else(|| directory.path().to_path_buf());
    fs::create_dir_all(&log_directory).expect("the process log directory is created");
    let local_directory = directory.path().join("store");
    let flow_id = format!("object-store-smoke-{NODE_ID}");
    let mut first_run_id = String::new();

    for restart in 0..3 {
        if restart == 2 {
            fs::remove_dir_all(&local_directory).expect("the warm local directory is deleted");
        }
        let address = free_loopback_address();
        let config_path = directory.path().join(format!("serve-{restart}.yaml"));
        ServeConfig {
            primary_endpoint: &endpoint,
            hard_delete_endpoint: None,
            listen: address,
            local_directory: &local_directory,
            lease_duration: HELD_LEASE_DURATION,
            fail_if_held: true,
            parade_db: None,
        }
        .write(&config_path);
        let mut process = ServeProcess::spawn(
            &config_path,
            address,
            log_directory.join(format!("serve-{restart}.log")),
        );
        let channel = process.wait_until_serving().await;
        let mut flow_client = FlowServiceClient::new(channel);
        if restart == 0 {
            first_run_id = flow_client
                .start_flow(StartFlowRequest {
                    flow_id: flow_id.clone(),
                    flow_type: FLOW_TYPE.to_owned(),
                    start_step_type: "GreetStep".to_owned(),
                    step_input: Some(string_value("hello")),
                    step_options: Some(StepOptions {
                        skip_wait_for: true,
                        ..StepOptions::default()
                    }),
                    flow_start_options: Some(FlowStartOptions {
                        flow_config_override: Some(FlowConfig {
                            worker_target: Some(WorkerTarget {
                                is_pull_mode: true,
                                ..WorkerTarget::default()
                            }),
                            ..FlowConfig::default()
                        }),
                        ..FlowStartOptions::default()
                    }),
                    request_id: "object-store-smoke-start".to_owned(),
                    ..StartFlowRequest::default()
                })
                .await
                .unwrap_or_else(|error| panic!("StartFlow failed: {error}; {}", process.log()))
                .into_inner()
                .run_id;
        }
        let summary = flow_client
            .get_flow_summary(GetFlowSummaryRequest {
                flow_id: flow_id.clone(),
                run_id: String::new(),
            })
            .await
            .unwrap_or_else(|error| panic!("GetFlowSummary failed: {error}; {}", process.log()))
            .into_inner();
        assert_eq!(summary.first_run_id, first_run_id, "{}", process.log());
        let status = process.terminate(SHUTDOWN_BUDGET).await;
        assert_eq!(status.code(), Some(0), "{}", process.log());
    }

    if let Some(emulator) = emulator {
        emulator.shutdown().await;
    }
}
