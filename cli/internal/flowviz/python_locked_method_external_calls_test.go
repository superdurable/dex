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
	// projectFiles are other project modules, by path relative to workflow.py, written next to it.
	projectFiles map[string]string
	// sendOptions replaces the StepOptions arguments of SendReminderStep; empty derives them from the lock fields.
	sendOptions string
	// linkedOutsideFiles are written to a .venv directory outside the project and linked from the given path next to workflow.py.
	linkedOutsideFiles map[string]string
}

// pythonRegistrationService does network I/O from another project module.
const pythonRegistrationService = `import requests


def request_registration_number(name):
    return requests.post("https://registry.example.com/", json={"name": name}).json()["number"]
`

// pythonLocalTimeHelpers and pythonRegistrationModel are pure project modules.
const pythonLocalTimeHelpers = `from datetime import datetime


def parse_local_time(text):
    return datetime.fromisoformat(text.strip())
`

const pythonRegistrationModel = `from dataclasses import dataclass


@dataclass
class Registration:
    name: str
    number: str

    def __post_init__(self):
        self.name = self.name.strip()
`

const pythonAuditModel = `class AuditEntry:
    def __init__(self, text):
        open("/tmp/audit", "a").write(text)
`

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

const pythonReboundLocalBody = `value = input
        value = value.strip()
        ReminderFlow.counter.set(context, len(value.upper()))`

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
		// isExecuteUnchecked requires no SendReminderStep execute marker, because it shares no lock with an RPC.
		isExecuteUnchecked bool
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
			source:             pythonLockedMethodCase{rpcLock: endingLock, executeLock: counterLock, deliverLock: counterLock, executeBody: "self.mailer.send(input)", deliverBody: "self.mailer.send(input)"},
			isExecuteUnchecked: true,
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
			source:             pythonLockedMethodCase{rpcLock: endingLock, waitLock: endingLock, waitBody: "notify = self.mailer.send\n        notify(input)"},
			messages:           []string{"WaitFor of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls notify;"},
			isExecuteUnchecked: true,
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
			source:             pythonLockedMethodCase{executeLock: endingLock, executeBody: "self.mailer.send(input)"},
			isExecuteUnchecked: true,
		},
		"rebound local under an RPC lock": {
			source:         pythonLockedMethodCase{rpcLock: endingLock, executeLock: endingLock, executeBody: pythonReboundLocalBody},
			isExecuteClean: true,
		},
		"rebound local without locks": {
			source:             pythonLockedMethodCase{executeBody: pythonReboundLocalBody},
			isExecuteUnchecked: true,
		},
		"rebound local in a loop under an RPC lock": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock,
				executeBody: "value = input\n        for _ in range(2):\n            value = value.strip()\n        ReminderFlow.counter.set(context, len(value.upper()))",
			},
			isExecuteClean: true,
		},
		"rebound injected service under an RPC lock": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock,
				executeBody: "service = input\n        service = self.mailer\n        service.get(input)",
			},
			messages: []string{"and calls service.get;"},
		},
		"constructor __new__ opens a file": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "LocalCtor()",
				moduleExtra: "class LocalCtor:\n    def __new__(cls):\n        open(\"/tmp/ledger\")\n        return super().__new__(cls)",
			},
			messages: []string{"and calls LocalCtor (calls open);"},
		},
		"inherited constructor calls urllib.request": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "Child(input)",
				moduleExtra: "class Base:\n    def __init__(self, text):\n        urllib.request.urlopen(text)\n\n\nclass Child(Base):\n    pass",
			},
			messages: []string{"and calls Child (calls urllib.request.urlopen);"},
		},
		"constructor inherited through multiple inheritance": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "LocalCtor()",
				moduleExtra: "class RemoteBase:\n    def __init__(self):\n        open(\"/tmp/audit\", \"w\").close()\nclass First(RemoteBase): pass\nclass Second:\n    def __init__(self): pass\nclass LocalCtor(First, Second): pass",
			},
			messages: []string{"and calls LocalCtor (calls open('/tmp/audit', 'w').close);"},
		},
		"multiple inheritance with pure constructors": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "LocalCtor()",
				moduleExtra: "class First:\n    def __init__(self):\n        super().__init__()\nclass Second:\n    def __init__(self):\n        self.ready = True\nclass LocalCtor(First, Second): pass",
			},
			isExecuteClean: true,
		},
		"constructor inherited from a third-party base": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "Session()",
				modulePrelude: "import requests",
				moduleExtra:   "class Session(requests.Session):\n    pass",
			},
			messages: []string{"and calls Session (a constructor dexcli cannot read);"},
		},
		"project module function does network I/O": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "ReminderFlow.counter.set(context, len(services.request_registration_number(input)))",
				modulePrelude: "import services", projectFiles: map[string]string{"services.py": pythonRegistrationService},
			},
			messages: []string{"and calls services.request_registration_number (calls requests.post("},
		},
		"relative project module function does network I/O": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "services.request_registration_number(input)",
				modulePrelude: "from . import services",
				projectFiles:  map[string]string{"__init__.py": "", "services.py": pythonRegistrationService},
			},
			messages: []string{"and calls services.request_registration_number (calls requests.post("},
		},
		"project module function does network I/O under a wrapped Attribute lock": {
			source: pythonLockedMethodCase{
				rpcLock: "WRAPPED.lock()", executeLock: "WRAPPED.lock()", executeBody: "services.request_registration_number(input)",
				modulePrelude: "import services",
				moduleExtra:   "def _synced(name, value_type):\n    return Attribute(name, value_type)\n\n\nWRAPPED = _synced(\"wrapped\", str)",
				projectFiles:  map[string]string{"services.py": pythonRegistrationService},
			},
			messages: []string{"holds lock wrapped, which RPC StopReminders also takes, and calls services.request_registration_number"},
		},
		"lock element is a module constant": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: "ENDING_LOCK", executeBody: "self.mailer.send(input)",
				moduleExtra: "ENDING_LOCK = ReminderFlow.ending.lock()",
			},
			messages: []string{"Execute of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls self.mailer.send;"},
		},
		"pure helper and dataclass from project modules": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock,
				executeBody:   "registration = Registration(input, str(parse_local_time(input)))\n        ReminderFlow.counter.set(context, len(registration.name))",
				modulePrelude: "from helpers import parse_local_time\nfrom models import Registration",
				projectFiles:  map[string]string{"helpers.py": pythonLocalTimeHelpers, "models.py": pythonRegistrationModel},
			},
			isExecuteClean: true,
		},
		"pure helper and dataclass from relative project modules": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock,
				executeBody:   "registration = Registration(input, str(parse_local_time(input)))\n        ReminderFlow.counter.set(context, len(registration.name))",
				modulePrelude: "from .helpers import parse_local_time\nfrom .models import Registration",
				projectFiles:  map[string]string{"__init__.py": "", "helpers.py": pythonLocalTimeHelpers, "models.py": pythonRegistrationModel},
			},
			isExecuteClean: true,
		},
		"project module constructor opens a file": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "AuditEntry(input)",
				modulePrelude: "from audit import AuditEntry", projectFiles: map[string]string{"audit.py": pythonAuditModel},
			},
			messages: []string{"and calls AuditEntry (calls open('/tmp/audit', 'a').write);"},
		},
		"decorated project function may replace its body": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "notify(input)", modulePrelude: "from services import notify",
				projectFiles: map[string]string{"services.py": "from functools import wraps\n\n\ndef publish(fn):\n    @wraps(fn)\n    def wrapper(*args):\n        open(\"audit.log\", \"a\").write(\"hit\")\n        return fn(*args)\n    return wrapper\n\n\n@publish\ndef notify(name):\n    return name\n"},
			},
			messages: []string{"and calls notify;"},
		},
		"project function default argument is a client": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "read(input)", modulePrelude: "from services import read",
				projectFiles: map[string]string{"services.py": "import requests\n\n\ndef read(url, session=requests.Session()):\n    return session.get(url)\n"},
			},
			messages: []string{"and calls read (calls session.get);"},
		},
		"project helper returns a module-level client": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "provider().get(input)", modulePrelude: "from services import provider",
				projectFiles: map[string]string{"services.py": "import requests\n\nsession = requests.Session()\n\n\ndef provider():\n    return session\n"},
			},
			messages: []string{"and calls provider().get;"},
		},
		"project class attribute holds a client": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "dep = Services.dep\n        dep.get(input)", modulePrelude: "from services import Services",
				projectFiles: map[string]string{"services.py": "import requests\n\n\nclass Services:\n    dep = requests.Session()\n"},
			},
			messages: []string{"and calls dep.get;"},
		},
		"package directory wins over a same-named module file": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "from_service(input)", modulePrelude: "from services import from_service",
				projectFiles: map[string]string{
					"services.py":          "def from_service(text):\n    return text\n",
					"services/__init__.py": "import requests\n\n\ndef from_service(url):\n    return requests.get(url)\n",
				},
			},
			messages: []string{"and calls from_service (calls requests.get);"},
		},
		"symbolic link to an installed package is not followed": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "parse(input)", modulePrelude: "from shim import parse",
				linkedOutsideFiles: map[string]string{"shim.py": "def parse(text):\n    return text.strip()\n"},
			},
			messages: []string{"and calls parse;"},
		},
		"package re-export through from . import": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "ReminderFlow.counter.set(context, len(parse(input)))", modulePrelude: "from . import parse",
				projectFiles: map[string]string{"__init__.py": "from .impl import parse\n", "impl.py": "def parse(text):\n    return text.strip()\n"},
			},
			isExecuteClean: true,
		},
		"lock list built from local variables": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeBody: "self.mailer.send(input)",
				sendOptions: "execute_lock_attributes=LOCAL_LOCKS",
				moduleExtra: "LOCAL_LOCKS = (ReminderFlow.ending.lock(),)",
			},
			messages: []string{"Execute of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls self.mailer.send;"},
		},
		"Channel results from a module tuple and a helper": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock,
				executeBody:   "taken = [*QUEUES[0].results(context), *inbox_for(1).results(context)]\n        ReminderFlow.counter.set(context, len(taken))",
				modulePrelude: "from dex import Channel",
				moduleExtra:   "QUEUES = (Channel(\"queue-0\", str), Channel(\"queue-1\", str))\n\n\ndef inbox_for(position):\n    return QUEUES[position]",
			},
			isExecuteClean: true,
		},
		"metaclass call opens a file": {
			source: pythonLockedMethodCase{
				rpcLock: endingLock, executeLock: endingLock, executeBody: "Built()",
				moduleExtra: "class Meta(type):\n    def __call__(cls):\n        open(\"/tmp/ledger\")\n\n\nclass Built(metaclass=Meta):\n    pass",
			},
			messages:      []string{"and calls Built (calls open);"},
			hasOtherError: true,
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
			if testCase.isExecuteUnchecked {
				require.Equal(t, -1, executeIndex, "%+v", graph.methodExternalCalls)
				require.Equal(t, !testCase.hasOtherError, graph.Valid, "%+v", graph.Diagnostics)
				return
			}
			require.NotEqual(t, -1, executeIndex, "%+v", graph.methodExternalCalls)
			if testCase.isExecuteClean {
				require.Empty(t, graph.methodExternalCalls[executeIndex].Calls)
			}
			require.Equal(t, !testCase.hasOtherError, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func TestPythonUnresolvedRegistrationLockIsReported(t *testing.T) {
	graph := analyzePythonLockedMethodSource(t, pythonLockedMethodCase{
		rpcLock: "ReminderFlow.ending.lock()", executeLock: "make_lock()", executeBody: "self.mailer.send(input)",
		moduleExtra: "def make_lock():\n    return ReminderFlow.ending.lock()",
	})
	var messages []string
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "registration_lock_unresolved" {
			require.Equal(t, "warning", diagnostic.Severity)
			messages = append(messages, diagnostic.Message)
		}
	}
	require.Equal(t, []string{
		"Step SendReminderStep lists make_lock() in execute_lock_attributes, but dexcli cannot resolve it to an Attribute lock of this Flow; lock checks such as locked_method_external_call skip it",
	}, messages)
	require.Empty(t, lockedMethodWarningMessages(graph))
	require.True(t, graph.Valid, "%+v", graph.Diagnostics)
}

func TestPythonUnpackedRegistrationOptionsAreReported(t *testing.T) {
	graph := analyzePythonLockedMethodSource(t, pythonLockedMethodCase{
		rpcLock: "ReminderFlow.ending.lock()", executeBody: "self.mailer.send(input)",
		sendOptions: `**{"execute_lock_attributes": (ReminderFlow.ending.lock(),)}`,
	})
	var messages []string
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "registration_lock_unresolved" {
			messages = append(messages, diagnostic.Message)
		}
	}
	require.Len(t, messages, 1, "%+v", graph.Diagnostics)
	require.Contains(t, messages[0], "Step SendReminderStep unpacks **{'execute_lock_attributes': (ReminderFlow.ending.lock(),)} into its options")
}

