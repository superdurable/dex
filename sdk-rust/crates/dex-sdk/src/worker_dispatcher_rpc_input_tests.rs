// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A Worker serves RPC requests whose input is unset, as released TypeScript Clients send.

use std::sync::Arc;

use dex_blob_cache::{BlobCache, BlobCacheConfig};
use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{Context as ProtoContext, InvokeWorkerRpcRequest, Kv, Value as ProtoValue};
use tonic::transport::Endpoint;

use super::WorkerDispatcher;
use crate::context::InvocationCancellation;
use crate::credential::{CredentialChannel, CredentialInterceptor};
use crate::server_protocol::NegotiatedServerProtocolVersion;
use crate::value_hydrator::ValueHydrator;
use crate::value_mapper;
use crate::{
    Attribute, Context, Flow, HandlerResult, PersistenceSchema, Registry, Rpc, RpcList, RpcResult,
};

struct NoteFlow {
    note: Attribute<String>,
}

impl NoteFlow {
    const GET_NOTE: Rpc<(), String> = Rpc::new("get_note");
    const SET_NOTE: Rpc<String, String> = Rpc::new("set_note");
    const CLEAR_NOTE: Rpc<(), ()> = Rpc::new("clear_note");

    fn get_note(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new(self.note.get(context)?.unwrap_or_default()))
    }

    fn set_note(&self, context: &mut Context, note: String) -> HandlerResult<RpcResult<String>> {
        self.note.set(context, note.clone())?;
        Ok(RpcResult::new(note))
    }

    fn clear_note(&self, context: &mut Context) -> HandlerResult<()> {
        self.note.set(context, String::new())
    }
}

impl Flow for NoteFlow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&self.note)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(Self::GET_NOTE, Self::get_note)
            .function(Self::SET_NOTE, Self::set_note)
            .procedure_without_input(Self::CLEAR_NOTE, Self::clear_note)
    }
}

fn dispatcher() -> (WorkerDispatcher, tempfile::TempDir) {
    let registry = Registry::new()
        .register(NoteFlow {
            note: Attribute::new("note"),
        })
        .expect("register NoteFlow");
    let directory = tempfile::tempdir().expect("cache directory");
    let cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(directory.path(), 1 << 20, 0).expect("cache configuration"),
        )
        .expect("open cache"),
    );
    // No request here holds a blob, so the hydrator never calls the service.
    let channel = Endpoint::from_static("http://127.0.0.1:1").connect_lazy();
    let service = FlowServiceClient::new(CredentialChannel::new(
        channel,
        CredentialInterceptor::new(None),
    ));
    let dispatcher = WorkerDispatcher::new(
        registry,
        ValueHydrator::new(service, cache),
        NegotiatedServerProtocolVersion::default(),
    );
    (dispatcher, directory)
}

fn request(rpc_name: &str, input: Option<ProtoValue>) -> InvokeWorkerRpcRequest {
    InvokeWorkerRpcRequest {
        context: Some(ProtoContext {
            flow_id: "note-flow".to_string(),
            run_id: "run".to_string(),
            ..Default::default()
        }),
        flow_type: "NoteFlow".to_string(),
        rpc_name: rpc_name.to_string(),
        input,
        attributes: vec![Kv {
            key: "note".to_string(),
            value: Some(value_mapper::encode(&"saved".to_string()).expect("encode note")),
        }],
        ..Default::default()
    }
}

fn string_output(output: Option<ProtoValue>) -> String {
    value_mapper::decode(&output.expect("RPC output")).expect("decode RPC output")
}

#[tokio::test]
async fn hydrator_leaves_an_unset_rpc_input_unset() {
    let (dispatcher, _directory) = dispatcher();
    let unset = request("get_note", None);
    let hydrated = dispatcher
        .hydrate_rpc(unset.clone())
        .await
        .expect("hydrate RPC request");
    assert_eq!(None, hydrated.input);
    assert_eq!(unset.attributes, hydrated.attributes);
}

#[tokio::test]
async fn worker_serves_an_input_free_rpc_with_unset_input() {
    let (dispatcher, _directory) = dispatcher();
    let response = dispatcher
        .invoke_rpc_with_cancellation(request("get_note", None), InvocationCancellation::new())
        .await
        .expect("invoke get_note");
    assert_eq!("saved", string_output(response.output));
}

#[tokio::test]
async fn worker_serves_an_input_free_procedure_with_unset_input() {
    let (dispatcher, _directory) = dispatcher();
    let response = dispatcher
        .invoke_rpc_with_cancellation(request("clear_note", None), InvocationCancellation::new())
        .await
        .expect("invoke clear_note");
    assert_eq!(1, response.upsert_attributes.len());
    assert_eq!("note", response.upsert_attributes[0].key);
}

#[tokio::test]
async fn worker_serves_an_input_free_rpc_with_null_input() {
    let (dispatcher, _directory) = dispatcher();
    let null = value_mapper::encode(&()).expect("encode null");
    let response = dispatcher
        .invoke_rpc_with_cancellation(
            request("get_note", Some(null)),
            InvocationCancellation::new(),
        )
        .await
        .expect("invoke get_note");
    assert_eq!("saved", string_output(response.output));
}

#[tokio::test]
async fn worker_rejects_an_input_bearing_rpc_with_unset_input() {
    let (dispatcher, _directory) = dispatcher();
    let error = dispatcher
        .invoke_rpc_with_cancellation(request("set_note", None), InvocationCancellation::new())
        .await
        .expect_err("set_note without input must fail");
    assert!(
        error
            .to_string()
            .contains("RPC 'set_note' request is missing its input"),
        "{error}"
    );
}

#[tokio::test]
async fn worker_decodes_an_input_bearing_rpc_input() {
    let (dispatcher, _directory) = dispatcher();
    let input = value_mapper::encode(&"changed".to_string()).expect("encode input");
    let response = dispatcher
        .invoke_rpc_with_cancellation(
            request("set_note", Some(input)),
            InvocationCancellation::new(),
        )
        .await
        .expect("invoke set_note");
    assert_eq!("changed", string_output(response.output));
}
