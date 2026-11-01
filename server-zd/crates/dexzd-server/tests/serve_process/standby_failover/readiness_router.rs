// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A loopback Service that sends each new connection to the process reporting SERVING.
//!
//! It samples every process's gRPC readiness, so a test can check that at most one served at once.

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

/// Service names that readiness probes check.
const READINESS_SERVICE_NAMES: [&str; 3] = ["", "dex.FlowService", "dex.WorkerTaskService"];
const PROBE_INTERVAL: Duration = Duration::from_millis(25);
/// A frozen or stalled process answers no probe; this bounds how long one round waits for it.
const PROBE_TIMEOUT: Duration = Duration::from_millis(250);
/// Two SERVING answers this fast that overlap prove two processes served at one instant.
///
/// A handover takes more than twice this long, so the rule cannot flag a clean handover.
const CONCLUSIVE_PROBE_DURATION: Duration = Duration::from_millis(5);
/// Unanswered probes in a row after which relayed connections close, like a client keepalive.
const UNREACHABLE_PROBES_BEFORE_CUT: u32 = 2;

/// What one readiness probe of one process found.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Readiness {
    Serving,
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
}

/// Counts from a passing readiness check.
#[derive(Debug)]
pub(crate) struct ServingReport {
    pub(crate) observations: usize,
    pub(crate) serving_observations: usize,
    pub(crate) serving_periods: usize,
}

/// Probed processes, their readiness history, and the current route.
struct RouterState {
    names: Vec<String>,
    backends: Vec<SocketAddr>,
    started_at: Instant,
    /// Per backend, the instant each generation's process was started.
    generation_starts: Mutex<Vec<Vec<Instant>>>,
    observations: Mutex<Vec<ReadinessObservation>>,
    route: Mutex<Option<usize>>,
    /// Per backend; a new value closes every connection relayed to that backend.
    connection_cuts: Vec<watch::Sender<u64>>,
}

/// A TCP proxy on a loopback port that routes like a Service whose endpoints follow readiness.
///
/// A new connection goes to the process that last reported SERVING; with none, it closes at once.
pub(crate) struct ReadinessRouter {
    address: SocketAddr,
    state: Arc<RouterState>,
    stop: CancellationToken,
    tasks: Vec<JoinHandle<()>>,
}

impl ReadinessRouter {
    /// Starts probing `backends`, each a name and an API address, and accepting connections.
    pub(crate) async fn start(backends: Vec<(String, SocketAddr)>) -> Self {
        let listener = TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
            .await
            .expect("the router binds a loopback port");
        let address = listener.local_addr().expect("the router has an address");
        let (names, backends): (Vec<_>, Vec<_>) = backends.into_iter().unzip();
        let state = Arc::new(RouterState {
            generation_starts: Mutex::new(vec![Vec::new(); backends.len()]),
            connection_cuts: backends.iter().map(|_| watch::channel(0).0).collect(),
            names,
            backends,
            started_at: Instant::now(),
            observations: Mutex::new(Vec::new()),
            route: Mutex::new(None),
        });
        let stop = CancellationToken::new();
        let tasks = vec![
            tokio::spawn(probe_backends(state.clone(), stop.clone())),
            tokio::spawn(accept_connections(listener, state.clone(), stop.clone())),
        ];
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
    }

    /// Returns the backend that new connections go to, which last reported SERVING.
    pub(crate) fn serving_backend(&self) -> Option<usize> {
        *self
            .state
            .route
            .lock()
            .expect("the route lock is not poisoned")
    }