func TestPythonFlowInitializerFactoryAttribute(t *testing.T) {
	source := `from dex import Attribute, Context, Flow, PersistenceSchema, Step, StepDecision, StepList, graceful_complete


def _synced(name, value_type):
    return Attribute(name, value_type)


class ReminderFlow(Flow[str]):
    def __init__(self):
        self.ending = _synced("ending", bool)
        self.send = SendStep()

    def get_steps(self):
        return StepList.start_step(self.send)

    def get_persistence_schema(self):
        return PersistenceSchema.of(self.ending)


# dex:group group-id:reminders group-label:"Reminders"
# dex:explanation text:"Send one reminder."
class SendStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)
`
	sourcePath := filepath.Join(t.TempDir(), "workflow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(source), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(graph.Nodes, func(node Node) bool { return node.Kind == "attribute" && node.Name == "ending" }), "%+v", graph.Nodes)
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
		"SEND_OPTIONS", cmp.Or(source.sendOptions, stepOptions(map[string]string{"wait_for_lock_attributes": source.waitLock, "execute_lock_attributes": source.executeLock})),
		"DELIVER_OPTIONS", stepOptions(map[string]string{"execute_lock_attributes": source.deliverLock}),
		"WAIT_BODY", statement(source.waitBody),
		"EXECUTE_BODY", statement(source.executeBody),
		"DELIVER_BODY", statement(source.deliverBody),
		"STEP_METHODS", source.stepMethods,
		"EXECUTE_DECORATOR", source.executeDecorator,
		"MODULE_PRELUDE", source.modulePrelude,
		"MODULE_EXTRA", source.moduleExtra,
	)
	sourceDirectory := t.TempDir()
	for relativePath, contents := range source.projectFiles {
		projectPath := filepath.Join(sourceDirectory, relativePath)
		require.NoError(t, os.MkdirAll(filepath.Dir(projectPath), 0o755))
		require.NoError(t, os.WriteFile(projectPath, []byte(contents), 0o644))
	}
	outsideDirectory := filepath.Join(t.TempDir(), ".venv")
	for relativePath, contents := range source.linkedOutsideFiles {
		outsidePath := filepath.Join(outsideDirectory, relativePath)
		require.NoError(t, os.MkdirAll(filepath.Dir(outsidePath), 0o755))
		require.NoError(t, os.WriteFile(outsidePath, []byte(contents), 0o644))
		require.NoError(t, os.Symlink(outsidePath, filepath.Join(sourceDirectory, relativePath)))
	}
	sourcePath := filepath.Join(sourceDirectory, "workflow.py")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(pythonLockedMethodSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	return graph
}
