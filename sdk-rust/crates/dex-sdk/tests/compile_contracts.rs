// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

#[test]
fn flow_start_input_is_checked_at_compile_time() {
    trybuild::TestCases::new().compile_fail("tests/compile_fail/wrong_flow_input.rs");
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
        Ok(())
    }

    let _ = calls_compile;
}
