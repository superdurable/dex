// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::sync::{Arc, Weak};
use std::time::{Duration, SystemTime};

use dex_protocol::dex::StepStreamWrite;
use tokio::sync::Mutex;
use tokio::task::JoinHandle;

use crate::context::{InvocationCancellation, StepOutputFinalizer};
use crate::worker_output::StepOutputEmitter;
use crate::{Context, HandlerError, HandlerResult, Value, value_mapper};

const DEFAULT_FLUSH_INTERVAL: Duration = Duration::from_secs(1);
const DEFAULT_MAX_BUFFERED_BYTES: usize = 16 * 1024;

#[derive(Debug)]
struct StreamDefinition {
    name: String,
    stream_capacity_bytes: i64,
}

/// Defines a typed best-effort resumable message Stream owned by one Flow type.
///
/// Register the same definition in exactly one [`crate::PersistenceSchema`]. Clones share
/// identity and may be used at Client and Step call sites.
#[derive(Clone, Debug)]
pub struct Stream<T> {
    definition: Arc<StreamDefinition>,
    marker: PhantomData<fn() -> T>,
}

impl<T> Stream<T> {
    /// Creates a Stream with a positive approximate budget shared by all instances of its Flow.
    ///
    /// # Panics
    ///
    /// Panics when `name` is empty or `stream_capacity_bytes` is not positive.
    pub fn new(name: impl Into<String>, stream_capacity_bytes: i64) -> Self {
        let name = name.into();
        assert!(!name.is_empty(), "Stream name must not be empty");
        assert!(
            stream_capacity_bytes > 0,
            "Stream stream_capacity_bytes must be positive"
        );
        Self {
            definition: Arc::new(StreamDefinition {
                name,
                stream_capacity_bytes,
            }),
            marker: PhantomData,
        }
    }

    /// Returns the stable logical Stream name.
    pub fn name(&self) -> &str {
        &self.definition.name
    }

    /// Returns the approximate shared byte budget.
    pub fn stream_capacity_bytes(&self) -> i64 {
        self.definition.stream_capacity_bytes
    }

    /// Appends one message immediately from the current Step execution.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] for RPC or Flow timeout Contexts, unregistered Streams,
    /// encoding failures, cancellation, or a closed Worker output stream. Dex storage failures are
    /// not acknowledged.
    pub fn write(&self, context: &mut Context, value: T) -> HandlerResult<()>
    where
        T: Value,
    {
        context.write_stream(self, value)
    }

    pub(crate) fn identity(&self) -> usize {
        Arc::as_ptr(&self.definition) as usize
    }
}

impl Stream<String> {
    /// Creates an invocation-managed writer with a one-second interval and 16 KiB threshold.
    ///
    /// The Worker flushes remaining text before the handler result or error. The writer preserves
    /// chunks exactly, ignores empty chunks, and never splits a chunk.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] unless this registered String Stream is used by an active Step.
    pub fn buffered_text(&self, context: &mut Context) -> HandlerResult<BufferedTextStream> {
        self.buffered_text_with_options(context, BufferedTextStreamOptions::default())
    }

    /// Creates an invocation-managed writer with explicit buffering settings.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] for invalid settings, an unregistered Stream, cancellation, or a
    /// Context that does not belong to a Step.
    pub fn buffered_text_with_options(
        &self,
        context: &mut Context,
        options: BufferedTextStreamOptions,
    ) -> HandlerResult<BufferedTextStream> {
        options.validate()?;
        let (output_emitter, cancellation, runtime) = context.prepare_buffered_text_stream(self)?;
        let inner = Arc::new(BufferedTextStreamInner {
            stream: self.clone(),
            output_emitter,
            cancellation: cancellation.clone(),
            runtime: runtime.clone(),
            options,
            state: Mutex::new(BufferedTextStreamState::default()),
        });
        context.register_step_output_finalizer(inner.clone())?;
        BufferedTextStreamInner::watch_cancellation(&inner);
        Ok(BufferedTextStream { inner })
    }
}

