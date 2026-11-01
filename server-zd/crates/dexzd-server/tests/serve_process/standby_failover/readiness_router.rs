// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A loopback Service that routes to the SERVING process and samples readiness and lease epochs.

use std::fmt::Write as _;
use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tokio::io::copy_bidirectional;
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::watch;
use tokio::task::{JoinHandle, JoinSet};
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use crate::metrics::try_scrape;

/// Service names that readiness probes check.
const READINESS_SERVICE_NAMES: [&str; 3] = ["", "dex.FlowService", "dex.WorkerTaskService"];
const PROBE_INTERVAL: Duration = Duration::from_millis(25);
/// A frozen or stalled process answers no probe; this bounds how long one probe waits for it.
const PROBE_TIMEOUT: Duration = Duration::from_millis(250);
/// Overlapping SERVING answers this fast count as simultaneous; every handover gap is longer than twice this.
const SIMULTANEOUS_ANSWER_LIMIT: Duration = Duration::from_millis(5);
/// Unanswered probes in a row, about 2 s, after which relayed connections close like a client keepalive.
const UNREACHABLE_PROBES_BEFORE_CUT: u32 = 8;
/// The OpenMetrics sample of the epoch a process holds, zero without the lease.
const LEASE_EPOCH_SAMPLE_PREFIX: &str = "dexzd_lease_epoch ";

/// One process position that the router probes and routes to.
#[derive(Clone, Debug)]
pub(crate) struct RouterBackend {
    pub(crate) name: String,
    pub(crate) api_address: SocketAddr,
    pub(crate) metrics_address: SocketAddr,
}

/// What one readiness probe of one process found.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Readiness {
    /// Every readiness name was SERVING under the lease epoch the process reported.
    Serving {
        epoch: u64,
    },
    NotServing,
    Unreachable,
}

/// One probe: the process answered at some instant between `sent_at` and `received_at`.
#[derive(Clone, Copy, Debug)]
struct ReadinessObservation {
    backend: usize,
    /// Counts process starts of the backend; each generation is one process.
    generation: usize,
    sent_at: Instant,
    received_at: Instant,
    readiness: Readiness,
}

impl ReadinessObservation {
    fn overlaps(&self, other: &Self) -> bool {
        self.sent_at <= other.received_at && other.sent_at <= self.received_at
    }

    fn lies_within(&self, start: Instant, end: Instant) -> bool {
        start <= self.sent_at && self.received_at <= end
    }

    fn serving_epoch(&self) -> Option<u64> {
        match self.readiness {
            Readiness::Serving { epoch } => Some(epoch),
            Readiness::NotServing | Readiness::Unreachable => None,
        }
    }
}

/// Counts from a passing readiness check.
#[derive(Debug)]
pub(crate) struct ServingReport {
    pub(crate) observations: usize,
    pub(crate) serving_observations: usize,
    /// Distinct processes and lease epochs that reported SERVING.
    pub(crate) serving_periods: usize,
    /// SERVING answers from a process that reported no lease epoch, such as one that just lost it.
    pub(crate) serving_without_lease: usize,
}

/// The latest probe of each backend and the backend that new connections go to.
struct RouteState {
    latest: Vec<Option<ReadinessObservation>>,
    unreachable_streaks: Vec<u32>,
    route: Option<usize>,
}

/// Probed processes, their readiness history, and the current route.
struct RouterState {
    backends: Vec<RouterBackend>,
    started_at: Instant,
    /// Per backend, the instant each generation's process was started.
    generation_starts: Mutex<Vec<Vec<Instant>>>,
    observations: Mutex<Vec<ReadinessObservation>>,
    route: Mutex<RouteState>,
    /// Per backend; a new value closes every connection relayed to that backend.
    connection_cuts: Vec<watch::Sender<u64>>,
}

impl RouterState {
    fn current_generation(&self, backend: usize) -> usize {
        self.generation_starts
            .lock()
            .expect("the generation lock is not poisoned")[backend]
            .len()
    }

