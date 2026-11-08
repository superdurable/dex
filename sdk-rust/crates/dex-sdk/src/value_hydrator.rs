// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::HashMap;
use std::str;

use dex_blob_cache::BlobCache;
use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{EncodedObject, LoadBlobRequestEntry, LoadBlobsRequest, Value, value};
use prost::Message;

use crate::credential::CredentialChannel;

use crate::sdk_error::FlowTargetRequirement;
use crate::{SdkError, SdkResult};

// The server rejects more than 1000 entries while history offload is on.
const MAX_LOAD_BLOBS_ENTRIES: usize = 1000;

#[derive(Clone)]
pub(crate) struct ValueHydrator {
    service: FlowServiceClient<CredentialChannel>,
    cache: std::sync::Arc<BlobCache>,
}

impl ValueHydrator {
    pub(crate) fn new(
        service: FlowServiceClient<CredentialChannel>,
        cache: std::sync::Arc<BlobCache>,
    ) -> Self {
        Self { service, cache }
    }

    pub(crate) async fn hydrate(&self, flow_id: &str, value: Value) -> SdkResult<Value> {
        let mut values = self.hydrate_all(flow_id, vec![value]).await?;
        Ok(values.remove(0))
    }

    pub(crate) async fn hydrate_all(
        &self,
        flow_id: &str,
        values: Vec<Value>,
    ) -> SdkResult<Vec<Value>> {
        self.hydrate_for_flows(
            values
                .into_iter()
                .map(|value| (flow_id.to_string(), value))
                .collect(),
        )
        .await
    }

    pub(crate) async fn hydrate_for_flows(
        &self,
        flow_values: Vec<(String, Value)>,
    ) -> SdkResult<Vec<Value>> {
        let mut hydrated = flow_values
            .iter()
            .map(|(_, value)| value.clone())
            .collect::<Vec<_>>();
        let mut pending: HashMap<BlobKey, Vec<usize>> = HashMap::new();
        for (index, (flow_id, value)) in flow_values.iter().enumerate() {
            if flow_id.is_empty() {
                return Err(mapping_error("Flow ID is required"));
            }
            if let Some(key) = BlobKey::from_value(flow_id, value)? {
                pending.entry(key).or_default().push(index);
            } else {
                validate_concrete(value)?;
            }
        }

        let mut resolved = HashMap::new();
        let mut misses = Vec::new();
        for key in pending.keys() {
            match self.read_cache(key) {
                Ok(Some(value)) => {
                    resolved.insert(key.clone(), value);
                }
                Ok(None) | Err(_) => misses.push(key.clone()),
            }
        }
        misses.sort_by_key(|key| pending[key][0]);
        for batch in misses.chunks(MAX_LOAD_BLOBS_ENTRIES) {
            let request = LoadBlobsRequest {
                entries: batch
                    .iter()
                    .map(|key| LoadBlobRequestEntry {
                        flow_id: key.flow_id.clone(),
                        blob_value: Some(key.request_value()),
                    })
                    .collect(),
            };
            let response = self
                .service
                .clone()
                .load_blobs(request)
                .await
                .map_err(|status| {
                    SdkError::from_status(status, "load_blobs", None, FlowTargetRequirement::None)
                })?
                .into_inner();
            for key in batch {
                let value = response.values.get(&key.id).cloned().ok_or_else(|| {
                    SdkError::ValueMapping {
                        message: format!("LoadBlobs omitted blob {}", key.id),
                    }
                })?;
                key.validate_hydrated(&value)?;
                if let Err(error) = self.write_cache(key, &value) {
                    tracing::warn!(%error, blob_id = %key.id, "cache hydrated blob");
                }
                resolved.insert(key.clone(), value);
            }
        }
        for (key, indexes) in pending {
            let value = resolved
                .remove(&key)
                .ok_or_else(|| SdkError::ValueMapping {
                    message: format!("blob {} was not hydrated", key.id),
                })?;
            for index in indexes {
                hydrated[index] = value.clone();
            }
        }
        Ok(hydrated)
    }

    fn read_cache(&self, key: &BlobKey) -> SdkResult<Option<Value>> {
        if key.is_hard_delete() {
            return Ok(None);
        }
        let payload = self.cache.get(&key.cache_key()).map_err(cache_error)?;
        let Some(payload) = payload else {
            return Ok(None);
        };
        let value = if key.object {
            Value {
                kind: Some(value::Kind::ObjValue(
                    EncodedObject::decode(payload.as_slice()).map_err(mapping_error)?,
                )),
            }
        } else {
            Value {
                kind: Some(value::Kind::StringValue(
                    str::from_utf8(&payload).map_err(mapping_error)?.to_string(),
                )),
            }
        };
        validate_concrete(&value)?;
        Ok(Some(value))
    }

