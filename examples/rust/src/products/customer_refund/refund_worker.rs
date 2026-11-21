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

//! Builds the refund Flow, runs its Worker, and opens a Client for the commands and tests.

use std::error::Error;
use std::path::Path;
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::Duration;

use dex_sdk::{
    BlobCache, Client, ClientOptions, PullWorkerOptions, Registry, SdkResult, Worker, WorkerOptions,
};

use super::billing_service::FakeBillingService;
use super::email_service::FakeEmailService;
use super::message_model::MockMessageModel;
use super::refund_config::{BillingMode, new_refund_worker_id};
use super::refund_flow::{CustomerRefundFlow, RefundServices};

const PULL_REGISTRATION_TIMEOUT: Duration = Duration::from_secs(30);

/// Returns the Flow; billing mode `call-missing` leaves out the billing call.
pub fn new_refund_flow(
    billing_mode: BillingMode,
    billing_ledger: impl AsRef<Path>,
    email_ledger: impl AsRef<Path>,
) -> CustomerRefundFlow {
    let services = RefundServices {
        billing: FakeBillingService::new(billing_ledger.as_ref(), billing_mode),
        email: FakeEmailService::new(email_ledger.as_ref()),
        model: MockMessageModel,
    };
    CustomerRefundFlow::new(Arc::new(services))
}

/// Returns Worker options with a new Worker ID that does not name the host.
pub fn refund_worker_options(server_address: &str) -> WorkerOptions {
    WorkerOptions::new()
        .server_address(server_address)
        .pull(PullWorkerOptions::new().app_worker_id(new_refund_worker_id()))
}

/// A Worker that serves CustomerRefundFlow on its own thread until stopped.
pub struct RunningRefundWorker {
    worker: Arc<Worker>,
    thread: Option<JoinHandle<SdkResult<()>>>,
}

impl RunningRefundWorker {
    /// Starts the Worker and returns once Dex has registered it.
    pub fn start(
        refund_flow: CustomerRefundFlow,
        cache: Arc<BlobCache>,
        options: WorkerOptions,
    ) -> Result<Self, Box<dyn Error>> {
        let registry = Registry::new().register(refund_flow)?;
        let worker = Arc::new(Worker::try_new(registry, cache, options)?);
        let serving_worker = Arc::clone(&worker);
        let thread = thread::spawn(move || serving_worker.start());
        let running = Self {
            worker,
            thread: Some(thread),
        };
        let registration = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()?
            .block_on(async {
                tokio::time::timeout(
                    PULL_REGISTRATION_TIMEOUT,
                    running.worker.wait_pull_registered(),
                )
                .await
            });
        registration??;
        Ok(running)
    }

    /// Stops the Worker and returns the error that ended it, if any.
    pub fn stop(mut self) -> Result<(), Box<dyn Error>> {
        self.stop_and_join()
    }

    fn stop_and_join(&mut self) -> Result<(), Box<dyn Error>> {
        self.worker.stop();
        let Some(thread) = self.thread.take() else {
            return Ok(());
        };
        let stopped = thread.join().map_err(|_| "the Worker thread panicked")?;
        Ok(stopped?)
    }
}

impl Drop for RunningRefundWorker {
    fn drop(&mut self) {
        // Reached when a caller returns early; stop the Worker so it serves no later run.
        if let Err(error) = self.stop_and_join() {
            eprintln!("stop the refund Worker: {error}");
        }
    }
}

/// Opens a Client that knows CustomerRefundFlow.
pub fn open_refund_client(
    refund_flow: CustomerRefundFlow,
    cache: Arc<BlobCache>,
    server_address: &str,
) -> SdkResult<Client> {
    let registry = Registry::new().register(refund_flow)?;
    Client::try_new(
        registry,
        cache,
        ClientOptions::new().server_address(server_address),
    )
}
