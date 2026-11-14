// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Idle vault Flows whose large Attributes, hard-delete values, and Channel messages the soak
//! writes, records, and reads back after every fault.

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, ChannelMessage, FlowAlreadyStartedOptions, FlowConfig, FlowStartOptions,
    FlowStatus, GetAttributesRequest, GetChannelMessagesRequest, GetFlowSummaryRequest, Kv,
    LoadBlobRequestEntry, LoadBlobsRequest, PublishToChannelRequest, SetAttributesRequest,
    StartFlowRequest, StepOptions, StopFlowRequest, StopType, Value, WorkerTarget, value,
};
use tokio::time::Instant;
use tonic::transport::Channel;

/// No Worker polls this type, so each vault Flow stays open until the soak stops it.
const VAULT_FLOW_TYPE: &str = "SoakVaultFlow";
const VAULT_STEP_TYPE: &str = "VaultStep";
const LARGE_ATTRIBUTE: &str = "soak-large";
const SECRET_ATTRIBUTE: &str = "soak-secret";
const MESSAGE_CHANNEL: &str = "soak-messages";
const LARGE_VALUE_BYTES: usize = 64 * 1024;
const SECRET_VALUE_BYTES: usize = 16 * 1024;
/// Open vault Flows at most; a new one starts while fewer are open.
const MAX_OPEN_VAULTS: usize = 6;
/// Large Attributes rewritten per round.
const REWRITES_PER_ROUND: usize = 2;
const CALL_TIMEOUT: Duration = Duration::from_secs(10);
/// Covers a takeover or a cold restart, after which every call must succeed again.
const ACKNOWLEDGEMENT_BUDGET: Duration = Duration::from_secs(120);
const RETRY_PAUSE: Duration = Duration::from_millis(250);

/// Whether a vault Flow's hard-delete value is stored or was erased.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum SecretState {
    Stored,
    Erased,
}

/// What the soak knows was acknowledged for one vault Flow.
#[derive(Debug)]
struct VaultRecord {
    flow_id: String,
    run_id: String,
    large_version: u64,
    secret: SecretState,
    is_closed: bool,
    /// Messages whose publish returned success.
    acknowledged_messages: BTreeSet<String>,
    /// Messages sent at least once; a resent one may appear twice.
    sent_messages: BTreeSet<String>,
    resent_messages: BTreeSet<String>,
}

/// Counts of everything the vault wrote and checked.
#[derive(Debug, Default)]
pub(crate) struct VaultReport {
    pub(crate) flows_started: usize,
    pub(crate) flows_closed: usize,
    pub(crate) large_writes: usize,
    pub(crate) secrets_erased: usize,
    pub(crate) messages_acknowledged: usize,
    pub(crate) messages_resent: usize,
    pub(crate) checks: usize,
}

impl fmt::Display for VaultReport {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{} vault Flows started, {} closed; {} large Attribute writes; {} hard-delete values erased; {} Channel messages acknowledged, {} resent; {} full checks",
            self.flows_started,
            self.flows_closed,
            self.large_writes,
            self.secrets_erased,
            self.messages_acknowledged,
            self.messages_resent,
            self.checks
        )
    }
}

/// The vault Flows of one soak run and the ledger of their acknowledged writes.
pub(crate) struct Vault {
    flow_id_prefix: String,
    records: Vec<VaultRecord>,
    next_flow_number: usize,
    next_message_number: u64,
    next_request_number: u64,
    rewrite_cursor: usize,
    report: VaultReport,
}

impl Vault {
    pub(crate) fn new(run_id: &str) -> Self {
        Self {
            flow_id_prefix: format!("vault-{run_id}"),
            records: Vec::new(),
            next_flow_number: 0,
            next_message_number: 0,
            next_request_number: 0,
            rewrite_cursor: 0,
            report: VaultReport::default(),
        }
    }

    pub(crate) fn report(&self) -> &VaultReport {
        &self.report
    }