/// Configures an invocation-managed buffered text Stream writer.
#[derive(Clone, Copy, Debug)]
pub struct BufferedTextStreamOptions {
    flush_interval: Duration,
    max_buffered_bytes: usize,
}

impl BufferedTextStreamOptions {
    /// Creates settings with a positive interval and soft UTF-8 byte threshold.
    ///
    /// Validation occurs when the writer is created.
    pub fn new(flush_interval: Duration, max_buffered_bytes: usize) -> Self {
        Self {
            flush_interval,
            max_buffered_bytes,
        }
    }

    /// Returns the one-shot flush interval.
    pub fn flush_interval(&self) -> Duration {
        self.flush_interval
    }

    /// Returns the soft UTF-8 batch threshold.
    pub fn max_buffered_bytes(&self) -> usize {
        self.max_buffered_bytes
    }

    fn validate(&self) -> HandlerResult<()> {
        if self.flush_interval.is_zero() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Buffered Stream flush interval must be positive",
            ));
        }
        if self.max_buffered_bytes == 0 {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Buffered Stream max buffered bytes must be positive",
            ));
        }
        Ok(())
    }
}

impl Default for BufferedTextStreamOptions {
    fn default() -> Self {
        Self {
            flush_interval: DEFAULT_FLUSH_INTERVAL,
            max_buffered_bytes: DEFAULT_MAX_BUFFERED_BYTES,
        }
    }
}

/// Batches text chunks during one Step invocation.
///
/// [`Self::write`] and [`Self::flush`] block only on the Worker's bounded output queue. They do not
/// wait for Dex Stream Store acknowledgement. The Worker automatically flushes the tail before
/// returning the handler result or error.
#[derive(Clone)]
pub struct BufferedTextStream {
    inner: Arc<BufferedTextStreamInner>,
}

impl BufferedTextStream {
    /// Appends one chunk and flushes after crossing the soft UTF-8 size threshold.
    ///
    /// Empty chunks are ignored. The first nonempty chunk starts a one-shot timer.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] after cancellation, invocation completion, or output failure.
    pub fn write(&self, chunk: impl AsRef<str>) -> HandlerResult<()> {
        let chunk = chunk.as_ref();
        let mut state = self.inner.state.blocking_lock();
        state.require_open()?;
        if chunk.is_empty() {
            return Ok(());
        }
        let was_empty = state.buffer.is_empty();
        state.buffer.push_str(chunk);
        state.buffered_bytes += chunk.len();
        if was_empty {
            self.inner.start_timer(&mut state);
        }
        if state.buffered_bytes >= self.inner.options.max_buffered_bytes {
            self.inner.stop_timer(&mut state);
            self.inner.flush_locked(&mut state)?;
        }
        Ok(())
    }

    /// Emits the current nonempty batch immediately.
    ///
    /// # Errors
    ///
    /// Returns [`HandlerError`] after cancellation, invocation completion, or output failure.
    pub fn flush(&self) -> HandlerResult<()> {
        let mut state = self.inner.state.blocking_lock();
        state.require_open()?;
        self.inner.stop_timer(&mut state);
        self.inner.flush_locked(&mut state)
    }
}

struct BufferedTextStreamInner {
    stream: Stream<String>,
    output_emitter: StepOutputEmitter,
    cancellation: InvocationCancellation,
    runtime: tokio::runtime::Handle,
    options: BufferedTextStreamOptions,
    state: Mutex<BufferedTextStreamState>,
}

#[derive(Default)]
struct BufferedTextStreamState {
    buffer: String,
    buffered_bytes: usize,
    timer_generation: u64,
    timer: Option<JoinHandle<()>>,
    closed: bool,
    terminal_failure: Option<String>,
}

impl BufferedTextStreamState {
    fn require_open(&self) -> HandlerResult<()> {
        if let Some(failure) = &self.terminal_failure {
            return Err(HandlerError::new("dex_sdk::HandlerError", failure));
        }
        if self.closed {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Buffered Stream invocation has finished",
            ));
        }
        Ok(())
    }
}