    /// Records `observation` and updates the route; returns whether to cut the backend's connections.
    fn record(&self, observation: ReadinessObservation) -> bool {
        let backend = observation.backend;
        // A probe of an earlier process may finish after a restart; it must not route to the new one.
        let is_current_process = observation.generation == self.current_generation(backend);
        let mut route_state = self.route.lock().expect("the route lock is not poisoned");
        if !is_current_process {
            drop(route_state);
            self.observations
                .lock()
                .expect("the observation lock is not poisoned")
                .push(observation);
            return false;
        }
        let streak = &mut route_state.unreachable_streaks[backend];
        *streak = if observation.readiness == Readiness::Unreachable {
            *streak + 1
        } else {
            0
        };
        let should_cut = *streak == UNREACHABLE_PROBES_BEFORE_CUT;
        route_state.latest[backend] = Some(observation);
        let keeps_route = route_state.route.is_some_and(|routed| {
            route_state.latest[routed].is_some_and(|latest| match latest.readiness {
                Readiness::Serving { .. } => true,
                Readiness::NotServing => false,
                Readiness::Unreachable => {
                    route_state.unreachable_streaks[routed] < UNREACHABLE_PROBES_BEFORE_CUT
                }
            })
        });
        if !keeps_route {
            route_state.route = route_state
                .latest
                .iter()
                .flatten()
                .find(|latest| latest.serving_epoch().is_some())
                .map(|latest| latest.backend);
        }
        drop(route_state);
        self.observations
            .lock()
            .expect("the observation lock is not poisoned")
            .push(observation);
        should_cut
    }
}

/// A loopback TCP proxy that routes new connections to the last SERVING process, else closes them.
pub(crate) struct ReadinessRouter {
    address: SocketAddr,
    state: Arc<RouterState>,
    stop: CancellationToken,
    tasks: Vec<JoinHandle<()>>,
}

impl ReadinessRouter {
    /// Starts probing each of `backends` on its own and accepting connections.
    pub(crate) async fn start(backends: Vec<RouterBackend>) -> Self {
        let listener = TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
            .await
            .expect("the router binds a loopback port");
        let address = listener.local_addr().expect("the router has an address");
        let backend_count = backends.len();
        let state = Arc::new(RouterState {
            generation_starts: Mutex::new(vec![Vec::new(); backend_count]),
            connection_cuts: backends.iter().map(|_| watch::channel(0).0).collect(),
            backends,
            started_at: Instant::now(),
            observations: Mutex::new(Vec::new()),
            route: Mutex::new(RouteState {
                latest: vec![None; backend_count],
                unreachable_streaks: vec![0; backend_count],
                route: None,
            }),
        });
        let stop = CancellationToken::new();
        let mut tasks: Vec<JoinHandle<()>> = Vec::with_capacity(backend_count + 1);
        for backend in 0..backend_count {
            let prober = tokio::spawn(probe_backend_until_stopped(
                state.clone(),
                backend,
                stop.clone(),
            ));
            tasks.push(prober);
        }
        let acceptor = tokio::spawn(accept_connections(listener, state.clone(), stop.clone()));
        tasks.push(acceptor);
        Self {
            address,
            state,
            stop,
            tasks,
        }
    }

    /// Returns the Service address that clients and Workers connect to.
    pub(crate) fn address(&self) -> SocketAddr {
        self.address
    }

    /// Records that a new process of `backend` starts now, so its probes are told apart from earlier ones.
    pub(crate) fn note_process_start(&self, backend: usize) {
        self.state
            .generation_starts
            .lock()
            .expect("the generation lock is not poisoned")[backend]
            .push(Instant::now());
        // Like a restarted container, a new process takes no connections before it reports SERVING.
        let mut route_state = self
            .state
            .route
            .lock()
            .expect("the route lock is not poisoned");
        route_state.latest[backend] = None;
        route_state.unreachable_streaks[backend] = 0;
        if route_state.route == Some(backend) {
            route_state.route = None;
        }
    }

    /// Returns the backend that new connections go to, which last reported SERVING.
    pub(crate) fn serving_backend(&self) -> Option<usize> {
        self.state
            .route
            .lock()
            .expect("the route lock is not poisoned")
            .route
    }

    /// Returns whether the latest probe of `backend`'s current process found it SERVING.
    pub(crate) fn is_serving(&self, backend: usize) -> bool {
        let generation = self.state.current_generation(backend);
        self.state
            .route
            .lock()
            .expect("the route lock is not poisoned")
            .latest[backend]
            .is_some_and(|latest| {
                latest.generation == generation && latest.serving_epoch().is_some()
            })
    }

    /// Stops probing and routing, then panics if two processes reported SERVING at one instant.
    pub(crate) async fn shutdown_and_check(mut self) -> ServingReport {
        self.stop.cancel();
        for task in self.tasks.drain(..) {
            task.await.expect("a router task does not panic");
        }
        let observations = self
            .state
            .observations
            .lock()
            .expect("the observation lock is not poisoned")
            .clone();
        let generation_starts = self
            .state
            .generation_starts
            .lock()
            .expect("the generation lock is not poisoned")
            .clone();
        ServingHistory {
            names: self
                .state
                .backends
                .iter()
                .map(|backend| backend.name.clone())
                .collect(),
            started_at: self.state.started_at,
            observations,
            generation_starts,
        }
        .check()
    }
}