    fn write_cache(&self, key: &BlobKey, value: &Value) -> SdkResult<()> {
        if key.is_hard_delete() {
            return Ok(());
        }
        let payload = match value.kind.as_ref() {
            Some(value::Kind::ObjValue(object)) if key.object => object.encode_to_vec(),
            Some(value::Kind::StringValue(text)) if !key.object => text.as_bytes().to_vec(),
            _ => return Err(mapping_error("hydrated blob has the wrong kind")),
        };
        self.cache
            .put(&key.cache_key(), &payload)
            .map_err(cache_error)?;
        Ok(())
    }
}

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
struct BlobKey {
    flow_id: String,
    id: String,
    object: bool,
}

impl BlobKey {
    fn from_value(flow_id: &str, value: &Value) -> SdkResult<Option<Self>> {
        let key = match value.kind.as_ref() {
            Some(value::Kind::InternalBlobIdForStringValue(id)) => Self {
                flow_id: flow_id.to_string(),
                id: require_blob_id(id)?,
                object: false,
            },
            Some(value::Kind::InternalBlobIdForObjValue(id)) => Self {
                flow_id: flow_id.to_string(),
                id: require_blob_id(id)?,
                object: true,
            },
            Some(_) => return Ok(None),
            None => return Err(mapping_error("Value has no concrete kind")),
        };
        Ok(Some(key))
    }

    fn request_value(&self) -> Value {
        Value {
            kind: Some(if self.object {
                value::Kind::InternalBlobIdForObjValue(self.id.clone())
            } else {
                value::Kind::InternalBlobIdForStringValue(self.id.clone())
            }),
        }
    }

    fn cache_key(&self) -> String {
        format!("{}:{}{}", self.flow_id.len(), self.flow_id, self.id)
    }

    fn is_hard_delete(&self) -> bool {
        self.id.starts_with("hd/") || self.id.starts_with("hd1|")
    }

    fn validate_hydrated(&self, value: &Value) -> SdkResult<()> {
        match value.kind.as_ref() {
            Some(value::Kind::ObjValue(_)) if self.object => validate_concrete(value),
            Some(value::Kind::StringValue(_)) if !self.object => validate_concrete(value),
            _ => Err(mapping_error("blob hydrated to the wrong Value kind")),
        }
    }
}

fn validate_concrete(value: &Value) -> SdkResult<()> {
    match value.kind.as_ref() {
        Some(value::Kind::StringValue(_))
        | Some(value::Kind::IntValue(_))
        | Some(value::Kind::BoolValue(_)) => Ok(()),
        Some(value::Kind::DoubleValue(number)) if number.is_finite() => Ok(()),
        Some(value::Kind::DoubleValue(_)) => {
            Err(mapping_error("non-finite numbers are unsupported"))
        }
        Some(value::Kind::ObjValue(object))
            if object.encoding == "json" || object.encoding == "raw" =>
        {
            Ok(())
        }
        Some(value::Kind::ObjValue(object)) => Err(mapping_error(format!(
            "unsupported object encoding {}",
            object.encoding
        ))),
        Some(value::Kind::InternalBlobIdForStringValue(_))
        | Some(value::Kind::InternalBlobIdForObjValue(_)) => {
            Err(mapping_error("blob-backed Value was not hydrated"))
        }
        Some(value::Kind::NullValue(_)) => Ok(()),
        None => Err(mapping_error("Value has no concrete kind")),
    }
}

fn require_blob_id(id: &str) -> SdkResult<String> {
    if id.is_empty() {
        Err(mapping_error("blob ID is required"))
    } else {
        Ok(id.to_string())
    }
}

fn cache_error(error: impl std::fmt::Display) -> SdkError {
    mapping_error(format!("blob cache: {error}"))
}