impl BufferedTextStreamInner {
    fn start_timer(self: &Arc<Self>, state: &mut BufferedTextStreamState) {
        state.timer_generation += 1;
        let generation = state.timer_generation;
        let weak = Arc::downgrade(self);
        let interval = self.options.flush_interval;
        state.timer = Some(self.runtime.spawn(async move {
            tokio::time::sleep(interval).await;
            if let Some(inner) = weak.upgrade() {
                inner.flush_from_timer(generation).await;
            }
        }));
    }

    fn stop_timer(&self, state: &mut BufferedTextStreamState) -> Option<JoinHandle<()>> {
        state.timer_generation += 1;
        let timer = state.timer.take();
        if let Some(timer) = &timer {
            timer.abort();
        }
        timer
    }

    fn flush_locked(&self, state: &mut BufferedTextStreamState) -> HandlerResult<()> {
        if state.buffer.is_empty() {
            return Ok(());
        }
        let value = std::mem::take(&mut state.buffer);
        state.buffered_bytes = 0;
        let write = self.stream_write(value)?;
        if let Err(failure) = self.output_emitter.send_stream_write(write) {
            state.terminal_failure = Some(failure.to_string());
            return Err(failure);
        }
        Ok(())
    }

    async fn flush_from_timer(&self, generation: u64) {
        let mut state = self.state.lock().await;
        if state.closed
            || state.terminal_failure.is_some()
            || generation != state.timer_generation
            || self.cancellation.is_cancelled()
        {
            return;
        }
        state.timer = None;
        if state.buffer.is_empty() {
            return;
        }
        let value = std::mem::take(&mut state.buffer);
        state.buffered_bytes = 0;
        let write = match self.stream_write(value) {
            Ok(write) => write,
            Err(failure) => {
                state.terminal_failure = Some(failure.to_string());
                return;
            }
        };
        if let Err(failure) = self.output_emitter.send_stream_write_async(write).await {
            state.terminal_failure = Some(failure.to_string());
        }
    }

    fn stream_write(&self, value: String) -> HandlerResult<StepStreamWrite> {
        Ok(StepStreamWrite {
            stream_name: self.stream.name().to_string(),
            stream_capacity_bytes: self.stream.stream_capacity_bytes(),
            value: Some(value_mapper::encode_handler(&value)?),
        })
    }

    fn watch_cancellation(this: &Arc<Self>) {
        let weak: Weak<Self> = Arc::downgrade(this);
        let cancellation = this.cancellation.clone();
        this.runtime.spawn(async move {
            cancellation.cancelled().await;
            if let Some(inner) = weak.upgrade() {
                inner.cancel().await;
            }
        });
    }

    async fn cancel(&self) {
        let mut state = self.state.lock().await;
        state.closed = true;
        self.stop_timer(&mut state);
        state.buffer.clear();
        state.buffered_bytes = 0;
    }
}

impl StepOutputFinalizer for BufferedTextStreamInner {
    fn finalize_step_output(&self) -> HandlerResult<()> {
        let timer = {
            let mut state = self.state.blocking_lock();
            if state.closed {
                return Ok(());
            }
            state.closed = true;
            self.stop_timer(&mut state)
        };
        if let Some(timer) = timer
            && let Err(failure) = self.runtime.block_on(timer)
            && !failure.is_cancelled()
        {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                format!("Buffered Stream timer failed: {failure}"),
            ));
        }
        let mut state = self.state.blocking_lock();
        if let Some(failure) = &state.terminal_failure {
            return Err(HandlerError::new("dex_sdk::HandlerError", failure));
        }
        self.flush_locked(&mut state)
    }
}

/// Describes one retained Stream message returned by [`crate::Client::read_stream`].
#[derive(Clone, Debug)]
pub struct StreamMessage<T> {
    /// Decoded application message.
    pub value: T,
    /// Opaque token to pass unchanged to the next read.
    pub resume_token: String,
    /// Server-assigned creation time.
    pub created_time: SystemTime,
    /// Client-provided source or Step-generated `#stepExecutionID` source.
    pub source: String,
}
