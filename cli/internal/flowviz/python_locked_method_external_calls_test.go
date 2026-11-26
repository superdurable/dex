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

const pythonLockedMethodSource = `import json
import logging
import time
import urllib.parse
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone

from dex import Attribute, Client, Context, Flow, PersistenceSchema, RPCResult, Step, StepDecision, StepList, StepOptions, Wait, go_to, graceful_complete, rpc

logger = logging.getLogger(__name__)

MODULE_PRELUDE


class ReminderFlow(Flow[str]):
    ending = Attribute("ending", bool)
    counter = Attribute("counter", int)

    def __init__(self, mailer):
        self.send_reminder = SendReminderStep(mailer)
        self.deliver = DeliverStep(mailer)

    def get_steps(self):
        return StepList.start_step(self.send_reminder).other_steps(self.deliver)

    def get_persistence_schema(self):
        return PersistenceSchema.of(self.ending, self.counter)

    @rpc(name="StopReminders"RPC_OPTIONS)
    def stop_reminders(self, context: Context, input: str) -> RPCResult[str]:
        ReminderFlow.ending.set(context, True)
        return RPCResult(input)


# dex:group group-id:reminders group-label:"Reminders"
# dex:explanation text:"Send one reminder."
class SendReminderStep(Step[str]):
    def __init__(self, mailer):
        self.mailer = mailer

    def get_step_options(self):
        return StepOptions(SEND_OPTIONS)

    def wait_for(self, context: Context, input: str) -> Wait:
        WAIT_BODY
        return Wait.skip_immediately()

    EXECUTE_DECORATORdef execute(self, context: Context, input: str) -> StepDecision:
        EXECUTE_BODY
        return go_to(DeliverStep, input)

STEP_METHODS


# dex:group group-id:reminders group-label:"Reminders"
# dex:explanation text:"Deliver the reminder."
class DeliverStep(Step[str]):
    def __init__(self, mailer):
        self.mailer = mailer

    def get_step_options(self):
        return StepOptions(DELIVER_OPTIONS)

    def execute(self, context: Context, input: str) -> StepDecision:
        DELIVER_BODY
        return graceful_complete(input)


MODULE_EXTRA
`

type pythonLockedMethodCase struct {
	rpcLock     string
	waitLock    string
	executeLock string
	deliverLock string
	waitBody    string
	executeBody string
	deliverBody string
	// stepMethods adds methods to SendReminderStep, indented as class members.
	stepMethods string
	// executeDecorator precedes the SendReminderStep execute definition, ending with its indentation.
	executeDecorator string
	modulePrelude    string
	moduleExtra      string
}

const pythonPureExecuteBody = `payload = json.dumps({"to": input.strip().upper(), "at": datetime.now(timezone.utc).isoformat()})
        logger.info("reminder %s %s", payload, urllib.parse.quote(input))
        names = sorted([input, payload], key=len)
        names.append(format_reminder(input))
        count = ReminderFlow.counter.get(context) or 0
        ReminderFlow.counter.set(context, count + len(names))
        context.record_event("reminder", Reminder(input).describe())
        self.mark_sent(context, time.time())`

const pythonPureHelperSource = `@dataclass
class Reminder:
    recipient: str

    def describe(self):
        return f"reminder for {self.recipient}"


def format_reminder(text):
    return text.strip().title()`

const pythonPureStepMethods = `    def mark_sent(self, context, sent_at):
        ReminderFlow.ending.set(context, sent_at > 0)`

