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
        let _stream_write: dex_sdk::HandlerResult<()> =
            stream.write(context, "checking".to_owned());
        client.write_stream("flow-1", stream, "client-1", "starting".to_owned())?;
        let message = client.read_stream("flow-1", stream, "")?;
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
