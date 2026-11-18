// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

//! Real-server harness for the adaptive slot stream tests: a per-test dex-server-zd process,
//! a gRPC proxy that observes and gates slot streams, and a raw competing Worker.

use std::collections::HashMap;
use std::convert::Infallible;
use std::fmt::Write as _;
use std::future::Future;
use std::path::PathBuf;
use std::pin::Pin;
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::task::{Context as TaskContext, Poll};
use std::thread;
use std::time::{Duration, Instant};

use dex_protocol::dex::poll_worker_task_request::Frame as RequestFrame;
use dex_protocol::dex::poll_worker_task_response::Frame as ResponseFrame;
use dex_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dex_protocol::dex::worker_task_service_server::{WorkerTaskService, WorkerTaskServiceServer};
use dex_protocol::dex::{
    PollWorkerSlotKind, PollWorkerTaskPong, PollWorkerTaskRegister, PollWorkerTaskRequest,
    PollWorkerTaskResponse,
};
use dex_sdk::{BlobCache, BlobCacheConfig, Client, ClientOptions, Registry};
use tempfile::TempDir;
use tokio::runtime::Runtime;
use tokio::sync::{mpsc, watch};
use tokio::task::JoinHandle;
use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream};
use tonic::body::Body;
use tonic::codegen::{Service, http};
use tonic::server::NamedService;
use tonic::transport::{Channel, Endpoint, Server};
use tonic::{Code, Request, Response, Status, Streaming};

pub(crate) const STEP_AND_RPC: PollWorkerSlotKind = PollWorkerSlotKind::StepAndRpc;
pub(crate) const RPC_ONLY: PollWorkerSlotKind = PollWorkerSlotKind::RpcOnly;
const PROXY_RESET_MESSAGE: &str = "pull slot proxy reset the stream";

/// Polls `condition` every 10 ms until it holds or `timeout` passes; returns whether it held.
pub(crate) fn eventually(timeout: Duration, mut condition: impl FnMut() -> bool) -> bool {
    let deadline = Instant::now() + timeout;
    loop {
        if condition() {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(10));
    }
}

/// Polls `condition` every 10 ms for `duration`; returns whether it never held.
pub(crate) fn never(duration: Duration, mut condition: impl FnMut() -> bool) -> bool {
    let deadline = Instant::now() + duration;
    while Instant::now() < deadline {
        if condition() {
            return false;
        }
        thread::sleep(Duration::from_millis(10));
    }
    !condition()
}

/// Waits until the watched flag is true; returns false when its sender is gone.
pub(crate) async fn wait_until_true(receiver: &mut watch::Receiver<bool>) -> bool {
    loop {
        if *receiver.borrow_and_update() {
            return true;
        }
        if receiver.changed().await.is_err() {
            return false;
        }
    }
}

/// Matching limits of a test server; `None` keeps the server default.
#[derive(Clone, Copy, Default)]
pub(crate) struct ServerLimits {
    pub(crate) max_concurrent_tasks_cap: Option<usize>,
    pub(crate) max_streams: Option<usize>,
}

impl ServerLimits {
    pub(crate) fn cap(max_concurrent_tasks_cap: usize) -> Self {
        Self {
            max_concurrent_tasks_cap: Some(max_concurrent_tasks_cap),
            max_streams: None,
        }
    }
}

/// One dex-server-zd process per test, so a test can set caps, kill, and restart it.
pub(crate) struct AdaptiveSlotTestServer {
    binary: String,
    directory: TempDir,
    pub(crate) address: String,
    child: Option<Child>,
}

impl AdaptiveSlotTestServer {
    pub(crate) fn start(limits: ServerLimits) -> Self {
        let binary = std::env::var("DEX_SERVER_ZD_BINARY")
            .expect("set DEX_SERVER_ZD_BINARY or use run-integration-tests.sh");
        let mut server = Self {
            binary,
            directory: tempfile::tempdir().expect("create test server directory"),
            address: String::new(),
            child: None,
        };
        server.launch(limits);
        server
    }