    /// Starts a vault Flow while few are open, rewrites some large Attributes, and publishes one
    /// message to every open vault Flow.
    ///
    /// # Errors
    ///
    /// Returns a description when a write is not acknowledged within the budget.
    pub(crate) async fn run_round(&mut self, channel: &Channel) -> Result<(), String> {
        let mut client = FlowServiceClient::new(channel.clone());
        if self.open_indexes().len() < MAX_OPEN_VAULTS {
            self.start_vault(&mut client).await?;
        }
        let open = self.open_indexes();
        for offset in 0..REWRITES_PER_ROUND.min(open.len()) {
            let index = open[(self.rewrite_cursor + offset) % open.len()];
            self.rewrite_large(&mut client, index).await?;
        }
        self.rewrite_cursor += REWRITES_PER_ROUND;
        for index in self.open_indexes() {
            self.publish_message(&mut client, index).await?;
        }
        Ok(())
    }

    /// Stops the oldest half of the open vault Flows and erases their hard-delete values;
    /// returns how many it closed.
    ///
    /// # Errors
    ///
    /// Returns a description when a call is not acknowledged within the budget.
    pub(crate) async fn close_and_erase_oldest(
        &mut self,
        channel: &Channel,
    ) -> Result<usize, String> {
        let open = self.open_indexes();
        let count = open.len().div_ceil(2);
        self.close_and_erase(channel, &open[..count]).await?;
        Ok(count)
    }

    /// Stops every open vault Flow and erases every stored hard-delete value.
    ///
    /// # Errors
    ///
    /// Returns a description when a call is not acknowledged within the budget.
    pub(crate) async fn close_and_erase_all(&mut self, channel: &Channel) -> Result<(), String> {
        let open = self.open_indexes();
        self.close_and_erase(channel, &open).await
    }

    /// Reads every vault Flow back and returns each difference from the acknowledged writes.
    ///
    /// # Errors
    ///
    /// Returns a description when a read keeps failing for the budget.
    pub(crate) async fn check_all(&mut self, channel: &Channel) -> Result<Vec<String>, String> {
        let mut client = FlowServiceClient::new(channel.clone());
        let mut violations = Vec::new();
        for record in &self.records {
            violations.extend(check_record(&mut client, record).await?);
        }
        self.report.checks += 1;
        Ok(violations)
    }

    fn open_indexes(&self) -> Vec<usize> {
        self.records
            .iter()
            .enumerate()
            .filter(|(_, record)| !record.is_closed)
            .map(|(index, _)| index)
            .collect()
    }

    fn new_request_id(&mut self, purpose: &str) -> String {
        self.next_request_number += 1;
        format!(
            "{}-{purpose}-{}",
            self.flow_id_prefix, self.next_request_number
        )
    }