func TestPythonLockedMethodExternalCall(t *testing.T) {
	const endingLock = "ReminderFlow.ending.lock()"
	const counterLock = "ReminderFlow.counter.lock()"
	testCases := map[string]struct {
		source pythonLockedMethodCase
		// messages lists every locked_method_external_call message fragment, in report order.
		messages []string
		// isExecuteClean requires an analyzed SendReminderStep execute with no possible external call.
		isExecuteClean bool
		// hasOtherError marks a source that another analyzer rule already makes invalid.
		hasOtherError bool
	}{
		"execute calls an injected service while holding an RPC lock": {
			source:   pythonLockedMethodCase{rpcLock: endingLock, executeLock: endingLock, executeBody: "self.mailer.send(input)"},
			messages: []string{"Execute of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls self.mailer.send; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step."},
		},
		"call moved to the next unlocked Step": {
			source:         pythonLockedMethodCase{rpcLock: endingLock, executeLock: endingLock, executeBody: "ReminderFlow.ending.set(context, False)", deliverBody: "self.mailer.send(input)"},
			isExecuteClean: true,
		},
		"lock shared only between Steps": {
			source: pythonLockedMethodCase{rpcLock: endingLock, executeLock: counterLock, deliverLock: counterLock, executeBody: "self.mailer.send(input)", deliverBody: "self.mailer.send(input)"},
		},
		"locked execute with only SDK, pure standard library, and pure helper calls": {
			source:         pythonLockedMethodCase{rpcLock: endingLock, executeLock: endingLock, executeBody: pythonPureExecuteBody, stepMethods: pythonPureStepMethods, moduleExtra: pythonPureHelperSource},
			isExecuteClean: true,
		},
		"helper calls urllib.request": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "notify_customer(input)",
				moduleExtra: "def notify_customer(text):\n    return urllib.request.urlopen(\"https://example.com/\" + text)",
			},
			messages: []string{"and calls notify_customer (calls urllib.request.urlopen); Dex fails that RPC"},
		},
		"helper calls a third-party package": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "notify_customer(input)",
				modulePrelude: "import requests",
				moduleExtra:   "def notify_customer(text):\n    return requests.post(\"https://example.com/\", json={\"to\": text})",
			},
			messages: []string{"and calls notify_customer (calls requests.post); Dex fails that RPC"},
		},
		"execute calls a Dex Client": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "DEX_CLIENT.invoke_rpc(context.flow_id, \"StopReminders\", input)",
				modulePrelude: "DEX_CLIENT = Client(None, None)",
			},
			messages: []string{"and calls DEX_CLIENT.invoke_rpc; Dex fails that RPC"},
		},
		"helper receives an injected service": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "deliver_with(self.mailer, input)",
				moduleExtra: "def deliver_with(mailer, text):\n    return mailer.get(text)",
			},
			messages: []string{"and calls deliver_with (calls mailer.get); Dex fails that RPC"},
		},
		"wait for calls a callable value while holding an RPC lock": {
			source:   pythonLockedMethodCase{rpcLock: endingLock, waitLock: endingLock, waitBody: "notify = self.mailer.send\n        notify(input)"},
			messages: []string{"WaitFor of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls notify;"},
		},
		"callback passed to map": {
			source:   pythonLockedMethodCase{rpcLock: endingLock, executeLock: endingLock, executeBody: "list(map(self.mailer.send, [input]))"},
			messages: []string{"and calls self.mailer.send;"},
		},
		"recursive pure helpers": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "count_down(3)",
				moduleExtra: "def count_down(n):\n    return 0 if n == 0 else count_up(n - 1)\n\n\ndef count_up(n):\n    return count_down(n)",
			},
			isExecuteClean: true,
		},
		"standard library sleep": {
			source:   pythonLockedMethodCase{rpcLock: endingLock, executeLock: endingLock, executeBody: "time.sleep(1)"},
			messages: []string{"and calls time.sleep;"},
		},
		"decorated execute is not analyzed": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock,
				modulePrelude: "import functools", executeDecorator: "@functools.cache\n    ",
			},
			messages:      []string{"and calls code dexcli cannot read (execute has decorator functools.cache);"},
			hasOtherError: true,
		},
		"no RPC lock": {
			source: pythonLockedMethodCase{executeLock: endingLock, executeBody: "self.mailer.send(input)"},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzePythonLockedMethodSource(t, testCase.source)
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
			executeIndex := slices.IndexFunc(graph.methodExternalCalls, func(marker methodExternalCalls) bool {
				return marker.NodeID == "step:SendReminderStep" && marker.Phase == "execute"
			})
			require.NotEqual(t, -1, executeIndex, "%+v", graph.methodExternalCalls)
			if testCase.isExecuteClean {
				require.Empty(t, graph.methodExternalCalls[executeIndex].Calls)
			}
			require.Equal(t, !testCase.hasOtherError, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func analyzePythonLockedMethodSource(t *testing.T, source pythonLockedMethodCase) *Graph {
	t.Helper()
	stepOptions := func(locks map[string]string) string {
		var options []string
		for _, field := range slices.Sorted(maps.Keys(locks)) {
			if locks[field] != "" {
				options = append(options, field+"=("+locks[field]+",)")
			}
		}
		return strings.Join(options, ", ")
	}
	rpcOptions := ""
	if source.rpcLock != "" {
		rpcOptions = ", lock_attributes=(" + source.rpcLock + ",)"
	}
	statement := func(body string) string {
		if body == "" {
			return "pass"
		}
		return body
	}
	replacer := strings.NewReplacer(
		"RPC_OPTIONS", rpcOptions,
		"SEND_OPTIONS", stepOptions(map[string]string{"wait_for_lock_attributes": source.waitLock, "execute_lock_attributes": source.executeLock}),
		"DELIVER_OPTIONS", stepOptions(map[string]string{"execute_lock_attributes": source.deliverLock}),
		"WAIT_BODY", statement(source.waitBody),
		"EXECUTE_BODY", statement(source.executeBody),
		"DELIVER_BODY", statement(source.deliverBody),
		"STEP_METHODS", source.stepMethods,
		"EXECUTE_DECORATOR", source.executeDecorator,
		"MODULE_PRELUDE", source.modulePrelude,
		"MODULE_EXTRA", source.moduleExtra,
	)
	sourcePath := filepath.Join(t.TempDir(), "workflow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(pythonLockedMethodSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	return graph
}