    fn launch(&mut self, limits: ServerLimits) {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").expect("reserve server port");
        self.address = listener
            .local_addr()
            .expect("read reserved server address")
            .to_string();
        drop(listener);
        let mut configuration =
            "dev:\n  durabilityMode: processCrashSafe\nmatching:\n  pingInterval: 2s\n".to_string();
        if let Some(cap) = limits.max_concurrent_tasks_cap {
            writeln!(configuration, "  maxConcurrentTasksCap: {cap}").expect("format config");
        }
        if let Some(max_streams) = limits.max_streams {
            writeln!(configuration, "  maxStreams: {max_streams}").expect("format config");
        }
        let configuration_path = self.directory.path().join("adaptive-slot-config.yaml");
        std::fs::write(&configuration_path, configuration).expect("write server config");
        let log = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(self.log_path())
            .expect("open server log");
        let child = Command::new(&self.binary)
            .arg("dev")
            .arg("--data-dir")
            .arg(self.directory.path().join("data"))
            .arg("--listen")
            .arg(&self.address)
            .arg("--config")
            .arg(configuration_path)
            .stdout(Stdio::from(log.try_clone().expect("clone server log")))
            .stderr(Stdio::from(log))
            .spawn()
            .expect("start dex-server-zd");
        self.child = Some(child);
        let client = Client::new(
            Registry::new(),
            test_cache().0,
            ClientOptions::new().server_address(&self.address),
        );
        let is_serving = eventually(Duration::from_secs(30), || {
            if self
                .child
                .as_mut()
                .expect("server child")
                .try_wait()
                .expect("check server exit")
                .is_some()
            {
                panic!("dex-server-zd exited before serving\n{}", self.log_tail());
            }
            client.health_check().is_ok()
        });
        assert!(
            is_serving,
            "dex-server-zd did not serve\n{}",
            self.log_tail()
        );
    }

    fn log_path(&self) -> PathBuf {
        self.directory.path().join("server.log")
    }

    /// Ends the server like a crash: every stream breaks and nothing drains.
    pub(crate) fn kill(&mut self) {
        if let Some(mut child) = self.child.take() {
            if child.try_wait().expect("check server exit").is_none() {
                child.kill().expect("kill dex-server-zd");
            }
            child.wait().expect("reap dex-server-zd");
        }
    }

    /// Starts a new process on the same data directory, on a new address.
    pub(crate) fn restart(&mut self, limits: ServerLimits) {
        self.kill();
        self.launch(limits);
    }

    pub(crate) fn log_tail(&self) -> String {
        match std::fs::read_to_string(self.log_path()) {
            Ok(log) => {
                let lines: Vec<&str> = log.lines().collect();
                lines[lines.len().saturating_sub(80)..].join("\n")
            }
            Err(error) => format!("read dex-server-zd log: {error}"),
        }
    }

    /// Returns a Client of the current process; a restart needs a new Client.
    pub(crate) fn new_client(&self, registry: &Registry) -> TestClient {
        let (cache, directory) = test_cache();
        let client = Client::new(
            registry.clone(),
            Arc::clone(&cache),
            ClientOptions::new().server_address(&self.address),
        );
        TestClient {
            client,
            _cache: cache,
            _directory: directory,
        }
    }
}

impl Drop for AdaptiveSlotTestServer {
    fn drop(&mut self) {
        self.kill();
    }
}

/// A Client with the blob cache it needs.
pub(crate) struct TestClient {
    pub(crate) client: Client,
    _cache: Arc<BlobCache>,
    _directory: TempDir,
}

pub(crate) fn test_cache() -> (Arc<BlobCache>, TempDir) {
    let directory = tempfile::tempdir().expect("create cache directory");
    let cache = Arc::new(
        BlobCache::open(
            BlobCacheConfig::new(directory.path(), 16 * 1024 * 1024, 10_000)
                .expect("valid cache config"),
        )
        .expect("open cache"),
    );
    (cache, directory)
}

/// One Register, acknowledgement, rejection, or reset that the proxy saw.
#[derive(Clone, Debug)]
pub(crate) struct ProxyEvent {
    pub(crate) app_worker_id: String,
    pub(crate) slot_kind: PollWorkerSlotKind,
    pub(crate) at: Instant,
}

struct ProxyStream {
    app_worker_id: String,
    slot_kind: PollWorkerSlotKind,
    is_registered: bool,
    has_ended: bool,
    reset: watch::Sender<bool>,
    upstream_answered: watch::Sender<bool>,
    stage: &'static str,
}

/// Holds STEP_AND_RPC Registers; `is_released_by_rpc_only` releases it at the first RPC_ONLY
/// acknowledgement.
struct StepRegisterGate {
    release: watch::Sender<bool>,
    is_released_by_rpc_only: bool,
    held: Vec<u64>,
    /// Set when the first STEP_AND_RPC Register waits; an RPC_ONLY Register waits for it.
    step_held: watch::Sender<bool>,
}

#[derive(Default)]
struct ProxyState {
    upstream: Option<Channel>,
    streams: HashMap<u64, ProxyStream>,
    next_stream_id: u64,
    registers: Vec<ProxyEvent>,
    acknowledgements: Vec<ProxyEvent>,
    rejections: Vec<ProxyEvent>,
    worker_resets: Vec<ProxyEvent>,
    step_register_gate: Option<StepRegisterGate>,
    acknowledgement_gate: Option<watch::Receiver<bool>>,
    fail_step_registers: bool,
    pass_step_registers: usize,
    held_register_limit: Option<usize>,
    held_register_release: Option<watch::Receiver<bool>>,
    held_registers: usize,
    half_close_gate: Option<watch::Receiver<bool>>,
    held_half_closes: usize,
    first_acknowledgement_at: Option<Instant>,
}

#[derive(Clone, Default)]
struct ProxyShared {
    state: Arc<Mutex<ProxyState>>,
}

