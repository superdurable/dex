// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#[test]
fn flow_start_input_is_checked_at_compile_time() {
    trybuild::TestCases::new().compile_fail("tests/compile_fail/wrong_flow_input.rs");
}

#[test]
fn get_attributes_reads_are_checked_at_compile_time() {
    let cases = trybuild::TestCases::new();
    cases.compile_fail("tests/compile_fail/get_attributes_rejects_map_load.rs");
    cases.compile_fail("tests/compile_fail/get_attributes_rejects_map_definition.rs");
    cases.compile_fail("tests/compile_fail/get_attributes_rejects_empty_reads.rs");
}

#[test]
fn get_attributes_tuple_reads_compile() {
    static ORDER_STATUS: std::sync::LazyLock<dex_sdk::Attribute<String>> =
        std::sync::LazyLock::new(|| dex_sdk::Attribute::new("order-status"));
    static NOTES: std::sync::LazyLock<dex_sdk::AttributeMap<u32>> =
        std::sync::LazyLock::new(|| dex_sdk::AttributeMap::new("notes"));

    type TwelveReadValues = (
        Option<String>,
        Option<u32>,
        Option<String>,
        Option<u32>,
        Option<String>,
        Option<u32>,
        Option<String>,
        Option<u32>,
        Option<String>,
        Option<u32>,
        Option<String>,
        Option<u32>,
    );

    fn calls_compile(client: &dex_sdk::Client) -> dex_sdk::SdkResult<()> {
        let (_status, _note): (Option<String>, Option<u32>) =
            client.get_attributes("flow-1", (&ORDER_STATUS, &NOTES.instance("n1")))?;
        let is_paid = dex_sdk::Attribute::<bool>::new("is-paid");
        let instance: dex_sdk::AttributeMapInstance<u32> = NOTES.instance("n2");
        assert_eq!("n2", instance.instance());
        let (_is_paid,): (Option<bool>,) = client.get_attributes("flow-1", (&is_paid,))?;
        let (_owned_note,): (Option<u32>,) = client.get_attributes("flow-1", (instance,))?;
        let _twelve_reads: TwelveReadValues = client.get_attributes(
            "flow-1",
            (
                &ORDER_STATUS,
                &NOTES.instance("1"),
                &ORDER_STATUS,
                &NOTES.instance("2"),
                &ORDER_STATUS,
                &NOTES.instance("3"),
                &ORDER_STATUS,
                &NOTES.instance("4"),
                &ORDER_STATUS,
                &NOTES.instance("5"),
                &ORDER_STATUS,
                &NOTES.instance("6"),
            ),
        )?;
        Ok(())
    }

    let _calls_compile = calls_compile;
}

#[test]
fn attribute_store_sync_builders_compile() {
    let _attribute = dex_sdk::Attribute::<String>::new("email").sync_to_attribute_store();
    let _attribute_map =
        dex_sdk::AttributeMap::<String>::new("email_by_tenant").sync_to_attribute_store();
    let _config = dex_sdk::FlowConfig::new()
        .attribute_store_names(vec!["profiles".to_owned(), "audit".to_owned()]);
    let _disabled = dex_sdk::FlowConfig::new().attribute_store_names(vec![]);
}

#[test]
fn stream_definitions_and_client_calls_compile() {
    let stream = dex_sdk::Stream::<String>::new("thinking", 1_048_576);
    let _schema = dex_sdk::PersistenceSchema::new().stream(&stream);

    fn calls_compile(
        client: &dex_sdk::Client,
        context: &mut dex_sdk::Context,
        stream: &dex_sdk::Stream<String>,
    ) -> dex_sdk::SdkResult<()> {
        let _heartbeat: dex_sdk::HandlerResult<()> = context.record_heartbeat();
        let _heartbeat_value: dex_sdk::HandlerResult<()> =
            context.record_heartbeat_value(Some("checkpoint".to_owned()));
        let _last_heartbeat: dex_sdk::HandlerResult<Option<Option<String>>> =
            context.last_heartbeat_value::<Option<String>>();
        let _stream_write: dex_sdk::HandlerResult<()> =
            stream.write(context, "checking".to_owned());
        let buffered: dex_sdk::HandlerResult<dex_sdk::BufferedTextStream> = stream
            .buffered_text_with_options(
                context,
                dex_sdk::BufferedTextStreamOptions::new(
                    std::time::Duration::from_millis(500),
                    8 * 1024,
                ),
            );
        if let Ok(buffered) = buffered {
            let _buffered_write: dex_sdk::HandlerResult<()> = buffered.write("thinking ");
        }
        client.write_stream("flow-1", stream, "client#source", "starting".to_owned())?;
        let message = client.read_stream("flow-1", stream, "")?;
        let _source: &str = &message.source;
        let _next = client.read_stream_with_timeout(
            "flow-1",
            stream,
            &message.resume_token,
            std::time::Duration::from_secs(2),
        )?;
        let page: dex_sdk::StreamMessagesPage<String> =
            client.list_stream_messages("flow-1", stream, 100, "")?;
        let _messages: &[dex_sdk::StreamMessage<String>] = &page.messages;
        let _next_page_token: &str = &page.next_page_token;
        Ok(())
    }

    let _ = calls_compile;
}

#[test]
fn rpc_invoke_options_builders_and_client_calls_compile() {
    let profiles = dex_sdk::AttributeMap::<String>::new("profiles");
    let queued_commands = dex_sdk::ChannelMap::<String>::new("queued_commands");
    let _options = dex_sdk::RpcInvokeOptions::new()
        .lock_attribute_map_instance(profiles.lock("partition-007"))
        .load_attribute_map_instance(profiles.load("partition-007"))
        .load_channel_map_instance(queued_commands.load_messages("partition-007"));

    fn calls_compile(
        client: &dex_sdk::Client,
        options: dex_sdk::RpcInvokeOptions,
    ) -> dex_sdk::SdkResult<()> {
        const UPDATE: dex_sdk::Rpc<String, ()> = dex_sdk::Rpc::new("update");
        const GET: dex_sdk::Rpc<(), String> = dex_sdk::Rpc::new("get");
        client.invoke_rpc_with_options("flow-1", UPDATE, "value".to_owned(), options.clone())?;
        let _: String = client.invoke_rpc_without_input_with_options("flow-1", GET, options)?;
        Ok(())
    }

    let _ = calls_compile;
}

#[test]
fn pull_worker_options_compile() {
    let _start_options = dex_sdk::StartFlowOptions::new()
        .config_override(dex_sdk::FlowConfig::new().step_durability(dex_sdk::StepDurability::Sync));
    let _sub_flow_options = dex_sdk::SubFlowOptions::new()
        .config_override(dex_sdk::FlowConfig::new().step_durability(dex_sdk::StepDurability::Sync));
    let _worker_options = dex_sdk::WorkerOptions::new().pull(
        dex_sdk::PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .idle_streams_per_queue(2)
            .app_worker_id("rust-worker-1")
            .stop_drain_timeout(std::time::Duration::from_secs(15)),
    );
}

#[test]
fn credential_options_compile() {
    use std::sync::Arc;

    struct TestCredential;
    impl dex_sdk::Credential for TestCredential {
        fn token(&self) -> Result<String, dex_sdk::CredentialError> {
            Ok("token".to_string())
        }
    }
    let credential: Arc<dyn dex_sdk::Credential> = Arc::new(TestCredential);
    let _client_options = dex_sdk::ClientOptions::new().credential(credential.clone());
    let _worker_options = dex_sdk::WorkerOptions::new().credential(credential);
}
