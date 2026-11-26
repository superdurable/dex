// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package flowviz

import (
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
    Attribute, Client, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, StepOptions, Wait,
};

pub trait Mailer: Send + Sync {
    fn send(&self, to: &str) -> Result<(), String>;
    fn get(&self) -> String;
}

static ENDING: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new("ending"));
static COUNTER: LazyLock<Attribute<i64>> = LazyLock::new(|| Attribute::new("counter"));
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
}

impl Step for SendReminderStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new() WAIT_LOCK EXECUTE_LOCK
    }

    WAIT_METHOD

    fn execute(&self, context: &mut Context, input: String) -> HandlerResult<StepDecision> {
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
	executeBody string
	deliverBody string
	extraSource string
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
				executeBody: "let lengths: Vec<usize> = vec![input.clone()].iter().map(|value| value.len()).collect();",
			},
			messages:      []string{"and calls value.len;"},
			executeMarker: rustMarkerCalls,
		},
		"derived Default of a same-file type": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let summary = Summary::default();\n        let copy = summary.clone();",
				extraSource: "#[derive(Clone, Default)]\nstruct Summary {\n    count: u32,\n}",
			},
			executeMarker: rustMarkerClean,
		},
		"definite Dex Client call listed before an uncertain cross-file call": {
			source: rustLockedMethodCase{
				rpcLock: rustEndingLock, executeLock: ".execute_lock(ENDING.lock())",
				executeBody: "let label = crate::labels::reminder_label(&input);\n        self.client.stop_flow(&input, None).map_err(HandlerError::from_error)?;",
			},
			messages:      []string{"and calls self.client.stop_flow (and 1 more);"},
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
			messages:      []string{"and calls records.first().unwrap().send;"},
			executeMarker: rustMarkerCalls,
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
			messages:      []string{"and calls std::thread::sleep (and 1 more);"},
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
			var messages []string
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == "locked_method_external_call" {
					require.Equal(t, "warning", diagnostic.Severity)
					require.NotNil(t, diagnostic.Span)
					messages = append(messages, diagnostic.Message)
				}
			}
			require.Len(t, messages, len(testCase.messages), "%+v", graph.Diagnostics)
			for index, fragment := range testCase.messages {
				require.Contains(t, messages[index], fragment)
			}
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
	)
	sourcePath := filepath.Join(t.TempDir(), "workflow.rs")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(rustLockedMethodSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	return graph
}