impl ProxyShared {
    fn lock(&self) -> std::sync::MutexGuard<'_, ProxyState> {
        self.state.lock().expect("proxy state lock")
    }

    fn upstream(&self) -> Channel {
        self.lock().upstream.clone().expect("proxy upstream")
    }
}

/// Forwards a Worker's gRPC traffic to the current server and observes its slot streams.
pub(crate) struct PullSlotProxy {
    shared: ProxyShared,
    pub(crate) address: String,
    shutdown: watch::Sender<bool>,
    server: Option<JoinHandle<Result<(), tonic::transport::Error>>>,
    runtime: Arc<Runtime>,
}

impl PullSlotProxy {
    pub(crate) fn start(runtime: &Arc<Runtime>, upstream_address: &str) -> Self {
        let shared = ProxyShared::default();
        let proxy_address;
        let (shutdown, mut stopped) = watch::channel(false);
        let server = {
            let _guard = runtime.enter();
            let listener = runtime
                .block_on(tokio::net::TcpListener::bind("127.0.0.1:0"))
                .expect("bind proxy");
            proxy_address = listener
                .local_addr()
                .expect("read proxy address")
                .to_string();
            let worker_tasks = WorkerTaskServiceServer::new(ProxyWorkerTaskService {
                shared: shared.clone(),
            })
            .max_decoding_message_size(64 * 1024 * 1024)
            .max_encoding_message_size(64 * 1024 * 1024);
            runtime.spawn(
                Server::builder()
                    .add_service(worker_tasks)
                    .add_service(FlowServicePassthrough {
                        shared: shared.clone(),
                    })
                    .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                        while !*stopped.borrow() && stopped.changed().await.is_ok() {}
                    }),
            )
        };
        let proxy = Self {
            shared,
            address: proxy_address,
            shutdown,
            server: Some(server),
            runtime: Arc::clone(runtime),
        };
        proxy.set_upstream(upstream_address);
        proxy
    }

    /// Points new calls at `address`; calls already open stay on the old server.
    pub(crate) fn set_upstream(&self, address: &str) {
        let _guard = self.runtime.enter();
        let channel = Endpoint::from_shared(format!("http://{address}"))
            .expect("proxy upstream endpoint")
            .connect_lazy();
        self.shared.lock().upstream = Some(channel);
    }

    /// Forces the reconnect order of design Test 17: a STEP_AND_RPC Register waits, the RPC_ONLY
    /// stream registers, and the waiting Register reaches the server before the Worker sees that
    /// acknowledgement.
    pub(crate) fn hold_step_registers_until_rpc_only_acknowledged(&self) {
        self.shared.lock().step_register_gate = Some(StepRegisterGate {
            release: watch::channel(false).0,
            is_released_by_rpc_only: true,
            held: Vec::new(),
            step_held: watch::channel(false).0,
        });
    }

    /// Holds STEP_AND_RPC Registers until the returned release runs.
    pub(crate) fn hold_step_registers(&self) -> impl FnOnce() + use<> {
        let release = watch::channel(false).0;
        let release_signal = release.clone();
        self.shared.lock().step_register_gate = Some(StepRegisterGate {
            release,
            is_released_by_rpc_only: false,
            held: Vec::new(),
            step_held: watch::channel(false).0,
        });
        let shared = self.shared.clone();
        move || {
            let mut state = shared.lock();
            if state
                .step_register_gate
                .as_ref()
                .is_some_and(|gate| gate.release.same_channel(&release_signal))
            {
                state.step_register_gate = None;
            }
            drop(state);
            release_signal.send_replace(true);
        }
    }

    /// Counts the STEP_AND_RPC Registers that the manual gate holds now.
    pub(crate) fn held_step_registers(&self) -> usize {
        let state = self.shared.lock();
        let Some(gate) = &state.step_register_gate else {
            return 0;
        };
        gate.held
            .iter()
            .filter(|stream_id| {
                state
                    .streams
                    .get(stream_id)
                    .is_some_and(|stream| !stream.has_ended)
            })
            .count()
    }

    /// Holds the next `count` STEP_AND_RPC Registers before upstream, like a blocked send.
    pub(crate) fn block_next_step_registers(&self, count: usize) -> impl FnOnce() + use<> {
        let (release, receiver) = watch::channel(false);
        let mut state = self.shared.lock();
        state.held_register_limit = Some(count);
        state.held_register_release = Some(receiver);
        state.held_registers = 0;
        move || {
            release.send_replace(true);
        }
    }

    pub(crate) fn blocked_registers(&self) -> usize {
        self.shared.lock().held_registers
    }

    /// Answers every later STEP_AND_RPC Register with UNAVAILABLE until
    /// [`Self::stop_failing_step_registers`].
    pub(crate) fn fail_step_registers_from_now(&self) {
        let mut state = self.shared.lock();
        state.fail_step_registers = true;
        state.pass_step_registers = 0;
    }

    /// Lets one STEP_AND_RPC Register through while Registers fail.
    pub(crate) fn pass_next_step_register(&self) {
        self.shared.lock().pass_step_registers += 1;
    }

    pub(crate) fn stop_failing_step_registers(&self) {
        self.shared.lock().fail_step_registers = false;
    }

    /// Keeps every Worker half-close from reaching the server until the returned release runs.
    pub(crate) fn hold_half_closes(&self) -> impl FnOnce() + use<> {
        let (release, receiver) = watch::channel(false);
        let mut state = self.shared.lock();
        state.half_close_gate = Some(receiver);
        state.held_half_closes = 0;
        let shared = self.shared.clone();
        move || {
            shared.lock().half_close_gate = None;
            release.send_replace(true);
        }
    }

    pub(crate) fn held_half_closes(&self) -> usize {
        self.shared.lock().held_half_closes
    }

    /// Delays every acknowledgement until the returned release runs.
    pub(crate) fn hold_acknowledgements(&self) -> impl FnOnce() + use<> {
        let (release, receiver) = watch::channel(false);
        self.shared.lock().acknowledgement_gate = Some(receiver);
        let shared = self.shared.clone();
        move || {
            shared.lock().acknowledgement_gate = None;
            release.send_replace(true);
        }
    }

    /// Resets one acknowledged stream of the kind, like a network failure.
    pub(crate) fn reset_registered_stream(
        &self,
        app_worker_id: &str,
        slot_kind: PollWorkerSlotKind,
    ) -> bool {
        let state = self.shared.lock();
        for stream in state.streams.values() {
            if stream.app_worker_id == app_worker_id
                && stream.slot_kind == slot_kind
                && stream.is_registered
                && !stream.has_ended
                && !*stream.reset.borrow()
            {
                stream.reset.send_replace(true);
                return true;
            }
        }
        false
    }

    /// Counts the acknowledged, live streams of a kind, as the server holds them.
    pub(crate) fn registered_streams(
        &self,
        app_worker_id: &str,
        slot_kind: PollWorkerSlotKind,
    ) -> usize {
        self.shared
            .lock()
            .streams
            .values()
            .filter(|stream| {
                stream.app_worker_id == app_worker_id
                    && stream.slot_kind == slot_kind
                    && stream.is_registered
                    && !stream.has_ended
            })
            .count()
    }

    pub(crate) fn registers_since(&self, app_worker_id: &str, since: Instant) -> Vec<ProxyEvent> {
        filter_events(&self.shared.lock().registers, app_worker_id, since)
    }

    pub(crate) fn acknowledgements_since(
        &self,
        app_worker_id: &str,
        since: Instant,
    ) -> Vec<ProxyEvent> {
        filter_events(&self.shared.lock().acknowledgements, app_worker_id, since)
    }

    pub(crate) fn rejections_since(&self, app_worker_id: &str, since: Instant) -> Vec<ProxyEvent> {
        filter_events(&self.shared.lock().rejections, app_worker_id, since)
    }

    pub(crate) fn worker_resets_since(
        &self,
        app_worker_id: &str,
        since: Instant,
    ) -> Vec<ProxyEvent> {
        filter_events(&self.shared.lock().worker_resets, app_worker_id, since)
    }

    pub(crate) fn first_acknowledgement_at(&self) -> Option<Instant> {
        self.shared.lock().first_acknowledgement_at
    }

    /// Summarizes what the proxy saw of `app_worker_id`, for failure messages.
    pub(crate) fn describe(&self, app_worker_id: &str) -> String {
        let state = self.shared.lock();
        let origin = state
            .registers
            .first()
            .map_or_else(Instant::now, |event| event.at);
        let mut description = String::new();
        for (name, events) in [
            ("registers", &state.registers),
            ("acknowledgements", &state.acknowledgements),
            ("rejections", &state.rejections),
            ("workerResets", &state.worker_resets),
        ] {
            write!(description, "{name}:").expect("format description");
            for event in events
                .iter()
                .filter(|event| event.app_worker_id == app_worker_id)
            {
                write!(
                    description,
                    " {:?}@{}ms",
                    event.slot_kind,
                    event.at.saturating_duration_since(origin).as_millis()
                )
                .expect("format description");
            }
            description.push('\n');
        }
        for stream in state.streams.values() {
            if stream.app_worker_id == app_worker_id {
                writeln!(
                    description,
                    "live {:?} registered={} stage={}",
                    stream.slot_kind, stream.is_registered, stream.stage
                )
                .expect("format description");
            }
        }
        description
    }
}

