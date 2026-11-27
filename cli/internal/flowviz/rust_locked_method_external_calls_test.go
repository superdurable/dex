// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const rustLockedMethodSource = `use std::collections::BTreeMap;
use std::fmt::Write;
use std::net::TcpStream;
use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, AttributeMap, Client, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, StepOptions, Wait,
};

pub trait Mailer: Send + Sync {
    fn send(&self, to: &str) -> Result<(), String>;
    fn get(&self) -> String;
}

static ENDING: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new("ending"));
static COUNTER: LazyLock<Attribute<i64>> = LazyLock::new(|| Attribute::new("counter"));
static LAST_ORDER: LazyLock<Attribute<crate::orders::Order>> =
    LazyLock::new(|| Attribute::new("last_order"));
static PENDING_REPORTS: LazyLock<AttributeMap<BTreeMap<String, crate::reports::Report>>> =
    LazyLock::new(|| AttributeMap::new("pending_reports"));
const STOP_REMINDERS: Rpc<String, String> = Rpc::new("StopReminders");

pub struct ReminderFlow {
    send_reminder: SendReminderStep,
    deliver: DeliverStep,
}

impl Flow for ReminderFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.send_reminder).and(&self.deliver)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&ENDING).attribute(&COUNTER)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(STOP_REMINDERS RPC_LOCK, Self::stop_reminders)
    }
}

impl ReminderFlow {
    fn stop_reminders(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<String>> {
        ENDING.set(context, true)?;
        Ok(RpcResult::new(input))
    }
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Send one reminder."
pub struct SendReminderStep {
    mailer: Arc<dyn Mailer>,
    client: Arc<Client>,
    optional_client: Option<Arc<Client>>,
    optional_mailer: Option<Arc<dyn Mailer>>,
}

impl Step for SendReminderStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new() WAIT_LOCK EXECUTE_LOCK
    }

    WAIT_METHOD

    fn execute(&self, context: &mut Context, input: EXECUTE_INPUT) -> HandlerResult<StepDecision> {
        EXECUTE_BODY
        Ok(StepDecision::go_to(&DeliverStep { mailer: self.mailer.clone() }, input))
    }
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Deliver the reminder."
pub struct DeliverStep {
    mailer: Arc<dyn Mailer>,
}

impl Step for DeliverStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new() DELIVER_LOCK
    }

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        DELIVER_BODY
        Ok(StepDecision::graceful_complete(input))
    }
}

EXTRA_SOURCE
`

type rustLockedMethodCase struct {
	rpcLock     string
	waitLock    string
	executeLock string
	deliverLock string
	waitBody    string
	// executeInput is the declared type of SendReminderStep.execute's input; empty means String.
	executeInput string
	executeBody  string
	deliverBody  string
	extraSource  string
}

const rustEndingLock = ".lock(ENDING.lock())"

// Expected states of a Step method's methodExternalCalls marker.
const (
	rustMarkerAbsent = "absent"
	rustMarkerClean  = "clean"
	rustMarkerCalls  = "calls"
)

const rustMailerSend = `self.mailer.send(&input).map_err(|error| HandlerError::new("Reminder", &error))?;`

const rustPureExecuteBody = `let mut names = BTreeMap::new();
        names.insert(input.trim().to_uppercase(), input.len());
        let encoded = serde_json::to_string(&names).map_err(HandlerError::from_error)?;
        tracing::info!(encoded = %encoded, count = names.len(), "reminder");
        let count = COUNTER.get(context)?.unwrap_or_default();
        COUNTER.set(context, count + names.len() as i64)?;
        ENDING.set(context, format_reminder(&input).is_empty())?;
        let delay = Duration::from_secs(5).as_millis();
        let message = format!("{delay} {}", vec![1, 2].len());
        let mut summary = String::new();
        write!(summary, "{message}").map_err(HandlerError::from_error)?;
        let total: usize = names.values().map(|length| length * 2).sum();
        assert!(total >= names.len(), "total {total}");
        let labels: Vec<String> = names.keys().cloned().chain([self.step_type().to_owned()]).collect();
        let note = describe_note(labels.join(","));`

const rustOrderDataBody = `let number = input.number.as_str().to_owned();
        let count = input.items.len();
        for item in &input.items {
            let name = item.name.trim().to_string();
        }
        let saved = LAST_ORDER.get(context)?.unwrap_or_default();
        let saved_number = saved.number.as_str();`

const rustPureHelperSource = `fn format_reminder(input: &str) -> String {
    let mut text = input.trim().to_owned();
    text.push_str(&format!(" reminder for {input}"));
    text
}