    async fn start_vault(&mut self, client: &mut FlowServiceClient<Channel>) -> Result<(), String> {
        let flow_id = format!("{}-{}", self.flow_id_prefix, self.next_flow_number);
        self.next_flow_number += 1;
        let request = StartFlowRequest {
            flow_id: flow_id.clone(),
            flow_type: VAULT_FLOW_TYPE.to_owned(),
            start_step_type: VAULT_STEP_TYPE.to_owned(),
            step_input: Some(string_value("vault")),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget {
                        is_pull_mode: true,
                        ..WorkerTarget::default()
                    }),
                    ..FlowConfig::default()
                }),
                flow_already_started_options: Some(FlowAlreadyStartedOptions {
                    ignore_already_started_error: true,
                }),
                ..FlowStartOptions::default()
            }),
            request_id: self.new_request_id("start"),
            ..StartFlowRequest::default()
        };
        let run_id = call_until_acknowledged(&format!("StartFlow {flow_id}"), || {
            let mut client = client.clone();
            let request = request.clone();
            async move {
                client
                    .start_flow(request)
                    .await
                    .map(|response| response.into_inner().run_id)
            }
        })
        .await?;
        let request = SetAttributesRequest {
            flow_id: flow_id.clone(),
            run_id: run_id.clone(),
            attributes: vec![
                AttributeWrite {
                    key: LARGE_ATTRIBUTE.to_owned(),
                    value: Some(string_value(&large_value(&flow_id, 1))),
                    ..AttributeWrite::default()
                },
                AttributeWrite {
                    key: SECRET_ATTRIBUTE.to_owned(),
                    value: Some(string_value(&secret_value(&flow_id))),
                    is_hard_delete_on_null: true,
                    ..AttributeWrite::default()
                },
            ],
            request_id: self.new_request_id("first-write"),
            ..SetAttributesRequest::default()
        };
        set_attributes_until_acknowledged(client, request).await?;
        self.records.push(VaultRecord {
            flow_id,
            run_id,
            large_version: 1,
            secret: SecretState::Stored,
            is_closed: false,
            acknowledged_messages: BTreeSet::new(),
            sent_messages: BTreeSet::new(),
            resent_messages: BTreeSet::new(),
        });
        self.report.flows_started += 1;
        self.report.large_writes += 1;
        Ok(())
    }

    async fn rewrite_large(
        &mut self,
        client: &mut FlowServiceClient<Channel>,
        index: usize,
    ) -> Result<(), String> {
        let version = self.records[index].large_version + 1;
        let request = SetAttributesRequest {
            flow_id: self.records[index].flow_id.clone(),
            run_id: self.records[index].run_id.clone(),
            attributes: vec![AttributeWrite {
                key: LARGE_ATTRIBUTE.to_owned(),
                value: Some(string_value(&large_value(
                    &self.records[index].flow_id,
                    version,
                ))),
                ..AttributeWrite::default()
            }],
            request_id: self.new_request_id("rewrite"),
            ..SetAttributesRequest::default()
        };
        set_attributes_until_acknowledged(client, request).await?;
        self.records[index].large_version = version;
        self.report.large_writes += 1;
        Ok(())
    }

    async fn publish_message(
        &mut self,
        client: &mut FlowServiceClient<Channel>,
        index: usize,
    ) -> Result<(), String> {
        self.next_message_number += 1;
        let text = format!("message-{}", self.next_message_number);
        let record = &mut self.records[index];
        let request = PublishToChannelRequest {
            flow_id: record.flow_id.clone(),
            run_id: record.run_id.clone(),
            messages: vec![ChannelMessage {
                channel_name: MESSAGE_CHANNEL.to_owned(),
                value: Some(string_value(&text)),
                ..ChannelMessage::default()
            }],
        };
        record.sent_messages.insert(text.clone());
        let mut attempts = 0;
        let outcome = call_until_acknowledged(&format!("PublishToChannel {text}"), || {
            attempts += 1;
            let mut client = client.clone();
            let request = request.clone();
            async move { client.publish_to_channel(request).await.map(|_| ()) }
        })
        .await;
        // PublishToChannel takes no request ID, so a resent message may be stored twice.
        if attempts > 1 {
            record.resent_messages.insert(text.clone());
            self.report.messages_resent += 1;
        }
        outcome?;
        record.acknowledged_messages.insert(text);
        self.report.messages_acknowledged += 1;
        Ok(())
    }

    async fn close_and_erase(
        &mut self,
        channel: &Channel,
        indexes: &[usize],
    ) -> Result<(), String> {
        let mut client = FlowServiceClient::new(channel.clone());
        for &index in indexes {
            let (flow_id, run_id) = (
                self.records[index].flow_id.clone(),
                self.records[index].run_id.clone(),
            );
            stop_until_terminated(&mut client, &flow_id, &run_id).await?;
            self.records[index].is_closed = true;
            self.report.flows_closed += 1;
            let request = SetAttributesRequest {
                flow_id,
                run_id,
                attributes: vec![AttributeWrite {
                    key: SECRET_ATTRIBUTE.to_owned(),
                    value: Some(Value {
                        kind: Some(value::Kind::NullValue(0)),
                    }),
                    is_hard_delete_on_null: true,
                    ..AttributeWrite::default()
                }],
                request_id: self.new_request_id("erase"),
                ..SetAttributesRequest::default()
            };
            set_attributes_until_acknowledged(&mut client, request).await?;
            self.records[index].secret = SecretState::Erased;
            self.report.secrets_erased += 1;
        }
        Ok(())
    }
}