impl Drop for PullSlotProxy {
    fn drop(&mut self) {
        self.shutdown.send_replace(true);
        if let Some(server) = self.server.take() {
            server.abort();
        }
    }
}

fn filter_events(events: &[ProxyEvent], app_worker_id: &str, since: Instant) -> Vec<ProxyEvent> {
    events
        .iter()
        .filter(|event| event.app_worker_id == app_worker_id && event.at >= since)
        .cloned()
        .collect()
}

pub(crate) fn step_events(events: &[ProxyEvent]) -> Vec<ProxyEvent> {
    events
        .iter()
        .filter(|event| event.slot_kind == STEP_AND_RPC)
        .cloned()
        .collect()
}

#[derive(Clone)]
struct ProxyWorkerTaskService {
    shared: ProxyShared,
}

/// Marks a proxied stream ended however its handler or pump exits.
struct ProxyStreamGuard {
    shared: ProxyShared,
    stream_id: u64,
}

impl Drop for ProxyStreamGuard {
    fn drop(&mut self) {
        let mut state = self.shared.lock();
        if let Some(stream) = state.streams.get_mut(&self.stream_id) {
            stream.has_ended = true;
            stream.upstream_answered.send_replace(true);
        }
        state.streams.remove(&self.stream_id);
    }
}