fn describe_note(note: impl Into<String>) -> String {
    note.into()
}`

func TestRustLockedMethodExternalCall(t *testing.T) {
	requireRustAnalyzerCargo(t)
	testCases := map[string]struct {
		source rustLockedMethodCase
		// messages lists every locked_method_external_call message fragment, in report order.
		messages []string
		// uncertainMessages lists every locked_method_uncertain_call message fragment, in report order.
		uncertainMessages []string
		// executeMarker is SendReminderStep.execute's marker: rustMarkerAbsent, rustMarkerClean, or rustMarkerCalls.
		executeMarker string
	}{
		"execute calls an injected service while holding an RPC lock": {
			source:        rustLockedMethodCase{rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: rustMailerSend},
			messages:      []string{"Execute of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls self.mailer.send; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step."},
			executeMarker: rustMarkerCalls,
		},
		"call moved to the next unlocked Step": {
			source:        rustLockedMethodCase{rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: "ENDING.set(context, false)?;", deliverBody: rustMailerSend},
			executeMarker: rustMarkerClean,
		},
		"lock shared only between Steps": {
			source:        rustLockedMethodCase{rpcLock: rustEndingLock, executeLock: ".execute_lock(COUNTER.lock())", deliverLock: ".execute_lock(COUNTER.lock())", executeBody: rustMailerSend, deliverBody: rustMailerSend},
			executeMarker: rustMarkerAbsent,
		},
		"locked Execute with only SDK, pure standard library, and pure helper calls": {
			source:        rustLockedMethodCase{rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: rustPureExecuteBody, extraSource: rustPureHelperSource},
			executeMarker: rustMarkerClean,
		},
		"same-file helper returns a Dex Client in HandlerResult": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let client = require_client(self.optional_client.as_ref())?;\n        let info = client.describe_flow(&input);",
				extraSource: "fn require_client(client: Option<&Arc<Client>>) -> HandlerResult<&Client> {\n    client.map(Arc::as_ref).ok_or_else(|| HandlerError::new(\"Reminder\", \"missing client\"))\n}",
			},
			messages:      []string{"and calls client.describe_flow;"},
			executeMarker: rustMarkerCalls,
		},
		"Step input of a same-file data type with derived Clone and standard fields": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeInput: "LocalOrder",
				executeBody: "let copy = input.clone();\n        let number = input.number.as_str();\n        let count = input.items.len();",
				extraSource: "#[derive(Clone)]\nstruct LocalOrder {\n    number: String,\n    items: Vec<String>,\n}",
			},
			executeMarker: rustMarkerClean,
		},
		"Step input of another file's type calls get": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeInput: "crate::payload::Payload",
				executeBody: "let value = input.get();",
			},
			uncertainMessages: []string{"and calls input.get. dexcli cannot determine whether this call performs I/O."},
			executeMarker:     rustMarkerCalls,
		},
		"derived Default whose field Default does I/O": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let made = SameFileDefault::default();",
				extraSource: "#[derive(Default)]\nstruct SameFileDefault {\n    value: IODefault,\n}\n\nstruct IODefault;\n\nimpl Default for IODefault {\n    fn default() -> Self {\n        let contents = std::fs::read(\"/tmp/one\");\n        Self\n    }\n}",
			},
			messages:      []string{"and calls SameFileDefault::default (calls IODefault::default (calls std::fs::read));"},
			executeMarker: rustMarkerCalls,
		},
		"derived Clone whose field type is from another file": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let copy = Wrapper::default().clone();",
				extraSource: "#[derive(Clone, Default)]\nstruct Wrapper {\n    payload: crate::payload::Payload,\n    shared: Arc<String>,\n}",
			},
			uncertainMessages: []string{"(calls crate::payload::Payload::"},
			executeMarker:     rustMarkerCalls,
		},
		"Step input and Attribute values of another file's type with pure field methods": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeInput: "crate::orders::Order",
				executeBody: rustOrderDataBody,
			},
			executeMarker: rustMarkerClean,
		},
		"injected service called with Step input data": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeInput: "crate::orders::Order",
				executeBody: rustOrderDataBody + "\n        let sent = self.mailer.send(input.number.as_str());",
			},
			messages:      []string{"and calls self.mailer.send;"},
			executeMarker: rustMarkerCalls,
		},
		"Step input data calling a method that is not standard-like": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeInput: "crate::orders::Order",
				executeBody: "let receipt = input.submit();",
			},
			uncertainMessages: []string{"and calls input.submit. dexcli cannot determine whether this call performs I/O."},
			executeMarker:     rustMarkerCalls,
		},
		"combinator closures over an AttributeMap value read as data": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let pending = PENDING_REPORTS.get(context, &input)?;\n" +
					"        let report = pending.and_then(|mut reports| reports.remove(&input));\n" +
					"        let count = PENDING_REPORTS.get(context, &input)?.map(|reports| reports.len()).unwrap_or_default();\n" +
					"        let first = PENDING_REPORTS.get(context, &input)?.and_then(|reports| reports.get(&input).map(|report| report.id.as_str().to_owned()));",
			},
			executeMarker: rustMarkerClean,
		},
		"combinator closure parameter bound to an injected service": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let sent = self.optional_mailer.as_ref().map(|mailer| mailer.send(&input));",
			},
			messages:      []string{"and calls mailer.send;"},
			executeMarker: rustMarkerCalls,
		},
		"injected service taken from a destructured tuple": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let (handle,) = (self.mailer.clone(),);\n        let fetched = handle.get();",
			},
			messages:      []string{"and calls handle.get;"},
			executeMarker: rustMarkerCalls,
		},
		"method on a value of unknown type": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let entries: Vec<_> = Vec::new();\n        entries.iter().for_each(|entry| entry.flush());",
			},
			uncertainMessages: []string{"and calls entry.flush. dexcli cannot determine whether this call performs I/O."},
			executeMarker:     rustMarkerCalls,
		},
		"derived Default of a same-file type": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let summary = Summary::default();\n        let copy = summary.clone();",
				extraSource: "#[derive(Clone, Default)]\nstruct Summary {\n    count: u32,\n    client: Option<Arc<Client>>,\n}",
			},
			executeMarker: rustMarkerClean,
		},
		"definite Dex Client call listed before an uncertain cross-file call": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let label = crate::labels::reminder_label(&input);\n        self.client.stop_flow(&input, None).map_err(HandlerError::from_error)?;",
			},
			messages:      []string{"and calls self.client.stop_flow (and 1 uncertain);"},
			executeMarker: rustMarkerCalls,
		},
		"helper calls std::net": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: "notify_customer(&input);",
				extraSource: "fn notify_customer(input: &str) {\n    let stream = TcpStream::connect(input);\n    drop(stream);\n}",
			},
			messages:      []string{"and calls notify_customer (calls TcpStream::connect); Dex fails that RPC"},
			executeMarker: rustMarkerCalls,
		},
		"helper calls reqwest": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: "notify_customer(&input);",
				extraSource: "fn notify_customer(input: &str) {\n    let response = reqwest::blocking::get(input);\n    drop(response);\n}",
			},
			messages:      []string{"and calls notify_customer (calls reqwest::blocking::get); Dex fails that RPC"},
			executeMarker: rustMarkerCalls,
		},
		"Dex Client call under the RPC lock": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let client = self.client.clone();\n        client.stop_flow(&input, None).map_err(HandlerError::from_error)?;",
			},
			messages:      []string{"and calls client.stop_flow;"},
			executeMarker: rustMarkerCalls,
		},
		"optional Dex Client unwrapped under the RPC lock": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let client = self.optional_client.as_ref().expect(\"client\");\n        let info = client.describe_flow(&input);",
			},
			messages:      []string{"and calls client.describe_flow;"},
			executeMarker: rustMarkerCalls,
		},
		"collection methods of another module's data type": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let mut records: Vec<crate::records::Record> = Vec::new();\n        let empty = records.is_empty() && records.len() == 0;\n        records.clear();",
			},
			executeMarker: rustMarkerClean,
		},
		"element method of another module's data type": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let records: Vec<crate::records::Record> = Vec::new();\n        let sent = records.first().unwrap().send();",
			},
			uncertainMessages: []string{"and calls records.first().unwrap().send. dexcli cannot determine whether this call performs I/O."},
			executeMarker:     rustMarkerCalls,
		},
		"injected service call inside format": {
			source:        rustLockedMethodCase{rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: `let summary = format!("{:?}", self.mailer.send(&input));`},
			messages:      []string{"and calls self.mailer.send;"},
			executeMarker: rustMarkerCalls,
		},
		"wait for calls a closure value while holding an RPC lock": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, waitLock: ".wait_for_lock(ENDING.lock())",
				waitBody: "let notify = |to: &str| to.len();\n        notify(&input);",
			},
			messages:      []string{"WaitFor of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls notify;"},
			executeMarker: rustMarkerAbsent,
		},
		"recursive pure helpers": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: "let remaining = count_down(3);",
				extraSource: "fn count_down(n: u32) -> u32 {\n    if n == 0 { 0 } else { count_up(n - 1) }\n}\n\nfn count_up(n: u32) -> u32 {\n    count_down(n)\n}",
			},
			executeMarker: rustMarkerClean,
		},
		"standard library sleep and println": {
			source:        rustLockedMethodCase{rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())", executeBody: "std::thread::sleep(Duration::from_secs(1));\n        println!(\"slept\");"},
			messages:      []string{"and calls std::thread::sleep (and 1 uncertain);"},
			executeMarker: rustMarkerCalls,
		},
		"no RPC lock": {
			source:        rustLockedMethodCase{executeLock: ".execute_lock(ENDING.lock())", executeBody: rustMailerSend},
			executeMarker: rustMarkerAbsent,
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzeRustLockedMethodSource(t, testCase.source)
			requireLockedMethodDiagnostics(t, graph, testCase.messages, testCase.uncertainMessages)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
			markerIndex := slices.IndexFunc(graph.methodExternalCalls, func(marker methodExternalCalls) bool {
				return marker.NodeID == "step:SendReminderStep" && marker.Phase == "execute"
			})
			if testCase.executeMarker == rustMarkerAbsent {
				require.Equal(t, -1, markerIndex, "%+v", graph.methodExternalCalls)
				return
			}
			require.GreaterOrEqual(t, markerIndex, 0, "%+v", graph.methodExternalCalls)
			calls := graph.methodExternalCalls[markerIndex].Calls
			require.NotNil(t, calls)
			require.Equal(t, testCase.executeMarker == rustMarkerClean, len(calls) == 0, "%+v", calls)
		})
	}
}

const rustDexSDKImportFormSource = `use std::sync::LazyLock;