    /// Returns whether the latest probe of `backend`'s current process found it SERVING.
    pub(crate) fn is_serving(&self, backend: usize) -> bool {
        let generation = self
            .state
            .generation_starts
            .lock()
            .expect("the generation lock is not poisoned")[backend]
            .len();
        self.state
            .observations
            .lock()
            .expect("the observation lock is not poisoned")
            .iter()
            .rev()
            .find(|observation| observation.backend == backend)
            .is_some_and(|observation| {
                observation.generation == generation && observation.readiness == Readiness::Serving
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
            names: &self.state.names,
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

/// Probes every backend each round, records the answers, and updates the route and cuts.
async fn probe_backends(state: Arc<RouterState>, stop: CancellationToken) {
    let backend_count = state.backends.len();
    let mut channels: Vec<Option<(usize, Channel)>> = vec![None; backend_count];
    let mut unreachable_streaks = vec![0_u32; backend_count];
    loop {
        tokio::select! {
            () = stop.cancelled() => return,
            () = tokio::time::sleep(PROBE_INTERVAL) => {}
        }
        let generations: Vec<usize> = state
            .generation_starts
            .lock()
            .expect("the generation lock is not poisoned")
            .iter()
            .map(Vec::len)
            .collect();
        let probes = (0..backend_count).map(|backend| {
            let generation = generations[backend];
            let channel = match &channels[backend] {
                Some((channel_generation, channel)) if *channel_generation == generation => {
                    channel.clone()
                }
                // A new process gets a new connection, so no answer of the old one is reused.
                _ => {
                    let channel =
                        Channel::from_shared(format!("http://{}", state.backends[backend]))
                            .expect("the backend address is a valid URI")
                            .connect_lazy();
                    channels[backend] = Some((generation, channel.clone()));
                    channel
                }
            };
            probe_backend(backend, generation, channel)
        });
        let round = futures::future::join_all(probes).await;
        for observation in &round {
            let streak = &mut unreachable_streaks[observation.backend];
            if observation.readiness == Readiness::Unreachable {
                *streak += 1;
                if *streak == UNREACHABLE_PROBES_BEFORE_CUT {
                    state.connection_cuts[observation.backend].send_modify(|cuts| *cuts += 1);
                }
            } else {
                *streak = 0;
            }
        }
        update_route(&state, &round, &unreachable_streaks);
        state
            .observations
            .lock()
            .expect("the observation lock is not poisoned")
            .extend(round);
    }
}

/// Keeps the route on a backend that still serves or only missed a probe; else picks a serving one.
fn update_route(state: &RouterState, round: &[ReadinessObservation], unreachable_streaks: &[u32]) {
    let mut route = state.route.lock().expect("the route lock is not poisoned");
    let keeps_route = route.is_some_and(|backend| {
        let readiness = round[backend].readiness;
        readiness == Readiness::Serving
            || (readiness == Readiness::Unreachable
                && unreachable_streaks[backend] < UNREACHABLE_PROBES_BEFORE_CUT)
    });
    if !keeps_route {
        *route = round
            .iter()
            .find(|observation| observation.readiness == Readiness::Serving)
            .map(|observation| observation.backend);
    }
}

async fn probe_backend(
    backend: usize,
    generation: usize,
    channel: Channel,
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
    let readiness =
        match tokio::time::timeout(PROBE_TIMEOUT, futures::future::try_join_all(checks)).await {
            Ok(Ok(responses)) => {
                if responses
                    .iter()
                    .all(|response| response.get_ref().status == ServingStatus::Serving as i32)
                {
                    Readiness::Serving
                } else {
                    Readiness::NotServing
                }
            }
            // A process that is down, starting, frozen, or stalled answers nothing in time.
            Ok(Err(_)) | Err(_) => Readiness::Unreachable,
        };
    ReadinessObservation {
        backend,
        generation,
        sent_at,
        received_at: Instant::now(),
        readiness,
    }
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
                let route = *state.route.lock().expect("the route lock is not poisoned");
                // With no serving process the connection closes, as a Service without endpoints refuses it.
                let Some(backend) = route else {
                    drop(incoming);
                    continue;
                };
                relays.spawn(relay_connection(
                    incoming,
                    state.backends[backend],
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
struct ServingHistory<'a> {
    names: &'a [String],
    started_at: Instant,
    observations: Vec<ReadinessObservation>,
    generation_starts: Vec<Vec<Instant>>,
}

impl ServingHistory<'_> {
    /// Panics with every proven overlap of two SERVING processes.
    ///
    /// A process serves one unbroken period, from its first SERVING answer to its last.
    ///
    /// Another process answering SERVING inside that span proves both served at one instant.
    ///
    /// Two fast overlapping SERVING answers prove it too.
    fn check(self) -> ServingReport {
        let observations: Vec<ReadinessObservation> = self
            .observations
            .iter()
            .copied()
            .filter(|observation| !self.spans_a_restart(observation))
            .collect();
        let serving: Vec<&ReadinessObservation> = observations
            .iter()
            .filter(|observation| observation.readiness == Readiness::Serving)
            .collect();
        let mut violations = String::new();
        let periods = self.serving_periods(&serving);
        for period in &periods {
            for observation in &observations {
                if !observation.lies_within(period.first_answer, period.last_probe) {
                    continue;
                }
                if observation.backend != period.backend
                    && observation.readiness == Readiness::Serving
                {
                    let violation = format!(
                        "served while {} served from {} to {}",
                        self.process_name(period.backend, period.generation),
                        self.offset(period.first_answer),
                        self.offset(period.last_probe)
                    );
                    self.record_violation(&mut violations, observation, &violation);
                }
                if observation.backend == period.backend
                    && observation.generation == period.generation
                    && observation.readiness == Readiness::NotServing
                {
                    let violation = format!(
                        "reported NOT_SERVING inside its serving period from {} to {}",
                        self.offset(period.first_answer),
                        self.offset(period.last_probe)
                    );
                    self.record_violation(&mut violations, observation, &violation);
                }
            }
        }
        let mut conclusive: Vec<&ReadinessObservation> = serving
            .iter()
            .copied()
            .filter(|observation| {
                observation.received_at - observation.sent_at <= CONCLUSIVE_PROBE_DURATION
            })
            .collect();
        conclusive.sort_by_key(|observation| observation.sent_at);
        for (index, observation) in conclusive.iter().enumerate() {
            for later in &conclusive[index + 1..] {
                if later.sent_at > observation.received_at {
                    break;
                }
                if later.backend != observation.backend && later.overlaps(observation) {
                    let violation = format!(
                        "answered SERVING while {} answered SERVING between {} and {}",
                        self.process_name(observation.backend, observation.generation),
                        self.offset(observation.sent_at),
                        self.offset(observation.received_at)
                    );
                    self.record_violation(&mut violations, later, &violation);
                }
            }
        }
        assert!(
            violations.is_empty(),
            "two processes served at one instant:\n{violations}"
        );
        ServingReport {
            observations: self.observations.len(),
            serving_observations: serving.len(),
            serving_periods: periods.len(),
        }
    }

    /// Returns whether a later process of the backend may have answered a probe sent to an earlier one.
    fn spans_a_restart(&self, observation: &ReadinessObservation) -> bool {
        self.generation_starts[observation.backend]
            .get(observation.generation)
            .is_some_and(|next_start| *next_start <= observation.received_at)
    }

    /// Returns the span each process surely served: from its first SERVING answer to its last probe.
    fn serving_periods(&self, serving: &[&ReadinessObservation]) -> Vec<ServingPeriod> {
        let mut periods: Vec<ServingPeriod> = Vec::new();
        for observation in serving {
            match periods.iter_mut().find(|period| {
                period.backend == observation.backend && period.generation == observation.generation
            }) {
                Some(period) => {
                    period.first_answer = period.first_answer.min(observation.received_at);
                    period.last_probe = period.last_probe.max(observation.sent_at);
                }
                None => periods.push(ServingPeriod {
                    backend: observation.backend,
                    generation: observation.generation,
                    first_answer: observation.received_at,
                    last_probe: observation.sent_at,
                }),
            }
        }
        periods.retain(|period| period.first_answer < period.last_probe);
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

/// The span in which one process surely reported SERVING throughout.
struct ServingPeriod {
    backend: usize,
    generation: usize,
    first_answer: Instant,
    last_probe: Instant,
}