#[tonic::async_trait]
impl WorkerTaskService for ProxyWorkerTaskService {
    type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

    async fn poll_worker_task(
        &self,
        request: Request<Streaming<PollWorkerTaskRequest>>,
    ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
        let metadata = request.metadata().clone();
        let mut incoming = request.into_inner();
        let first = incoming
            .message()
            .await?
            .ok_or_else(|| Status::invalid_argument("proxy: missing Register"))?;
        let Some(RequestFrame::Register(register)) = first.frame.as_ref() else {
            return Err(Status::invalid_argument("proxy: Register must be first"));
        };
        let slot_kind = register.slot_kind();
        let app_worker_id = register.app_worker_id.clone();
        let is_step = slot_kind == STEP_AND_RPC;
        let (reset, mut reset_signal) = watch::channel(false);
        let (upstream_answered, _) = watch::channel(false);
        let (stream_id, step_gate, rpc_only_gate, should_fail, blocked_register) = {
            let mut state = self.shared.lock();
            let stream_id = state.next_stream_id;
            state.next_stream_id += 1;
            state.registers.push(ProxyEvent {
                app_worker_id: app_worker_id.clone(),
                slot_kind,
                at: Instant::now(),
            });
            state.streams.insert(
                stream_id,
                ProxyStream {
                    app_worker_id: app_worker_id.clone(),
                    slot_kind,
                    is_registered: false,
                    has_ended: false,
                    reset,
                    upstream_answered,
                    stage: "arrived",
                },
            );
            let mut step_gate = None;
            let mut rpc_only_gate = None;
            if let Some(gate) = state.step_register_gate.as_mut() {
                if is_step {
                    gate.held.push(stream_id);
                    gate.step_held.send_replace(true);
                    step_gate = Some(gate.release.subscribe());
                } else if gate.is_released_by_rpc_only {
                    rpc_only_gate = Some(gate.step_held.subscribe());
                }
            }
            let should_fail =
                is_step && state.fail_step_registers && state.pass_step_registers == 0;
            if is_step && state.fail_step_registers && state.pass_step_registers > 0 {
                state.pass_step_registers -= 1;
            }
            let mut blocked_register = None;
            if is_step
                && let Some(limit) = state.held_register_limit
                && state.held_registers < limit
            {
                state.held_registers += 1;
                blocked_register = state.held_register_release.clone();
            }
            (
                stream_id,
                step_gate,
                rpc_only_gate,
                should_fail,
                blocked_register,
            )
        };
        let guard = ProxyStreamGuard {
            shared: self.shared.clone(),
            stream_id,
        };
        if should_fail {
            return Err(Status::unavailable("pull slot proxy failed the Register"));
        }
        if let Some(mut release) = blocked_register {
            self.set_stage(stream_id, "blocked before upstream");
            if !wait_until_true(&mut release).await {
                return Err(Status::unavailable("proxy register release closed"));
            }
        }
        if let Some(mut release) = step_gate {
            self.set_stage(stream_id, "gated");
            if !wait_until_true(&mut release).await {
                return Err(Status::unavailable("proxy step gate closed"));
            }
        }
        if let Some(mut step_held) = rpc_only_gate {
            self.set_stage(stream_id, "waiting for a held Step-and-RPC Register");
            if !wait_until_true(&mut step_held).await {
                return Err(Status::unavailable("proxy step gate closed"));
            }
        }
        self.set_stage(stream_id, "opening upstream");
        let (upstream_sender, upstream_receiver) = mpsc::channel(64);
        upstream_sender
            .send(first)
            .await
            .map_err(|_| Status::internal("proxy upstream channel closed"))?;
        let mut upstream_request = Request::new(ReceiverStream::new(upstream_receiver));
        *upstream_request.metadata_mut() = metadata;
        let mut client = WorkerTaskServiceClient::new(self.shared.upstream())
            .max_decoding_message_size(64 * 1024 * 1024)
            .max_encoding_message_size(64 * 1024 * 1024);
        let opened = tokio::select! {
            opened = client.poll_worker_task(upstream_request) => opened,
            _ = wait_until_true(&mut reset_signal) => {
                return Err(Status::unavailable(PROXY_RESET_MESSAGE));
            }
        };
        let upstream = match opened {
            Ok(response) => response.into_inner(),
            Err(status) => {
                self.record_end(stream_id, &status);
                return Err(status);
            }
        };
        self.set_stage(stream_id, "register sent upstream");
        let (downstream_sender, downstream_receiver) = mpsc::channel(64);
        let pump = ProxyPump {
            shared: self.shared.clone(),
            stream_id,
            _guard: guard,
        };
        // The pump ends with either side of the stream; the proxy runtime owns it.
        let _pump = tokio::spawn(pump.run(
            incoming,
            upstream_sender,
            upstream,
            downstream_sender,
            reset_signal,
        ));
        Ok(Response::new(ReceiverStream::new(downstream_receiver)))
    }
}