/// Reads one vault Flow and returns each difference from its acknowledged writes.
async fn check_record(
    client: &mut FlowServiceClient<Channel>,
    record: &VaultRecord,
) -> Result<Vec<String>, String> {
    let mut violations = Vec::new();
    let attributes = call_until_acknowledged(&format!("GetAttributes {}", record.flow_id), || {
        let mut client = client.clone();
        let request = GetAttributesRequest {
            flow_id: record.flow_id.clone(),
            run_id: record.run_id.clone(),
            all_keys: true,
            ..GetAttributesRequest::default()
        };
        async move {
            client
                .get_attributes(request)
                .await
                .map(|response| response.into_inner().attributes)
        }
    })
    .await?;
    let large = attribute_text(client, record, &attributes, LARGE_ATTRIBUTE).await?;
    let expected_large = large_value(&record.flow_id, record.large_version);
    if large.as_deref() != Some(expected_large.as_str()) {
        violations.push(format!(
            "{} does not hold large Attribute version {}: {:?}",
            record.flow_id,
            record.large_version,
            large.map(|text| text.chars().take(60).collect::<String>())
        ));
    }
    let secret = attribute_text(client, record, &attributes, SECRET_ATTRIBUTE).await?;
    match record.secret {
        SecretState::Stored
            if secret.as_deref() != Some(secret_value(&record.flow_id).as_str()) =>
        {
            violations.push(format!(
                "{} lost its hard-delete value: {:?}",
                record.flow_id,
                secret.map(|text| text.chars().take(60).collect::<String>())
            ));
        }
        SecretState::Erased if secret.is_some() => {
            violations.push(format!(
                "{} still returns its erased hard-delete value",
                record.flow_id
            ));
        }
        SecretState::Stored | SecretState::Erased => {}
    }
    let messages =
        call_until_acknowledged(&format!("GetChannelMessages {}", record.flow_id), || {
            let mut client = client.clone();
            let request = GetChannelMessagesRequest {
                flow_id: record.flow_id.clone(),
                run_id: record.run_id.clone(),
                channel_name: MESSAGE_CHANNEL.to_owned(),
            };
            async move {
                client
                    .get_channel_messages(request)
                    .await
                    .map(|response| response.into_inner().messages)
            }
        })
        .await?;
    let mut stored: BTreeMap<String, usize> = BTreeMap::new();
    for message in messages {
        if let Some(value::Kind::StringValue(text)) = message.value.and_then(|value| value.kind) {
            *stored.entry(text).or_default() += 1;
        }
    }
    for text in &record.acknowledged_messages {
        if !stored.contains_key(text) {
            violations.push(format!(
                "{} lost the acknowledged Channel message {text}",
                record.flow_id
            ));
        }
    }
    for (text, count) in &stored {
        if !record.sent_messages.contains(text) {
            violations.push(format!(
                "{} holds the Channel message {text}, which was never sent",
                record.flow_id
            ));
        } else if *count > 1 && !record.resent_messages.contains(text) {
            violations.push(format!(
                "{} holds the Channel message {text} {count} times, but it was sent once",
                record.flow_id
            ));
        }
    }
    let status = call_until_acknowledged(&format!("GetFlowSummary {}", record.flow_id), || {
        let mut client = client.clone();
        let request = GetFlowSummaryRequest {
            flow_id: record.flow_id.clone(),
            run_id: record.run_id.clone(),
        };
        async move {
            client
                .get_flow_summary(request)
                .await
                .map(|response| response.into_inner().flow_status)
        }
    })
    .await?;
    let expected_status = if record.is_closed {
        FlowStatus::Terminated
    } else {
        FlowStatus::Running
    };
    if status != expected_status as i32 {
        violations.push(format!(
            "{} has status {status}, expected {expected_status:?}",
            record.flow_id
        ));
    }
    Ok(violations)
}

