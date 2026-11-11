// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime};

use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{FlowDefinition, GetFlowDefinitionsRequest};
use dex_sdk::{
    BlobCache, BlobCacheConfig, FlowDefinitionDocument, FlowDefinitionUploadOutcome,
    FlowDefinitionUploadResult, Registry, SdkError, Worker, WorkerOptions,
};
use sha2::{Digest, Sha256};
use tokio::runtime::Runtime;

use crate::flow_definition_upload_workflow::FlowDefinitionUploadWorkflow;
use crate::support::{DexDevTestEnvironment, flow_id, server_address};

#[test]
#[ignore = "requires dexcli dev"]
fn test_worker_uploads_registered_definitions_from_paths_and_memory() {
    let path_flow_type = unique_flow_type("Path");
    let mtime_flow_type = unique_flow_type("Mtime");
    let memory_flow_type = unique_flow_type("Memory");
    let unregistered_flow_type = unique_flow_type("Unregistered");
    let old_schema_flow_type = unique_flow_type("OldSchema");
    let working_directory = std::env::current_dir().expect("read working directory");
    let absolute_source_path = working_directory.join("src/path_flow.rs");
    let absolute_source_path = absolute_source_path
        .to_str()
        .expect("UTF-8 working directory");
    let definition_directory = tempfile::tempdir().expect("create Flow definition directory");
    let nested = definition_directory.path().join("nested");
    std::fs::create_dir(&nested).expect("create nested Flow definition directory");
    let path_content = definition(
        &path_flow_type,
        absolute_source_path,
        "\"generatedAt\": \"2026-11-01T08:00:00+08:00\", ",
    );
    std::fs::write(nested.join("path-flow.json"), &path_content).expect("write path definition");
    let mtime_file = definition_directory.path().join("mtime-flow.json");
    std::fs::write(
        &mtime_file,
        definition(&mtime_flow_type, "relative/mtime_flow.rs", ""),
    )
    .expect("write mtime definition");
    let mtime = unix_time(1_793_363_696);
    std::fs::File::options()
        .write(true)
        .open(&mtime_file)
        .and_then(|file| file.set_modified(mtime))
        .expect("set Flow definition modification time");
    std::fs::write(
        definition_directory.path().join("unregistered-flow.json"),
        definition(&unregistered_flow_type, "/elsewhere/unregistered.rs", ""),
    )
    .expect("write unregistered definition");
    std::fs::write(
        definition_directory.path().join("package.json"),
        "{\"name\": \"not-a-flow-definition\"}",
    )
    .expect("write unrelated JSON");
    std::fs::write(
        definition_directory.path().join("old-schema.JSON"),
        old_schema_definition(&old_schema_flow_type),
    )
    .expect("write 1.x definition");
    std::fs::write(definition_directory.path().join("notes.txt"), "not JSON")
        .expect("write unrelated file");
    let memory_content = definition(&memory_flow_type, "/opt/elsewhere/app/memory_flow.go", "");
    let memory_generated_at = unix_time(1_793_577_600);
    let path_workflow = FlowDefinitionUploadWorkflow::new(&path_flow_type);
    let options = WorkerOptions::new()
        .flow_definition_paths([definition_directory.path()])
        .flow_definitions([FlowDefinitionDocument::new(
            memory_flow_type.as_str(),
            memory_content.as_bytes(),
            memory_generated_at,
        )]);

    let environment = DexDevTestEnvironment::start_with_worker_options(
        Registry::new()
            .register(FlowDefinitionUploadWorkflow::new(&path_flow_type))
            .and_then(|registry| {
                registry.register(FlowDefinitionUploadWorkflow::new(&mtime_flow_type))
            })
            .and_then(|registry| {
                registry.register(FlowDefinitionUploadWorkflow::new(&memory_flow_type))
            })
            .and_then(|registry| {
                registry.register(FlowDefinitionUploadWorkflow::new(&old_schema_flow_type))
            }),
        options,
    );

    let stored = get_flow_definitions(&[
        &path_flow_type,
        &mtime_flow_type,
        &memory_flow_type,
        &unregistered_flow_type,
        &old_schema_flow_type,
    ]);
    assert_eq!(3, stored.len());
    assert!(!stored.contains_key(&unregistered_flow_type));
    assert!(!stored.contains_key(&old_schema_flow_type));

    let path_definition = &stored[&path_flow_type];
    assert_eq!(
        path_content.replace(absolute_source_path, "src/path_flow.rs"),
        String::from_utf8_lossy(&path_definition.content)
    );
    let path_info = path_definition.info.as_ref().expect("path definition info");
    assert_eq!(1_793_491_200, generated_at_seconds(path_definition));
    assert!(path_info.uploader.starts_with("sdk-rust/"));

    let mtime_definition = &stored[&mtime_flow_type];
    assert_eq!(
        definition(&mtime_flow_type, "relative/mtime_flow.rs", ""),
        String::from_utf8_lossy(&mtime_definition.content)
    );
    assert_eq!(1_793_363_696, generated_at_seconds(mtime_definition));

    let memory_definition = &stored[&memory_flow_type];
    assert_eq!(
        memory_content.replace("/opt/elsewhere/app/memory_flow.go", "memory_flow.go"),
        String::from_utf8_lossy(&memory_definition.content)
    );
    assert_eq!(1_793_577_600, generated_at_seconds(memory_definition));

    assert_flow_completes(&environment, &path_workflow);
}