impl ProxyWorkerTaskService {
    fn set_stage(&self, stream_id: u64, stage: &'static str) {
        if let Some(stream) = self.shared.lock().streams.get_mut(&stream_id) {
            stream.stage = stage;
        }
    }

    fn record_end(&self, stream_id: u64, status: &Status) {
        record_stream_status(&self.shared, stream_id, status);
    }
}

fn record_stream_status(shared: &ProxyShared, stream_id: u64, status: &Status) {
    let mut state = shared.lock();
    let Some(stream) = state.streams.get(&stream_id) else {
        return;
    };
    stream.upstream_answered.send_replace(true);
    let event = ProxyEvent {
        app_worker_id: stream.app_worker_id.clone(),
        slot_kind: stream.slot_kind,
        at: Instant::now(),
    };
    if status.code() == Code::ResourceExhausted {
        state.rejections.push(event);
    }
}

struct ProxyPump {
    shared: ProxyShared,
    stream_id: u64,
    _guard: ProxyStreamGuard,
}

impl ProxyPump {
    async fn run(
        self,
        mut incoming: Streaming<PollWorkerTaskRequest>,
        upstream_sender: mpsc::Sender<PollWorkerTaskRequest>,
        mut upstream: Streaming<PollWorkerTaskResponse>,
        downstream: mpsc::Sender<Result<PollWorkerTaskResponse, Status>>,
        mut reset_signal: watch::Receiver<bool>,
    ) {
        let mut upstream_sender = Some(upstream_sender);
        loop {
            tokio::select! {
                _ = wait_until_true(&mut reset_signal) => {
                    // The send fails only after the Worker already went away.
                    if downstream.send(Err(Status::unavailable(PROXY_RESET_MESSAGE))).await.is_err() {
                        return;
                    }
                    return;
                }
                frame = incoming.message(), if upstream_sender.is_some() => {
                    match frame {
                        Ok(Some(frame)) => {
                            let sender = upstream_sender.as_ref().expect("open upstream sender");
                            if sender.send(frame).await.is_err() {
                                return;
                            }
                        }
                        Ok(None) => {
                            self.wait_for_half_close_gate().await;
                            upstream_sender = None;
                        }
                        Err(_) => {
                            self.record_worker_reset();
                            return;
                        }
                    }
                }
                response = upstream.message() => {
                    match response {
                        Ok(Some(response)) => {
                            self.mark_answered();
                            if matches!(response.frame, Some(ResponseFrame::Registered(_))) {
                                self.record_acknowledgement().await;
                            }
                            if downstream.send(Ok(response)).await.is_err() {
                                self.record_worker_reset();
                                return;
                            }
                        }
                        Ok(None) => return,
                        Err(status) => {
                            record_stream_status(&self.shared, self.stream_id, &status);
                            if downstream.send(Err(status)).await.is_err() {
                                return;
                            }
                            return;
                        }
                    }
                }
            }
        }
    }

    fn mark_answered(&self) {
        if let Some(stream) = self.shared.lock().streams.get(&self.stream_id) {
            stream.upstream_answered.send_replace(true);
        }
    }

    fn record_worker_reset(&self) {
        let mut state = self.shared.lock();
        let Some(stream) = state.streams.get(&self.stream_id) else {
            return;
        };
        if *stream.reset.borrow() {
            return;
        }
        let event = ProxyEvent {
            app_worker_id: stream.app_worker_id.clone(),
            slot_kind: stream.slot_kind,
            at: Instant::now(),
        };
        state.worker_resets.push(event);
    }

    async fn wait_for_half_close_gate(&self) {
        let gate = {
            let mut state = self.shared.lock();
            let gate = state.half_close_gate.clone();
            if gate.is_some() {
                state.held_half_closes += 1;
            }
            gate
        };
        if let Some(mut gate) = gate {
            // A dropped release means the test ended, so the half-close goes on either way.
            wait_until_true(&mut gate).await;
        }
    }