impl Drop for ReadinessRouter {
    fn drop(&mut self) {
        self.stop.cancel();
        for task in &self.tasks {
            task.abort();
        }
    }
}

/// Probes one backend every interval, so a frozen backend never slows the others.
async fn probe_backend_until_stopped(
    state: Arc<RouterState>,
    backend: usize,
    stop: CancellationToken,
) {
    let mut channel: Option<(usize, Channel)> = None;
    loop {
        tokio::select! {
            () = stop.cancelled() => return,
            () = tokio::time::sleep(PROBE_INTERVAL) => {}
        }
        let generation = state.current_generation(backend);
        let probe_channel = match &channel {
            Some((channel_generation, existing)) if *channel_generation == generation => {
                existing.clone()
            }
            // A new process gets a new connection, so no answer of the old one is reused.
            _ => {
                let created =
                    Channel::from_shared(format!("http://{}", state.backends[backend].api_address))
                        .expect("the backend address is a valid URI")
                        .connect_lazy();
                channel = Some((generation, created.clone()));
                created
            }
        };
        let observation = probe_backend(
            backend,
            generation,
            probe_channel,
            state.backends[backend].metrics_address,
        )
        .await;
        if state.record(observation) {
            state.connection_cuts[backend].send_modify(|cuts| *cuts += 1);
        }
    }
}

async fn probe_backend(
    backend: usize,
    generation: usize,
    channel: Channel,
    metrics_address: SocketAddr,
) -> ReadinessObservation {
    let sent_at = Instant::now();
    let health = HealthClient::new(channel);
    let checks = READINESS_SERVICE_NAMES.map(|service| {
        let mut health = health.clone();
        async move {
            health
                .check(HealthCheckRequest {
                    service: service.to_owned(),
                })
                .await
        }
    });
    let probe = async {
        tokio::join!(
            futures::future::try_join_all(checks),
            try_scrape(metrics_address, "/metrics")
        )
    };
    let readiness = match tokio::time::timeout(PROBE_TIMEOUT, probe).await {
        Ok((Ok(responses), metrics)) => {
            if responses
                .iter()
                .all(|response| response.get_ref().status == ServingStatus::Serving as i32)
            {
                // An epoch that cannot be read gives the answer no period, like a lost probe.
                metrics
                    .ok()
                    .and_then(|metrics| lease_epoch(&metrics))
                    .map_or(Readiness::Unreachable, |epoch| Readiness::Serving { epoch })
            } else {
                Readiness::NotServing
            }
        }
        // A process that is down, starting, frozen, or stalled answers nothing in time.
        Ok((Err(_), _)) | Err(_) => Readiness::Unreachable,
    };
    ReadinessObservation {
        backend,
        generation,
        sent_at,
        received_at: Instant::now(),
        readiness,
    }
}

/// Returns the `dexzd_lease_epoch` sample of an OpenMetrics response.
fn lease_epoch(metrics: &str) -> Option<u64> {
    metrics
        .lines()
        .find_map(|line| line.strip_prefix(LEASE_EPOCH_SAMPLE_PREFIX))
        .and_then(|value| value.trim().parse().ok())
}

/// Relays each accepted connection to the routed backend until either side or a cut closes it.
async fn accept_connections(
    listener: TcpListener,
    state: Arc<RouterState>,
    stop: CancellationToken,
) {
    let mut relays = JoinSet::new();
    loop {
        tokio::select! {
            () = stop.cancelled() => break,
            accepted = listener.accept() => {
                let (incoming, _) = accepted.expect("the router accepts connections");
                let route = state.route.lock().expect("the route lock is not poisoned").route;
                // With no serving process the connection closes, as a Service without endpoints refuses it.
                let Some(backend) = route else {
                    drop(incoming);
                    continue;
                };
                relays.spawn(relay_connection(
                    incoming,
                    state.backends[backend].api_address,
                    state.connection_cuts[backend].subscribe(),
                    stop.clone(),
                ));
            }
            Some(finished) = relays.join_next(), if !relays.is_empty() => {
                finished.expect("a relay task does not panic");
            }
        }
    }
    relays.shutdown().await;
}

async fn relay_connection(
    mut incoming: TcpStream,
    backend: SocketAddr,
    mut cuts: watch::Receiver<u64>,
    stop: CancellationToken,
) {
    // A backend that stopped listening refuses; closing `incoming` reports that to the client.
    let Ok(mut outgoing) = TcpStream::connect(backend).await else {
        return;
    };
    tokio::select! {
        // Either side closing, or resetting, ends the relay the same way.
        _copied = copy_bidirectional(&mut incoming, &mut outgoing) => {}
        changed = cuts.changed() => {
            changed.expect("the router keeps its cut senders");
        }
        () = stop.cancelled() => {}
    }
}

