// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard};

use tokio::sync::{Mutex as AsyncMutex, mpsc, oneshot};

use crate::{
    CORE_PROTOCOL_VERSION, CoreError, Invocation, InvocationId, InvocationKind, InvocationResult,
};

type CompletionSender = oneshot::Sender<Result<InvocationResult, CoreError>>;

/// Core worker configuration.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct WorkerConfig {
    queue_capacity: usize,
}

impl WorkerConfig {
    /// Creates bounded queue configuration.
    pub fn new(queue_capacity: usize) -> Result<Self, CoreError> {
        if queue_capacity == 0 {
            return Err(CoreError::InvalidQueueCapacity);
        }
        Ok(Self { queue_capacity })
    }

    /// Returns the maximum queued invocations.
    pub fn queue_capacity(self) -> usize {
        self.queue_capacity
    }
}

/// Language-neutral invocation engine.
#[derive(Clone)]
pub struct WorkerCore {
    inner: Arc<WorkerCoreInner>,
}

struct WorkerCoreInner {
    next_invocation_id: AtomicU64,
    shutdown: AtomicBool,
    sender: Mutex<Option<mpsc::Sender<Invocation>>>,
    receiver: AsyncMutex<mpsc::Receiver<Invocation>>,
    pending: Mutex<HashMap<InvocationId, CompletionSender>>,
}

impl WorkerCore {
    /// Creates a running Core worker.
    pub fn new(config: WorkerConfig) -> Self {
        let (sender, receiver) = mpsc::channel(config.queue_capacity());
        Self {
            inner: Arc::new(WorkerCoreInner {
                next_invocation_id: AtomicU64::new(1),
                shutdown: AtomicBool::new(false),
                sender: Mutex::new(Some(sender)),
                receiver: AsyncMutex::new(receiver),
                pending: Mutex::new(HashMap::new()),
            }),
        }
    }

    /// Dispatches serialized server work.
    pub async fn dispatch(
        &self,
        kind: InvocationKind,
        request: Vec<u8>,
    ) -> Result<InvocationResult, CoreError> {
        if self.is_shutdown() {
            return Err(CoreError::WorkerShutdown);
        }

        let invocation_id = self.next_invocation_id()?;
        let (completion_sender, completion_receiver) = oneshot::channel();
        let previous_sender = lock(&self.inner.pending).insert(invocation_id, completion_sender);
        debug_assert!(previous_sender.is_none());
        let _pending_guard = PendingInvocationGuard::new(self.inner.clone(), invocation_id);

        let sender = lock(&self.inner.sender)
            .clone()
            .ok_or(CoreError::WorkerShutdown)?;
        sender
            .send(Invocation::new(invocation_id, kind, request))
            .await
            .map_err(|_| CoreError::WorkerShutdown)?;

        completion_receiver
            .await
            .map_err(|_| CoreError::CompletionReceiverDropped(invocation_id))?
    }

    /// Polls the next live invocation.
    pub async fn poll_invocation(&self) -> Result<Invocation, CoreError> {
        loop {
            if self.is_shutdown() {
                return Err(CoreError::WorkerShutdown);
            }

            let invocation = self
                .inner
                .receiver
                .lock()
                .await
                .recv()
                .await
                .ok_or(CoreError::WorkerShutdown)?;

            if self.is_shutdown() {
                return Err(CoreError::WorkerShutdown);
            }
            if lock(&self.inner.pending).contains_key(&invocation.id()) {
                return Ok(invocation);
            }
        }
    }

    /// Completes one invocation exactly once.
    pub fn complete_invocation(
        &self,
        protocol_version: u32,
        invocation_id: InvocationId,
        result: InvocationResult,
    ) -> Result<(), CoreError> {
        if protocol_version != CORE_PROTOCOL_VERSION {
            return Err(CoreError::UnsupportedProtocolVersion {
                expected: CORE_PROTOCOL_VERSION,
                actual: protocol_version,
            });
        }
        let completion_sender = lock(&self.inner.pending)
            .remove(&invocation_id)
            .ok_or(CoreError::UnknownInvocation(invocation_id))?;
        completion_sender
            .send(Ok(result))
            .map_err(|_| CoreError::CompletionReceiverDropped(invocation_id))
    }

    /// Stops polling and fails outstanding requests.
    pub fn initiate_shutdown(&self) -> Result<(), CoreError> {
        if self.inner.shutdown.swap(true, Ordering::AcqRel) {
            return Ok(());
        }

        drop(lock(&self.inner.sender).take());
        let mut first_error = None;
        for (invocation_id, completion_sender) in lock(&self.inner.pending).drain() {
            if completion_sender
                .send(Err(CoreError::WorkerShutdown))
                .is_err()
                && first_error.is_none()
            {
                first_error = Some(CoreError::CompletionReceiverDropped(invocation_id));
            }
        }
        first_error.map_or(Ok(()), Err)
    }

    /// Reports whether shutdown started.
    pub fn is_shutdown(&self) -> bool {
        self.inner.shutdown.load(Ordering::Acquire)
    }

    /// Returns unused invocation queue slots.
    pub fn available_queue_capacity(&self) -> usize {
        lock(&self.inner.sender)
            .as_ref()
            .map(mpsc::Sender::capacity)
            .unwrap_or(0)
    }

    fn next_invocation_id(&self) -> Result<InvocationId, CoreError> {
        self.inner
            .next_invocation_id
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |current| {
                current.checked_add(1)
            })
            .map(InvocationId::new)
            .map_err(|_| CoreError::InvocationIdExhausted)
    }
}

struct PendingInvocationGuard {
    inner: Arc<WorkerCoreInner>,
    invocation_id: InvocationId,
}

impl PendingInvocationGuard {
    fn new(inner: Arc<WorkerCoreInner>, invocation_id: InvocationId) -> Self {
        Self {
            inner,
            invocation_id,
        }
    }
}

impl Drop for PendingInvocationGuard {
    fn drop(&mut self) {
        lock(&self.inner.pending).remove(&self.invocation_id);
    }
}

fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex.lock().expect("worker core mutex poisoned")
}