    async fn record_acknowledgement(&self) {
        let acknowledgement_gate = self.shared.lock().acknowledgement_gate.clone();
        if let Some(mut gate) = acknowledgement_gate {
            // A dropped release means the test ended, so the acknowledgement goes on either way.
            wait_until_true(&mut gate).await;
        }
        let held_answers = {
            let mut state = self.shared.lock();
            let Some(stream) = state.streams.get_mut(&self.stream_id) else {
                return;
            };
            stream.is_registered = true;
            let slot_kind = stream.slot_kind;
            let event = ProxyEvent {
                app_worker_id: stream.app_worker_id.clone(),
                slot_kind,
                at: Instant::now(),
            };
            state.acknowledgements.push(event);
            state
                .first_acknowledgement_at
                .get_or_insert_with(Instant::now);
            let is_releasing_step_registers = slot_kind == RPC_ONLY
                && state
                    .step_register_gate
                    .as_ref()
                    .is_some_and(|gate| gate.is_released_by_rpc_only);
            if is_releasing_step_registers {
                let gate = state.step_register_gate.take().expect("step register gate");
                gate.release.send_replace(true);
                gate.held
                    .iter()
                    .filter_map(|stream_id| state.streams.get(stream_id))
                    .map(|stream| stream.upstream_answered.subscribe())
                    .collect()
            } else {
                Vec::new()
            }
        };
        // The Worker sees this acknowledgement only after the server answered every held Register.
        for mut answered in held_answers {
            let is_answered =
                tokio::time::timeout(Duration::from_secs(10), wait_until_true(&mut answered)).await;
            assert!(
                is_answered.is_ok(),
                "a held Register got no answer from the server"
            );
        }
    }
}

/// Forwards every FlowService call unchanged to the current upstream.
#[derive(Clone)]
struct FlowServicePassthrough {
    shared: ProxyShared,
}

impl NamedService for FlowServicePassthrough {
    const NAME: &'static str = "dex.FlowService";
}

impl Service<http::Request<Body>> for FlowServicePassthrough {
    type Response = http::Response<Body>;
    type Error = Infallible;
    type Future = Pin<Box<dyn Future<Output = Result<http::Response<Body>, Infallible>> + Send>>;

    fn poll_ready(&mut self, _: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<Body>) -> Self::Future {
        let mut channel = self.shared.upstream();
        Box::pin(async move {
            if let Err(error) = std::future::poll_fn(|context| channel.poll_ready(context)).await {
                return Ok(Status::unavailable(error.to_string()).into_http());
            }
            match channel.call(request).await {
                Ok(response) => Ok(response),
                Err(error) => Ok(Status::unavailable(error.to_string()).into_http()),
            }
        })
    }
}

/// Holds node slots under another app Worker ID and retakes any freed slot at once.
pub(crate) struct PullSlotCompetitor {
    held: Arc<Mutex<HashMap<u64, watch::Sender<bool>>>>,
    stop: watch::Sender<bool>,
    loops: Vec<JoinHandle<()>>,
    runtime: Arc<Runtime>,
}

impl PullSlotCompetitor {
    pub(crate) fn start(
        runtime: &Arc<Runtime>,
        address: &str,
        app_worker_id: &str,
        loops: usize,
    ) -> Self {
        let channel = {
            let _guard = runtime.enter();
            Endpoint::from_shared(format!("http://{address}"))
                .expect("competitor endpoint")
                .connect_lazy()
        };
        let held = Arc::new(Mutex::new(HashMap::new()));
        let (stop, stopped) = watch::channel(false);
        let next_id = Arc::new(Mutex::new(0_u64));
        let loops = (0..loops)
            .map(|_| {
                let competitor_loop = CompetitorLoop {
                    client: WorkerTaskServiceClient::new(channel.clone()),
                    held: Arc::clone(&held),
                    next_id: Arc::clone(&next_id),
                    app_worker_id: app_worker_id.to_string(),
                    stopped: stopped.clone(),
                };
                runtime.spawn(competitor_loop.run())
            })
            .collect();
        Self {
            held,
            stop,
            loops,
            runtime: Arc::clone(runtime),
        }
    }

    pub(crate) fn held_streams(&self) -> usize {
        self.held.lock().expect("competitor lock").len()
    }

    /// Ends one held stream; its loop re-registers into the freed slot at once.
    pub(crate) fn close_one_stream(&self) {
        if let Some(close) = self.held.lock().expect("competitor lock").values().next() {
            close.send_replace(true);
        }
    }

    pub(crate) fn stop(&mut self) {
        self.stop.send_replace(true);
        for competitor_loop in self.loops.drain(..) {
            let result = self.runtime.block_on(competitor_loop);
            assert!(result.is_ok(), "competitor loop failed: {result:?}");
        }
    }
}

impl Drop for PullSlotCompetitor {
    fn drop(&mut self) {
        self.stop.send_replace(true);
        for competitor_loop in self.loops.drain(..) {
            competitor_loop.abort();
        }
    }
}