/// Every readiness answer of one test and the process start instants of each backend.
struct ServingHistory {
    names: Vec<String>,
    started_at: Instant,
    observations: Vec<ReadinessObservation>,
    generation_starts: Vec<Vec<Instant>>,
}

/// The span in which one process surely reported SERVING throughout one lease epoch.
struct ServingPeriod {
    backend: usize,
    generation: usize,
    epoch: u64,
    first_answer: Instant,
    last_probe: Instant,
}

impl ServingHistory {
    /// Panics with every proven overlap of two SERVING processes.
    fn check(self) -> ServingReport {
        let observations: Vec<ReadinessObservation> = self
            .observations
            .iter()
            .copied()
            .filter(|observation| !self.spans_a_restart(observation))
            .collect();
        let serving: Vec<&ReadinessObservation> = observations
            .iter()
            .filter(|observation| observation.serving_epoch().is_some())
            .collect();
        let periods = self.serving_periods(&serving);
        let mut violations = String::new();
        // One epoch is one unbroken serving period, so any answer inside its span overlaps it.
        for period in periods
            .iter()
            .filter(|period| period.first_answer < period.last_probe)
        {
            for observation in &observations {
                if !observation.lies_within(period.first_answer, period.last_probe) {
                    continue;
                }
                let is_same_process = observation.backend == period.backend
                    && observation.generation == period.generation;
                if !is_same_process && observation.serving_epoch().is_some() {
                    let violation = format!(
                        "served while {} served epoch {} from {} to {}",
                        self.process_name(period.backend, period.generation),
                        period.epoch,
                        self.offset(period.first_answer),
                        self.offset(period.last_probe)
                    );
                    self.record_violation(&mut violations, observation, &violation);
                }
                if is_same_process && observation.readiness == Readiness::NotServing {
                    let violation = format!(
                        "reported NOT_SERVING inside its epoch {} serving period from {} to {}",
                        period.epoch,
                        self.offset(period.first_answer),
                        self.offset(period.last_probe)
                    );
                    self.record_violation(&mut violations, observation, &violation);
                }
            }
        }
        self.record_simultaneous_answers(&serving, &mut violations);
        assert!(
            violations.is_empty(),
            "two processes served at one instant:\n{violations}"
        );
        ServingReport {
            observations: self.observations.len(),
            serving_observations: serving.len(),
            serving_periods: periods.len(),
            serving_without_lease: serving
                .iter()
                .filter(|observation| observation.serving_epoch() == Some(0))
                .count(),
        }
    }

    /// Records fast SERVING answers of two processes whose probes overlap.
    fn record_simultaneous_answers(
        &self,
        serving: &[&ReadinessObservation],
        violations: &mut String,
    ) {
        let mut fast: Vec<&ReadinessObservation> = serving
            .iter()
            .copied()
            .filter(|observation| {
                observation.received_at - observation.sent_at <= SIMULTANEOUS_ANSWER_LIMIT
            })
            .collect();
        fast.sort_by_key(|observation| observation.sent_at);
        for (index, observation) in fast.iter().enumerate() {
            for later in &fast[index + 1..] {
                if later.sent_at > observation.received_at {
                    break;
                }
                let is_other_process = later.backend != observation.backend
                    || later.generation != observation.generation;
                if is_other_process && later.overlaps(observation) {
                    let violation = format!(
                        "answered SERVING while {} answered SERVING between {} and {}",
                        self.process_name(observation.backend, observation.generation),
                        self.offset(observation.sent_at),
                        self.offset(observation.received_at)
                    );
                    self.record_violation(violations, later, &violation);
                }
            }
        }
    }

    /// Returns whether a later process of the backend may have answered a probe sent to an earlier one.
    fn spans_a_restart(&self, observation: &ReadinessObservation) -> bool {
        self.generation_starts[observation.backend]
            .get(observation.generation)
            .is_some_and(|next_start| *next_start <= observation.received_at)
    }