#[test]
fn test_worker_rejects_one_flow_type_from_two_sources() {
    let duplicate_flow_type = unique_flow_type("Duplicate");
    let definition_directory = tempfile::tempdir().expect("create Flow definition directory");
    let content = definition(&duplicate_flow_type, "duplicate.rs", "");
    std::fs::write(definition_directory.path().join("duplicate.json"), &content)
        .expect("write duplicate definition");

    let message = worker_configuration_error(
        &duplicate_flow_type,
        WorkerOptions::new()
            .flow_definition_paths([definition_directory.path()])
            .flow_definitions([FlowDefinitionDocument::new(
                duplicate_flow_type.as_str(),
                content,
                unix_time(1_793_491_200),
            )]),
    );

    assert!(
        message.contains("more than one Flow definition"),
        "{message}"
    );
    assert!(message.contains(&duplicate_flow_type), "{message}");
}

#[test]
fn test_worker_rejects_missing_path_invalid_file_and_malformed_generated_at() {
    let flow_type = unique_flow_type("Invalid");
    let definition_directory = tempfile::tempdir().expect("create Flow definition directory");

    let missing = worker_configuration_error(
        &flow_type,
        WorkerOptions::new()
            .flow_definition_paths([definition_directory.path().join("missing.json")]),
    );
    assert!(missing.contains("does not exist"), "{missing}");

    let old_schema = definition_directory.path().join("old-schema.txt");
    std::fs::write(&old_schema, old_schema_definition(&flow_type)).expect("write 1.x definition");
    let invalid = worker_configuration_error(
        &flow_type,
        WorkerOptions::new().flow_definition_paths([&old_schema]),
    );
    assert!(invalid.contains("schemaVersion"), "{invalid}");

    let malformed_directory = definition_directory.path().join("malformed");
    std::fs::create_dir(&malformed_directory).expect("create malformed directory");
    std::fs::write(
        malformed_directory.join("flow.json"),
        definition(
            &flow_type,
            "flow.rs",
            "\"generatedAt\": \"2026-11-01 00:00:00+00:00\", ",
        ),
    )
    .expect("write malformed definition");
    let malformed = worker_configuration_error(
        &flow_type,
        WorkerOptions::new().flow_definition_paths([&malformed_directory]),
    );
    assert!(malformed.contains("RFC 3339"), "{malformed}");
}

fn worker_configuration_error(flow_type: &str, options: WorkerOptions) -> String {
    let cache_directory = tempfile::tempdir().expect("create cache directory");
    let cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(cache_directory.path(), 1024 * 1024, 100)
                .expect("valid cache config"),
        )
        .expect("open cache"),
    );
    let result = Worker::try_new(
        Registry::new()
            .register(FlowDefinitionUploadWorkflow::new(flow_type))
            .expect("register Flow"),
        Arc::clone(&cache),
        options,
    );
    cache.close().expect("close cache");
    let Err(SdkError::FlowDefinition { message }) = result else {
        panic!("invalid Flow definition configuration must fail Worker construction");
    };
    message
}