/// Returns the text of Attribute `key`, loading a blob reference, or `None` when missing or not
/// text.
async fn attribute_text(
    client: &mut FlowServiceClient<Channel>,
    record: &VaultRecord,
    attributes: &[Kv],
    key: &str,
) -> Result<Option<String>, String> {
    let Some(stored) = attributes
        .iter()
        .find(|attribute| attribute.key == key)
        .and_then(|attribute| attribute.value.clone())
    else {
        return Ok(None);
    };
    let blob_id = match &stored.kind {
        Some(value::Kind::StringValue(text)) => return Ok(Some(text.clone())),
        Some(value::Kind::InternalBlobIdForStringValue(blob_id)) => blob_id.clone(),
        _ => return Ok(None),
    };
    let mut loaded = call_until_acknowledged(&format!("LoadBlobs {}", record.flow_id), || {
        let mut client = client.clone();
        let request = LoadBlobsRequest {
            entries: vec![LoadBlobRequestEntry {
                flow_id: record.flow_id.clone(),
                blob_value: Some(stored.clone()),
            }],
        };
        async move {
            client
                .load_blobs(request)
                .await
                .map(|response| response.into_inner().values)
        }
    })
    .await?;
    Ok(match loaded.remove(&blob_id).and_then(|value| value.kind) {
        Some(value::Kind::StringValue(text)) => Some(text),
        other => {
            return Err(format!(
                "LoadBlobs {} returned {other:?} for blob {blob_id}",
                record.flow_id
            ));
        }
    })
}

/// Stops `flow_id`'s run; a stop whose reply was lost shows as an already terminated run.
async fn stop_until_terminated(
    client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    run_id: &str,
) -> Result<(), String> {
    let deadline = Instant::now() + ACKNOWLEDGEMENT_BUDGET;
    loop {
        let stop = client.stop_flow(StopFlowRequest {
            flow_id: flow_id.to_owned(),
            run_id: run_id.to_owned(),
            reason: "soak hard delete".to_owned(),
            stop_type: StopType::Terminate as i32,
        });
        let failure = match tokio::time::timeout(CALL_TIMEOUT, stop).await {
            Ok(Ok(_)) => return Ok(()),
            Ok(Err(status)) => status.to_string(),
            Err(_) => format!("no reply within {CALL_TIMEOUT:?}"),
        };
        let summary = client.get_flow_summary(GetFlowSummaryRequest {
            flow_id: flow_id.to_owned(),
            run_id: run_id.to_owned(),
        });
        if let Ok(Ok(summary)) = tokio::time::timeout(CALL_TIMEOUT, summary).await
            && summary.get_ref().flow_status == FlowStatus::Terminated as i32
        {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "StopFlow {flow_id} failed until {ACKNOWLEDGEMENT_BUDGET:?} passed: {failure}"
            ));
        }
        tokio::time::sleep(RETRY_PAUSE).await;
    }
}

/// Sends `request` until it is acknowledged; its request ID makes a resend harmless.
async fn set_attributes_until_acknowledged(
    client: &mut FlowServiceClient<Channel>,
    request: SetAttributesRequest,
) -> Result<(), String> {
    call_until_acknowledged(&format!("SetAttributes {}", request.request_id), || {
        let mut client = client.clone();
        let request = request.clone();
        async move { client.set_attributes(request).await.map(|_| ()) }
    })
    .await
}

/// Repeats `call` until it succeeds or the acknowledgement budget passes.
async fn call_until_acknowledged<T, F, Fut>(description: &str, mut call: F) -> Result<T, String>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = Result<T, tonic::Status>>,
{
    let deadline = Instant::now() + ACKNOWLEDGEMENT_BUDGET;
    loop {
        let failure = match tokio::time::timeout(CALL_TIMEOUT, call()).await {
            Ok(Ok(value)) => return Ok(value),
            Ok(Err(status)) => status.to_string(),
            Err(_) => format!("no reply within {CALL_TIMEOUT:?}"),
        };
        if Instant::now() >= deadline {
            return Err(format!(
                "{description} failed until {ACKNOWLEDGEMENT_BUDGET:?} passed: {failure}"
            ));
        }
        tokio::time::sleep(RETRY_PAUSE).await;
    }
}

/// Returns the large Attribute text of `version` of `flow_id`.
fn large_value(flow_id: &str, version: u64) -> String {
    repeated_text(&format!("{flow_id} version {version} "), LARGE_VALUE_BYTES)
}

fn secret_value(flow_id: &str) -> String {
    repeated_text(&format!("{flow_id} secret "), SECRET_VALUE_BYTES)
}

fn repeated_text(unit: &str, length: usize) -> String {
    unit.chars().cycle().take(length).collect()
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}
