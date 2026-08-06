// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::net::SocketAddr;
use std::sync::Mutex;

use dex_core::{
    BlobCache, BlobCacheConfig, FlowSpec, InvocationFailure, InvocationKind, InvocationResult,
    PersistenceKind, PersistenceSpec, Registry, RpcSpec, StepSpec, WorkerConfig, WorkerCore,
};
use dex_runtime::{WorkerService, WorkerServiceServer};
use jni::JNIEnv;
use jni::objects::{JByteArray, JClass, JString};
use jni::sys::{JNI_FALSE, jboolean, jbyteArray, jint, jlong};
use serde::Deserialize;
use tokio::runtime::{Builder, Runtime};
use tokio::sync::oneshot;
use tonic::transport::Server;

struct NativeWorker {
    runtime: Runtime,
    core: WorkerCore,
    service: WorkerService,
    shutdown: Mutex<Option<oneshot::Sender<()>>>,
}

#[derive(Deserialize)]
struct RegistryDto {
    flows: Vec<FlowDto>,
}

#[derive(Deserialize)]
struct FlowDto {
    name: String,
    steps: Vec<StepDto>,
    rpcs: Vec<String>,
    persistence: Vec<PersistenceDto>,
}

#[derive(Deserialize)]
struct StepDto {
    name: String,
    starting: bool,
}

#[derive(Deserialize)]
struct PersistenceDto {
    name: String,
    kind: String,
}

impl NativeWorker {
    fn new(registry_json: &str, queue_capacity: usize) -> Result<Self, String> {
        let registry = parse_registry(registry_json)?;
        let config = WorkerConfig::new(queue_capacity).map_err(|error| error.to_string())?;
        let core = WorkerCore::new(config);
        let service = WorkerService::new(registry, core.clone());
        let runtime = Builder::new_multi_thread()
            .enable_all()
            .thread_name("dex-rust-core")
            .build()
            .map_err(|error| format!("create Tokio runtime: {error}"))?;
        Ok(Self {
            runtime,
            core,
            service,
            shutdown: Mutex::new(None),
        })
    }

    fn serve(&self, bind_address: &str) -> Result<(), String> {
        let address = parse_bind_address(bind_address)?;
        let (shutdown_sender, shutdown_receiver) = oneshot::channel();
        let mut shutdown = self.shutdown.lock().expect("JNI shutdown mutex poisoned");
        if shutdown.replace(shutdown_sender).is_some() {
            return Err("Worker is already running".into());
        }
        drop(shutdown);

        let result = self.runtime.block_on(
            Server::builder()
                .add_service(WorkerServiceServer::new(self.service.clone()))
                .serve_with_shutdown(address, async {
                    let _ = shutdown_receiver.await;
                }),
        );
        self.shutdown
            .lock()
            .expect("JNI shutdown mutex poisoned")
            .take();
        result.map_err(|error| format!("serve WorkerService on {address}: {error}"))
    }

