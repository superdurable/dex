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
    environment
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
            "client#source",
            "client-progress".to_string(),
        )
        .expect("write client Stream message");
    environment
        .client
        .write_stream(
            &flow_id,
            &PROGRESS,
            "client#source",
            "client-progress-again".to_string(),
        )
        .expect("append repeated-source client Stream message");

    let mut resume_token = String::new();
    let mut step_source = None;
    for expected in [
        "wait-progress-1",
        "wait-progress-2",
        "execute-progress-1",
        "execute-progress-2",
    ] {
        let message = environment
            .client
            .read_stream_with_timeout(&flow_id, &PROGRESS, &resume_token, Duration::from_secs(30))
            .expect("read Step Stream message");
        assert_eq!(expected, message.value);
        assert!(!message.resume_token.is_empty());
        assert!(message.created_time > SystemTime::UNIX_EPOCH);
        assert!(message.source.starts_with('#'));
        assert!(!message.source[1..].is_empty());
        if let Some(source) = step_source.as_ref() {
            assert_eq!(source, &message.source);
        } else {
            step_source = Some(message.source.clone());
        }
        resume_token = message.resume_token;
    }

    for expected in ["client-progress", "client-progress-again"] {
        let message = environment
            .client
            .read_stream_with_timeout(&flow_id, &PROGRESS, &resume_token, Duration::from_secs(30))
            .expect("read client Stream message");
        assert_eq!(expected, message.value);
        assert_ne!(resume_token, message.resume_token);
        assert!(message.created_time > SystemTime::UNIX_EPOCH);
        assert_eq!("client#source", message.source);
        resume_token = message.resume_token;
    }
}