fn old_schema_definition(flow_type: &str) -> String {
    format!("{{\"schemaVersion\": \"1.0\", \"flow\": {{\"name\": \"{flow_type}\"}}}}")
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_worker_processes_tasks_when_server_rejects_upload() {
    let rejected_flow_type = unique_flow_type("Rejected");
    let workflow = FlowDefinitionUploadWorkflow::new(&rejected_flow_type);

    let environment = DexDevTestEnvironment::start_with_worker_options(
        Registry::new().register(FlowDefinitionUploadWorkflow::new(&rejected_flow_type)),
        WorkerOptions::new().flow_definitions([FlowDefinitionDocument::new(
            rejected_flow_type.as_str(),
            definition(&rejected_flow_type, "rejected.rs", ""),
            SystemTime::now() + Duration::from_secs(3 * 24 * 60 * 60),
        )]),
    );

    assert_flow_completes(&environment, &workflow);
    assert!(get_flow_definitions(&[&rejected_flow_type]).is_empty());
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_invalid_in_memory_document_fails_worker_and_client_before_any_request() {
    let invalid_flow_type = unique_flow_type("InMemoryInvalid");
    let old_schema = FlowDefinitionDocument::new(
        invalid_flow_type.as_str(),
        old_schema_definition(&invalid_flow_type),
        unix_time(1_793_491_200),
    );
    let worker_message = worker_configuration_error(
        &invalid_flow_type,
        WorkerOptions::new().flow_definitions([old_schema.clone()]),
    );
    assert!(worker_message.contains("schemaVersion"), "{worker_message}");

    let environment = DexDevTestEnvironment::start(Registry::new().register(
        FlowDefinitionUploadWorkflow::new(&unique_flow_type("InMemoryInvalidWorker")),
    ));
    let result = environment
        .client
        .upload_flow_definitions([old_schema], false);
    assert!(
        matches!(result, Err(SdkError::InvalidArgument { .. })),
        "{result:?}"
    );
    assert!(get_flow_definitions(&[&invalid_flow_type]).is_empty());
}

#[test]
#[ignore = "requires dexcli dev"]
fn test_client_upload_reports_each_outcome() {
    let client_flow_type = unique_flow_type("Client");
    let first = definition(&client_flow_type, "client_flow.rs", "").into_bytes();
    let second = definition(&client_flow_type, "client_flow_v2.rs", "").into_bytes();
    let first_generated_at = unix_time(1_793_664_000);
    let older_generated_at = unix_time(1_793_577_600);
    let environment = DexDevTestEnvironment::start(Registry::new().register(
        FlowDefinitionUploadWorkflow::new(&unique_flow_type("ClientWorker")),
    ));
    let upload_one = |content: &[u8], generated_at: SystemTime, force: bool| {
        let mut results = environment
            .client
            .upload_flow_definitions(
                [FlowDefinitionDocument::new(
                    client_flow_type.as_str(),
                    content,
                    generated_at,
                )],
                force,
            )
            .expect("upload Flow definition");
        assert_eq!(1, results.len());
        results.remove(0)
    };

    let created: FlowDefinitionUploadResult = upload_one(&first, first_generated_at, false);
    assert_eq!(FlowDefinitionUploadOutcome::Created, created.outcome());
    assert_eq!(client_flow_type, created.flow_type());
    assert_eq!(sha256_hex(&first), created.stored_digest());
    assert_eq!(Some(first_generated_at), created.stored_generated_at());

    assert_eq!(
        FlowDefinitionUploadOutcome::Unchanged,
        upload_one(&first, first_generated_at, false).outcome()
    );

    let kept = upload_one(&second, older_generated_at, false);
    assert_eq!(FlowDefinitionUploadOutcome::KeptExisting, kept.outcome());
    assert_eq!(sha256_hex(&first), kept.stored_digest());
    assert_eq!(Some(first_generated_at), kept.stored_generated_at());

    let replaced = upload_one(&second, older_generated_at, true);
    assert_eq!(FlowDefinitionUploadOutcome::Replaced, replaced.outcome());
    assert_eq!(sha256_hex(&second), replaced.stored_digest());
    assert_eq!(Some(older_generated_at), replaced.stored_generated_at());
}

fn assert_flow_completes(
    environment: &DexDevTestEnvironment,
    workflow: &FlowDefinitionUploadWorkflow,
) {
    let flow_id = flow_id("flow-definition-upload");
    environment
        .client
        .start_flow(workflow, &flow_id, 41)
        .expect("start Flow definition upload Flow");
    let output: i32 = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .and_then(|result| result.single_output())
        .expect("Flow definition upload Flow completes");
    assert_eq!(42, output);
}

fn get_flow_definitions(flow_types: &[&str]) -> BTreeMap<String, FlowDefinition> {
    let runtime = Runtime::new().expect("create FlowService runtime");
    runtime.block_on(async {
        let mut client = FlowServiceClient::connect(format!("http://{}", server_address()))
            .await
            .expect("connect FlowService");
        client
            .get_flow_definitions(GetFlowDefinitionsRequest {
                flow_types: flow_types.iter().map(ToString::to_string).collect(),
            })
            .await
            .expect("GetFlowDefinitions")
            .into_inner()
            .definitions
            .into_iter()
            .map(|definition| {
                let flow_type = definition
                    .info
                    .as_ref()
                    .expect("Flow definition info")
                    .flow_type
                    .clone();
                (flow_type, definition)
            })
            .collect()
    })
}

fn generated_at_seconds(definition: &FlowDefinition) -> i64 {
    definition
        .info
        .as_ref()
        .and_then(|info| info.generated_at)
        .expect("Flow definition generated_at")
        .seconds
}

fn definition(flow_type: &str, source_path: &str, extra_fields: &str) -> String {
    format!(
        "{{\n  \"schemaVersion\": \"2.0\", {extra_fields}\"source\": {{\"language\": \"rust\", \"path\": \"{source_path}\"}},\n  \"flow\": {{\"name\": \"{flow_type}\", \"weight\": 1.50}},\n  \"nodes\": [], \"edges\": []\n}}\n"
    )
}

fn unique_flow_type(purpose: &str) -> String {
    format!(
        "FlowDefinitionUpload{purpose}{}",
        uuid::Uuid::new_v4().simple()
    )
}

fn unix_time(seconds: u64) -> SystemTime {
    SystemTime::UNIX_EPOCH + Duration::from_secs(seconds)
}

fn sha256_hex(content: &[u8]) -> String {
    Sha256::digest(content)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