    fn stop(&self) -> Result<(), String> {
        if let Some(sender) = self
            .shutdown
            .lock()
            .expect("JNI shutdown mutex poisoned")
            .take()
        {
            let _ = sender.send(());
        }
        self.core
            .initiate_shutdown()
            .map_err(|error| error.to_string())
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_create(
    mut env: JNIEnv,
    _class: JClass,
    registry_json: JString,
    queue_capacity: jint,
) -> jlong {
    let result = (|| {
        let json: String = env
            .get_string(&registry_json)
            .map_err(|error| error.to_string())?
            .into();
        let capacity = usize::try_from(queue_capacity)
            .map_err(|_| "queue capacity must be positive".to_string())?;
        NativeWorker::new(&json, capacity).map(|worker| Box::into_raw(Box::new(worker)) as jlong)
    })();
    jni_result(&mut env, result, 0)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_serve(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
    bind_address: JString,
) {
    let result = (|| {
        let worker = native_worker(handle)?;
        let address: String = env
            .get_string(&bind_address)
            .map_err(|error| error.to_string())?
            .into();
        worker.serve(&address)
    })();
    jni_result(&mut env, result, ());
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_poll(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
) -> jbyteArray {
    let result = (|| {
        let worker = native_worker(handle)?;
        let invocation = worker
            .runtime
            .block_on(worker.core.poll_invocation())
            .map_err(|error| error.to_string())?;
        let mut envelope = Vec::with_capacity(13 + invocation.request().len());
        envelope.extend_from_slice(&invocation.protocol_version().to_le_bytes());
        envelope.extend_from_slice(&invocation.id().get().to_le_bytes());
        envelope.push(invocation_kind(invocation.kind()));
        envelope.extend_from_slice(invocation.request());
        env.byte_array_from_slice(&envelope)
            .map(|array| array.into_raw())
            .map_err(|error| error.to_string())
    })();
    jni_result(&mut env, result, std::ptr::null_mut())
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_complete(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
    protocol_version: jint,
    invocation_id: jlong,
    success: jboolean,
    payload: JByteArray,
    error_type: JString,
    error_message: JString,
) {
    let result = (|| {
        let worker = native_worker(handle)?;
        let version = u32::try_from(protocol_version)
            .map_err(|_| "protocol version must be non-negative".to_string())?;
        let id = u64::try_from(invocation_id)
            .map_err(|_| "invocation ID must be positive".to_string())?;
        let bytes = env
            .convert_byte_array(&payload)
            .map_err(|error| error.to_string())?;
        let result = if success != JNI_FALSE {
            InvocationResult::Success(bytes)
        } else {
            let error_type: String = env
                .get_string(&error_type)
                .map_err(|error| error.to_string())?
                .into();
            let message: String = env
                .get_string(&error_message)
                .map_err(|error| error.to_string())?
                .into();
            InvocationResult::Failure(InvocationFailure::new(error_type, message, bytes))
        };
        worker
            .core
            .complete_invocation(
                version,
                dex_core::InvocationId::from_u64(id)
                    .ok_or_else(|| "invocation ID must be positive".to_string())?,
                result,
            )
            .map_err(|error| error.to_string())
    })();
    jni_result(&mut env, result, ());
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_stop(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
) {
    let result = native_worker(handle).and_then(NativeWorker::stop);
    jni_result(&mut env, result, ());
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_destroy(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
) {
    let result = if handle == 0 {
        Err("native Worker handle is closed".into())
    } else {
        // SAFETY: create returns this Box exactly once; Java closes it once.
        let worker = unsafe { Box::from_raw(handle as *mut NativeWorker) };
        worker.stop()
    };
    jni_result(&mut env, result, ());
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_cacheOpen(
    mut env: JNIEnv,
    _class: JClass,
    directory: JString,
    max_bytes: jlong,
    frequency_counters: jlong,
) -> jlong {
    let result = (|| {
        let directory: String = env
            .get_string(&directory)
            .map_err(|error| error.to_string())?
            .into();
        let config = BlobCacheConfig::new(directory, max_bytes, frequency_counters)
            .map_err(|error| error.to_string())?;
        BlobCache::open(config)
            .map(|cache| Box::into_raw(Box::new(cache)) as jlong)
            .map_err(|error| error.to_string())
    })();
    jni_result(&mut env, result, 0)
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_cacheGet(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
    blob_id: JString,
) -> jbyteArray {
    let result = (|| {
        let cache = native_cache(handle)?;
        let blob_id: String = env
            .get_string(&blob_id)
            .map_err(|error| error.to_string())?
            .into();
        cache
            .get(&blob_id)
            .map_err(|error| error.to_string())?
            .map(|payload| {
                env.byte_array_from_slice(&payload)
                    .map(|array| array.into_raw())
                    .map_err(|error| error.to_string())
            })
            .transpose()
    })();
    match result {
        Ok(Some(payload)) => payload,
        Ok(None) => std::ptr::null_mut(),
        Err(message) => {
            let _ = env.throw_new("java/lang/IllegalStateException", message);
            std::ptr::null_mut()
        }
    }
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_cachePut(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
    blob_id: JString,
    payload: JByteArray,
) -> jboolean {
    let result = (|| {
        let cache = native_cache(handle)?;
        let blob_id: String = env
            .get_string(&blob_id)
            .map_err(|error| error.to_string())?
            .into();
        let payload = env
            .convert_byte_array(&payload)
            .map_err(|error| error.to_string())?;
        cache
            .put(&blob_id, &payload)
            .map_err(|error| error.to_string())
    })();
    jni_result(&mut env, result, false) as jboolean
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_cacheDelete(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
    blob_id: JString,
) {
    let result = (|| {
        let cache = native_cache(handle)?;
        let blob_id: String = env
            .get_string(&blob_id)
            .map_err(|error| error.to_string())?
            .into();
        cache.delete(&blob_id).map_err(|error| error.to_string())
    })();
    jni_result(&mut env, result, ());
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_cacheDeleteAll(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
) {
    let result = native_cache(handle)
        .and_then(|cache| cache.delete_all().map_err(|error| error.to_string()));
    jni_result(&mut env, result, ());
}

#[unsafe(no_mangle)]
pub extern "system" fn Java_io_superdurable_dex_NativeCore_cacheClose(
    mut env: JNIEnv,
    _class: JClass,
    handle: jlong,
) {
    let result = if handle == 0 {
        Err("native BlobCache handle is closed".into())
    } else {
        // SAFETY: cacheOpen returns this Box exactly once; Java closes it once.
        let cache = unsafe { Box::from_raw(handle as *mut BlobCache) };
        cache.close().map_err(|error| error.to_string())
    };
    jni_result(&mut env, result, ());
}

fn parse_registry(json: &str) -> Result<Registry, String> {
    let dto: RegistryDto = serde_json::from_str(json)
        .map_err(|error| format!("decode Registry specification: {error}"))?;
    let flows = dto
        .flows
        .into_iter()
        .map(|flow| {
            let steps = flow
                .steps
                .into_iter()
                .map(|step| {
                    if step.starting {
                        StepSpec::starting(step.name)
                    } else {
                        StepSpec::non_starting(step.name)
                    }
                })
                .collect();
            let rpcs = flow.rpcs.into_iter().map(RpcSpec::new).collect();
            let persistence = flow
                .persistence
                .into_iter()
                .map(|definition| {
                    persistence_kind(&definition.kind)
                        .map(|kind| PersistenceSpec::new(definition.name, kind))
                })
                .collect::<Result<Vec<_>, _>>()?;
            Ok(FlowSpec::new(flow.name, steps, rpcs, persistence))
        })
        .collect::<Result<Vec<_>, String>>()?;
    Registry::new(flows).map_err(|error| error.to_string())
}

fn persistence_kind(kind: &str) -> Result<PersistenceKind, String> {
    match kind {
        "attribute" => Ok(PersistenceKind::Attribute),
        "attributeMap" => Ok(PersistenceKind::AttributeMap),
        "channel" => Ok(PersistenceKind::Channel),
        "channelMap" => Ok(PersistenceKind::ChannelMap),
        _ => Err(format!("unknown persistence kind {kind:?}")),
    }
}

fn parse_bind_address(address: &str) -> Result<SocketAddr, String> {
    let normalized = if address.starts_with(':') {
        format!("0.0.0.0{address}")
    } else {
        address.to_string()
    };
    normalized
        .parse()
        .map_err(|error| format!("invalid Worker bind address {address:?}: {error}"))
}

fn invocation_kind(kind: InvocationKind) -> u8 {
    match kind {
        InvocationKind::WaitFor => 1,
        InvocationKind::Execute => 2,
        InvocationKind::WorkerRpc => 3,
    }
}

fn native_worker(handle: jlong) -> Result<&'static NativeWorker, String> {
    if handle == 0 {
        return Err("native Worker handle is closed".into());
    }
    // SAFETY: Java owns the handle until destroy after all Worker threads stop.
    unsafe {
        (handle as *const NativeWorker)
            .as_ref()
            .ok_or_else(|| "native Worker handle is invalid".into())
    }
}

fn native_cache(handle: jlong) -> Result<&'static BlobCache, String> {
    if handle == 0 {
        return Err("native BlobCache handle is closed".into());
    }
    // SAFETY: Java owns the handle until cacheClose after all calls finish.
    unsafe {
        (handle as *const BlobCache)
            .as_ref()
            .ok_or_else(|| "native BlobCache handle is invalid".into())
    }
}

fn jni_result<T: Copy>(env: &mut JNIEnv, result: Result<T, String>, fallback: T) -> T {
    match result {
        Ok(value) => value,
        Err(message) => {
            let _ = env.throw_new("java/lang/IllegalStateException", message);
            fallback
        }
    }
}