    /// Returns one period for each process and lease epoch that reported SERVING.
    fn serving_periods(&self, serving: &[&ReadinessObservation]) -> Vec<ServingPeriod> {
        let mut periods: Vec<ServingPeriod> = Vec::new();
        for observation in serving {
            let epoch = observation
                .serving_epoch()
                .expect("only SERVING observations form periods");
            // Health can lag a lost lease briefly; such answers still count against other periods.
            if epoch == 0 {
                continue;
            }
            match periods.iter_mut().find(|period| {
                period.backend == observation.backend
                    && period.generation == observation.generation
                    && period.epoch == epoch
            }) {
                Some(period) => {
                    period.first_answer = period.first_answer.min(observation.received_at);
                    period.last_probe = period.last_probe.max(observation.sent_at);
                }
                None => periods.push(ServingPeriod {
                    backend: observation.backend,
                    generation: observation.generation,
                    epoch,
                    first_answer: observation.received_at,
                    last_probe: observation.sent_at,
                }),
            }
        }
        periods
    }

    fn record_violation(
        &self,
        violations: &mut String,
        observation: &ReadinessObservation,
        violation: &str,
    ) {
        writeln!(
            violations,
            "  {} between {} and {} {violation}",
            self.process_name(observation.backend, observation.generation),
            self.offset(observation.sent_at),
            self.offset(observation.received_at),
        )
        .expect("writing to a String cannot fail");
    }

    fn process_name(&self, backend: usize, generation: usize) -> String {
        format!("{} process {generation}", self.names[backend])
    }

    /// Returns `instant` as the time since the router started.
    fn offset(&self, instant: Instant) -> String {
        format!("{:?}", instant - self.started_at)
    }
}

/// Builds a history of two backends, each started once at `start`, from `(backend, sent, received, readiness)`.
#[cfg(test)]
fn synthetic_history(start: Instant, answers: &[(usize, u64, u64, Readiness)]) -> ServingHistory {
    let at = |milliseconds: u64| start + Duration::from_millis(milliseconds);
    ServingHistory {
        names: vec!["first".to_owned(), "second".to_owned()],
        started_at: start,
        observations: answers
            .iter()
            .map(
                |&(backend, sent, received, readiness)| ReadinessObservation {
                    backend,
                    generation: 1,
                    sent_at: at(sent),
                    received_at: at(received),
                    readiness,
                },
            )
            .collect(),
        generation_starts: vec![vec![start], vec![start]],
    }
}

/// A clean handover, health lagging a lost lease, and a second epoch of one process pass.
#[test]
fn a_clean_handover_and_a_later_epoch_pass_the_serving_check() {
    let start = Instant::now();
    let report = synthetic_history(
        start,
        &[
            (0, 0, 2, Readiness::Serving { epoch: 1 }),
            (1, 0, 2, Readiness::NotServing),
            (0, 30, 32, Readiness::Serving { epoch: 1 }),
            (0, 45, 47, Readiness::Serving { epoch: 0 }),
            (0, 60, 62, Readiness::NotServing),
            (1, 90, 92, Readiness::Serving { epoch: 2 }),
            (1, 120, 122, Readiness::Serving { epoch: 2 }),
            (1, 150, 152, Readiness::NotServing),
            (0, 180, 182, Readiness::Serving { epoch: 3 }),
            (0, 210, 212, Readiness::Serving { epoch: 3 }),
        ],
    )
    .check();
    assert_eq!(report.serving_periods, 3);
    assert_eq!(report.serving_without_lease, 1);
}

/// A SERVING answer inside another process's serving span fails the check.
#[test]
#[should_panic(expected = "two processes served at one instant")]
fn an_answer_inside_another_serving_period_fails_the_serving_check() {
    let start = Instant::now();
    synthetic_history(
        start,
        &[
            (0, 0, 2, Readiness::Serving { epoch: 1 }),
            (1, 40, 60, Readiness::Serving { epoch: 2 }),
            (0, 90, 92, Readiness::Serving { epoch: 1 }),
        ],
    )
    .check();
}

/// Two fast overlapping SERVING answers of different processes fail the check.
#[test]
#[should_panic(expected = "two processes served at one instant")]
fn overlapping_fast_answers_fail_the_serving_check() {
    let start = Instant::now();
    synthetic_history(
        start,
        &[
            (0, 10, 12, Readiness::Serving { epoch: 1 }),
            (1, 11, 13, Readiness::Serving { epoch: 2 }),
        ],
    )
    .check();
}

/// A process that stops and resumes serving within one epoch fails the check.
#[test]
#[should_panic(expected = "reported NOT_SERVING inside its epoch 1 serving period")]
fn not_serving_inside_one_epoch_fails_the_serving_check() {
    let start = Instant::now();
    synthetic_history(
        start,
        &[
            (0, 0, 2, Readiness::Serving { epoch: 1 }),
            (0, 30, 32, Readiness::NotServing),
            (0, 60, 62, Readiness::Serving { epoch: 1 }),
        ],
    )
    .check();
}