struct CompetitorLoop {
    client: WorkerTaskServiceClient<Channel>,
    held: Arc<Mutex<HashMap<u64, watch::Sender<bool>>>>,
    next_id: Arc<Mutex<u64>>,
    app_worker_id: String,
    stopped: watch::Receiver<bool>,
}

impl CompetitorLoop {
    async fn run(mut self) {
        while !*self.stopped.borrow() {
            self.register_and_hold().await;
            tokio::select! {
                () = tokio::time::sleep(Duration::from_millis(1)) => {}
                _ = wait_until_true(&mut self.stopped) => return,
            }
        }
    }

    async fn register_and_hold(&mut self) {
        let (sender, receiver) = mpsc::channel(8);
        let register = PollWorkerTaskRequest {
            frame: Some(RequestFrame::Register(PollWorkerTaskRegister {
                supports_reserved_slot_dispatch: true,
                slot_kind: STEP_AND_RPC as i32,
                app_worker_id: self.app_worker_id.clone(),
                flow_types: vec!["PullSlotCompetitorFlow".to_string()],
                sdk_name: "rust".to_string(),
                sdk_version: env!("CARGO_PKG_VERSION").to_string(),
                ..Default::default()
            })),
        };
        if sender.send(register).await.is_err() {
            return;
        }
        let Ok(response) = self
            .client
            .poll_worker_task(ReceiverStream::new(receiver))
            .await
        else {
            return;
        };
        let mut responses = response.into_inner();
        let Ok(Some(first)) = responses.message().await else {
            return;
        };
        if !matches!(first.frame, Some(ResponseFrame::Registered(_))) {
            return;
        }
        let held_id = {
            let mut next_id = self.next_id.lock().expect("competitor id lock");
            *next_id += 1;
            *next_id
        };
        let (close, mut closed) = watch::channel(false);
        self.held
            .lock()
            .expect("competitor lock")
            .insert(held_id, close);
        loop {
            tokio::select! {
                response = responses.message() => {
                    match response {
                        Ok(Some(PollWorkerTaskResponse { frame: Some(ResponseFrame::Ping(_)) })) => {
                            let pong = PollWorkerTaskRequest { frame: Some(RequestFrame::Pong(PollWorkerTaskPong {})) };
                            if sender.send(pong).await.is_err() {
                                break;
                            }
                        }
                        Ok(Some(_)) => {}
                        Ok(None) | Err(_) => break,
                    }
                }
                _ = wait_until_true(&mut closed) => break,
                _ = wait_until_true(&mut self.stopped) => break,
            }
        }
        self.held.lock().expect("competitor lock").remove(&held_id);
    }
}

/// WARN and INFO log lines of the process, for clamp and soft-rejection assertions.
pub(crate) mod captured_logs {
    use std::fmt::Write as _;
    use std::sync::{Mutex, OnceLock};

    static LINES: Mutex<Vec<String>> = Mutex::new(Vec::new());
    static INSTALLED: OnceLock<()> = OnceLock::new();

    /// Installs the process-wide capture once; Worker runtime threads log through it.
    pub(crate) fn install() {
        INSTALLED.get_or_init(|| {
            tracing::subscriber::set_global_default(CapturingSubscriber)
                .expect("no other global tracing subscriber in the integ tests");
        });
    }

    /// Returns the WARN lines that mention `app_worker_id` and contain every fragment.
    pub(crate) fn warnings_with(app_worker_id: &str, fragments: &[&str]) -> usize {
        LINES
            .lock()
            .expect("captured log lock")
            .iter()
            .filter(|line| {
                line.starts_with("WARN ")
                    && line.contains(app_worker_id)
                    && fragments.iter().all(|fragment| line.contains(fragment))
            })
            .count()
    }

    struct CapturingSubscriber;

    struct LineVisitor(String);

    impl tracing::field::Visit for LineVisitor {
        fn record_debug(&mut self, field: &tracing::field::Field, value: &dyn std::fmt::Debug) {
            write!(self.0, " {}={value:?}", field.name()).expect("format log field");
        }
    }

    impl tracing::Subscriber for CapturingSubscriber {
        fn enabled(&self, metadata: &tracing::Metadata<'_>) -> bool {
            *metadata.level() <= tracing::Level::INFO
        }
        fn new_span(&self, _: &tracing::span::Attributes<'_>) -> tracing::span::Id {
            tracing::span::Id::from_u64(1)
        }
        fn record(&self, _: &tracing::span::Id, _: &tracing::span::Record<'_>) {}
        fn record_follows_from(&self, _: &tracing::span::Id, _: &tracing::span::Id) {}
        fn event(&self, event: &tracing::Event<'_>) {
            let mut visitor = LineVisitor(format!("{} ", event.metadata().level()));
            event.record(&mut visitor);
            LINES.lock().expect("captured log lock").push(visitor.0);
        }
        fn enter(&self, _: &tracing::span::Id) {}
        fn exit(&self, _: &tracing::span::Id) {}
    }
}