DEX_IMPORTS

static ENDING: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new("ending"));
const STOP_REMINDERS: Rpc<String, String> = Rpc::new("StopReminders");

pub struct ReminderFlow {
    send_reminder: SendReminderStep,
}

impl Flow for ReminderFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.send_reminder)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&ENDING)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(STOP_REMINDERS.lock(ENDING.lock()), Self::stop_reminders)
    }
}

impl ReminderFlow {
    fn stop_reminders(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<String>> {
        ENDING.set(context, true)?;
        Ok(RpcResult::new(input))
    }
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Send one reminder."
pub struct SendReminderStep;

impl Step for SendReminderStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(ENDING.lock())
    }

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        if input.is_empty() {
            return Ok(StepDecision::dead_end());
        }
        ENDING.set(context, false)?;
        Ok(StepDecision::graceful_complete(input))
    }
}
`

func TestRustLockedMethodDexSDKImportForms(t *testing.T) {
	const dexNames = "Attribute, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step, StepDecision, StepList, StepOptions"
	testCases := map[string]struct {
		imports  string
		manifest string
	}{
		"dex_sdk glob beside a crate glob":  {imports: "use crate::models::*;\nuse dex_sdk::*;"},
		"dex_sdk glob beside a super glob":  {imports: "use dex_sdk::*;\nuse super::*;"},
		"renamed dependency with named use": {imports: "use dex::{" + dexNames + "};", manifest: "[dependencies]\ndex = { package = \"dex-sdk\", version = \"1\" }\n"},
		"renamed dependency table with glob": {
			imports: "use dex::*;\nuse crate::models::*;", manifest: "[dependencies.dex]\npackage = \"dex-sdk\"\nversion = \"1\"\n",
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			manifest := "[package]\nname = \"reminders\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n" + cmp.Or(testCase.manifest, "[dependencies]\ndex-sdk = \"1\"\n")
			require.NoError(t, os.WriteFile(filepath.Join(directory, "Cargo.toml"), []byte(manifest), 0o644))
			sourcePath := filepath.Join(directory, "src", "flow.rs")
			require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
			source := strings.Replace(rustDexSDKImportFormSource, "DEX_IMPORTS", testCase.imports, 1)
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
			require.NoError(t, err)
			require.Empty(t, lockedMethodWarningMessages(graph), "%+v", graph.Diagnostics)
			markerIndex := slices.IndexFunc(graph.methodExternalCalls, func(marker methodExternalCalls) bool {
				return marker.NodeID == "step:SendReminderStep" && marker.Phase == "execute"
			})
			require.GreaterOrEqual(t, markerIndex, 0, "%+v", graph.methodExternalCalls)
			require.Empty(t, graph.methodExternalCalls[markerIndex].Calls)
		})
	}
}

func TestRustLockedMethodDexSDKAliasBoundaries(t *testing.T) {
	const memberPackage = "[package]\nname = \"reminders\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"
	const workspaceRoot = "[workspace]\nmembers = [\"reminders\"]\n\n[workspace.dependencies]\ndex = { package = \"dex-sdk\", version = \"1\" }\n"
	testCases := map[string]struct {
		imports, call, workspace, member string
		// callee is the possible external call the marker must keep; empty requires a clean marker.
		callee string
	}{
		"file binding shadows the renamed dependency": {
			imports: "use dex_sdk::*;\nuse crate::services as dex;", call: "dex::send(&input);",
			member: "[dependencies]\ndex = { package = 'dex-sdk', version = \"1\" }\ndex-sdk = \"1\"\n", callee: "dex::send",
		},
		"block use shadows the renamed dependency": {
			imports: "use dex_sdk::*;\nuse crate::models::*;", call: "{\n            use crate::services as dex;\n            dex::send(&input);\n        }",
			member: "[dependencies]\ndex = { package = \"dex-sdk\", version = \"1\" }\n", callee: "dex::send",
		},
		"target-only rename is not applied": {
			imports: "use dex_sdk::*;\nuse crate::models::*;", call: "dex::send(&input);",
			member: "[dependencies]\ndex-sdk = \"1\"\n\n[target.'cfg(unix)'.dependencies]\ndex = { package = \"dex-sdk\", version = \"1\" }\n", callee: "dex::send",
		},
		"workspace rename that the member does not inherit": {
			imports: "use dex_sdk::*;\nuse crate::models::*;", call: "dex::send(&input);", workspace: workspaceRoot,
			member: "[dependencies]\ndex-sdk = \"1\"\ndex = { path = \"../other\" }\n", callee: "dex::send",
		},
		"workspace rename that the member inherits": {
			imports: "use dex::*;\nuse crate::models::*;", workspace: workspaceRoot,
			member: "[dependencies]\ndex = { workspace = true }\n",
		},
		"feature-gated export beside another glob": {
			imports: "use dex_sdk::*;\nuse crate::services::*;", call: "ApiKeyCredential::send(&input);",
			member: "[dependencies]\ndex-sdk = { version = \"1\", default-features = false }\n", callee: "ApiKeyCredential::send",
		},
		"API key credential builder exchanges a token": {
			imports: "use dex_sdk::{ApiKeyCredentialBuilder, Attribute, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step, StepDecision, StepList, StepOptions};",
			call:    "let _ = ApiKeyCredentialBuilder::new().build();", member: "[dependencies]\ndex-sdk = \"1\"\n", callee: "ApiKeyCredentialBuilder::new",
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if testCase.workspace != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(testCase.workspace), 0o644))
			}
			directory := filepath.Join(root, "reminders")
			sourcePath := filepath.Join(directory, "src", "flow.rs")
			require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(directory, "Cargo.toml"), []byte(memberPackage+testCase.member), 0o644))
			source := strings.Replace(rustDexSDKImportFormSource, "DEX_IMPORTS", testCase.imports, 1)
			source = strings.Replace(source, "        ENDING.set(context, false)?;", "        "+testCase.call+"\n        ENDING.set(context, false)?;", 1)
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
			require.NoError(t, err)
			markerIndex := slices.IndexFunc(graph.methodExternalCalls, func(marker methodExternalCalls) bool {
				return marker.NodeID == "step:SendReminderStep" && marker.Phase == "execute"
			})
			require.GreaterOrEqual(t, markerIndex, 0, "%+v", graph.methodExternalCalls)
			calls := graph.methodExternalCalls[markerIndex].Calls
			if testCase.callee == "" {
				require.Empty(t, calls)
				return
			}
			require.True(t, slices.ContainsFunc(calls, func(call possibleExternalCall) bool { return call.Callee == testCase.callee }), "%+v", calls)
		})
	}
}

func TestRustLockedMethodCertainty(t *testing.T) {
	const imports = "use dex_sdk::*;\nuse crate::gateway::SmsGateway;\nuse crate::time::parse_local_time;"
	testCases := map[string]struct {
		body      string
		warnings  []string
		uncertain []string
	}{
		"concrete field type from another module is an injected service": {
			body:     "self.gateway.send(&input);\n        let _ = parse_local_time(&input);",
			warnings: []string{"and calls self.gateway.send (and 1 uncertain); Dex fails that RPC"},
		},
		"helper from another module is uncertain": {
			body:      "let _ = parse_local_time(&input);",
			uncertain: []string{"and calls parse_local_time. dexcli cannot determine whether this call performs I/O."},
		},
		"UFCS call of a standard trait method is a static call": {
			body: "let _ = <String as Clone>::clone(&input);\n        let _ = <String as From<&str>>::from(input.as_str());",
		},
		"function pointer field is a dynamic call": {
			body:     "(self.callback)(&input);",
			warnings: []string{"and calls (self.callback); Dex fails that RPC"},
		},
		"printing is uncertain": {
			body:      "println!(\"{input}\");",
			uncertain: []string{"and calls println!. dexcli cannot determine whether this call performs I/O."},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			source := strings.NewReplacer(
				"DEX_IMPORTS", imports,
				"pub struct SendReminderStep;", "pub struct SendReminderStep {\n    gateway: SmsGateway,\n    callback: fn(&str),\n}",
				"        if input.is_empty() {\n            return Ok(StepDecision::dead_end());\n        }\n", "        "+testCase.body+"\n",
			).Replace(rustDexSDKImportFormSource)
			sourcePath := filepath.Join(t.TempDir(), "flow.rs")
			require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
			graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
			require.NoError(t, err)
			requireLockedMethodDiagnostics(t, graph, testCase.warnings, testCase.uncertain)
		})
	}
}

func analyzeRustLockedMethodSource(t *testing.T, source rustLockedMethodCase) *Graph {
	t.Helper()
	waitMethod := ""
	if source.waitBody != "" {
		waitMethod = "fn wait_for(&self, context: &mut Context, input: String) -> HandlerResult<Wait> {\n        " +
			source.waitBody + "\n        Ok(Wait::skip_immediately())\n    }"
	}
	replacer := strings.NewReplacer(
		"RPC_LOCK", source.rpcLock,
		"WAIT_LOCK", source.waitLock,
		"EXECUTE_LOCK", source.executeLock,
		"DELIVER_LOCK", source.deliverLock,
		"WAIT_METHOD", waitMethod,
		"EXECUTE_BODY", source.executeBody,
		"DELIVER_BODY", source.deliverBody,
		"EXTRA_SOURCE", source.extraSource,
		"EXECUTE_INPUT", cmp.Or(source.executeInput, "String"),
	)
	sourcePath := filepath.Join(t.TempDir(), "workflow.rs")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(rustLockedMethodSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	return graph
}
