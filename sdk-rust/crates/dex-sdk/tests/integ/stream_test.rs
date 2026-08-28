// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::{Duration, SystemTime};

use dex_sdk::Registry;

use crate::stream_workflow::{PROGRESS, StreamTestWorkflow};
use crate::support::{DexDevTestEnvironment, flow_id};

#[test]
#[ignore = "requires dexcli dev"]
fn test_stream_round_trip() {
    let workflow = StreamTestWorkflow::new();
    let environment = DexDevTestEnvironment::start(Registry::new().register(workflow.clone()));
    let flow_id = flow_id("stream");
    let run_id = environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Stream Flow");
    environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Stream Flow");

    environment
        .client
        .write_stream(
            &flow_id,
            &PROGRESS,
            "client-write",
            "client-progress".to_string(),
        )
        .expect("write client Stream message");
    environment
        .client
        .write_stream(
            &flow_id,
            &PROGRESS,
            "client-write",
            "duplicate-ignored".to_string(),
        )
        .expect("deduplicate client Stream message");

    let step = environment
        .client
        .read_stream_with_timeout(&flow_id, &PROGRESS, "", Duration::from_secs(30))
        .expect("read Step Stream message");
    assert_eq!("step-progress", step.value);
    assert!(!step.resume_token.is_empty());
    assert!(step.created_time > SystemTime::UNIX_EPOCH);
    assert!(step.idempotency_key.starts_with(&format!("{run_id}#")));

    let client = environment
        .client
        .read_stream_with_timeout(
            &flow_id,
            &PROGRESS,
            &step.resume_token,
            Duration::from_secs(30),
        )
        .expect("read client Stream message");
    assert_eq!("client-progress", client.value);
    assert_ne!(step.resume_token, client.resume_token);
    assert!(client.created_time > SystemTime::UNIX_EPOCH);
    assert_eq!("client-write", client.idempotency_key);
}