fn mapping_error(error: impl std::fmt::Display) -> SdkError {
    SdkError::ValueMapping {
        message: error.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use dex_blob_cache::BlobCacheConfig;
    use tonic::transport::Endpoint;

    use super::*;
    use crate::credential::CredentialInterceptor;

    #[tokio::test]
    async fn rewritten_hard_delete_attribute_bypasses_stale_cache() {
        let directory = tempfile::tempdir().expect("cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(directory.path(), 1 << 20, 0).expect("cache configuration"),
            )
            .expect("open cache"),
        );
        let channel = Endpoint::from_static("http://127.0.0.1:1").connect_lazy();
        let service = FlowServiceClient::new(CredentialChannel::new(
            channel,
            CredentialInterceptor::new(None),
        ));
        let hydrator = ValueHydrator::new(service, Arc::clone(&cache));

        for locator in ["hd/attribute-object", "hd1|run-id|attribute-object"] {
            let key = BlobKey {
                flow_id: "flow-1".to_owned(),
                id: locator.to_owned(),
                object: false,
            };
            cache
                .put(&key.cache_key(), b"original")
                .expect("seed stale cache");
            assert!(hydrator.read_cache(&key).expect("read cache").is_none());
            let updated = Value {
                kind: Some(value::Kind::StringValue("updated".to_owned())),
            };
            hydrator
                .write_cache(&key, &updated)
                .expect("skip cache write");
            assert!(hydrator.read_cache(&key).expect("read cache").is_none());
            assert_eq!(
                cache.get(&key.cache_key()).expect("inspect cache"),
                Some(b"original".to_vec())
            );
        }
        cache.close().expect("close cache");
    }

    #[derive(Clone)]
    struct RecordingBlobService {
        batches: Arc<std::sync::Mutex<Vec<Vec<String>>>>,
    }

    impl tonic::server::NamedService for RecordingBlobService {
        const NAME: &'static str = "dex.FlowService";
    }

    impl<RequestBody> tonic::codegen::Service<tonic::codegen::http::Request<RequestBody>>
        for RecordingBlobService
    where
        RequestBody: tonic::codegen::Body + Send + 'static,
        RequestBody::Error: Into<tonic::codegen::StdError> + Send + 'static,
    {
        type Response = tonic::codegen::http::Response<tonic::body::Body>;
        type Error = std::convert::Infallible;
        type Future = tonic::codegen::BoxFuture<Self::Response, Self::Error>;

        fn poll_ready(
            &mut self,
            _: &mut std::task::Context<'_>,
        ) -> std::task::Poll<Result<(), Self::Error>> {
            std::task::Poll::Ready(Ok(()))
        }

        fn call(&mut self, request: tonic::codegen::http::Request<RequestBody>) -> Self::Future {
            let service = self.clone();
            Box::pin(async move {
                let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
                Ok(grpc.unary(service, request).await)
            })
        }
    }

    impl tonic::server::UnaryService<LoadBlobsRequest> for RecordingBlobService {
        type Response = dex_protocol::dex::LoadBlobsResponse;
        type Future = tonic::codegen::BoxFuture<tonic::Response<Self::Response>, tonic::Status>;

        fn call(&mut self, request: tonic::Request<LoadBlobsRequest>) -> Self::Future {
            let entries = request.into_inner().entries;
            let blob_ids = entries
                .iter()
                .map(
                    |entry| match entry.blob_value.as_ref().unwrap().kind.as_ref().unwrap() {
                        value::Kind::InternalBlobIdForStringValue(blob_id) => blob_id.clone(),
                        unexpected => panic!("unexpected blob kind: {unexpected:?}"),
                    },
                )
                .collect::<Vec<_>>();
            self.batches.lock().unwrap().push(blob_ids.clone());
            Box::pin(async move {
                assert!(blob_ids.len() <= 1000);
                let values = blob_ids
                    .into_iter()
                    .map(|blob_id| {
                        let concrete = Value {
                            kind: Some(value::Kind::StringValue(format!("value-{blob_id}"))),
                        };
                        (blob_id, concrete)
                    })
                    .collect();
                Ok(tonic::Response::new(dex_protocol::dex::LoadBlobsResponse {
                    values,
                }))
            })
        }
    }

    #[tokio::test]
    async fn count_batches_preserve_request_and_result_order() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let batches = Arc::new(std::sync::Mutex::new(Vec::new()));
        let service = RecordingBlobService {
            batches: Arc::clone(&batches),
        };
        let (stop_sender, stop_receiver) = tokio::sync::oneshot::channel();
        let server_task = tokio::spawn(
            tonic::transport::Server::builder()
                .add_service(service)
                .serve_with_incoming_shutdown(
                    tokio_stream::wrappers::TcpListenerStream::new(listener),
                    async {
                        stop_receiver.await.unwrap();
                    },
                ),
        );
        let channel = Endpoint::from_shared(format!("http://{address}"))
            .unwrap()
            .connect()
            .await
            .unwrap();
        let directory = tempfile::tempdir().unwrap();
        let cache = Arc::new(
            BlobCache::open(BlobCacheConfig::new(directory.path(), 1 << 20, 0).unwrap()).unwrap(),
        );
        let hydrator = ValueHydrator::new(
            FlowServiceClient::new(CredentialChannel::new(
                channel,
                CredentialInterceptor::new(None),
            )),
            Arc::clone(&cache),
        );
        let values = (0..2500)
            .map(|index| Value {
                kind: Some(value::Kind::InternalBlobIdForStringValue(format!(
                    "blob-{index}"
                ))),
            })
            .collect();
        let hydrated = hydrator.hydrate_all("flow", values).await.unwrap();
        {
            let batches = batches.lock().unwrap();
            assert_eq!(
                batches.iter().map(Vec::len).collect::<Vec<_>>(),
                vec![1000, 1000, 500]
            );
            assert_eq!(
                batches.iter().flatten().cloned().collect::<Vec<_>>(),
                (0..2500)
                    .map(|index| format!("blob-{index}"))
                    .collect::<Vec<_>>()
            );
        }
        for (index, value) in hydrated.iter().enumerate() {
            assert_eq!(
                value.kind,
                Some(value::Kind::StringValue(format!("value-blob-{index}")))
            );
        }
        cache.close().unwrap();
        stop_sender.send(()).unwrap();
        server_task.await.unwrap().unwrap();
    }
}
