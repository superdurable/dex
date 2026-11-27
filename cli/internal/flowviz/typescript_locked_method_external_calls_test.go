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

const typeScriptLockedMethodSource = `import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { sendMail } from "./mailer.js";
import {
  Attribute,
  Client,
  StepList,
  Wait,
  booleanCodec,
  doubleCodec,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

const ending = new Attribute("Ending", booleanCodec);
const counter = new Attribute("Counter", doubleCodec);

interface Mailer {
  send(to: string): Promise<void>;
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Send one reminder."
class SendReminderStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: ReminderFlow) {}

  public getStepType(): string {
    return "SendReminderStep";
  }

  public getStepOptions(): StepOptions {
    return {waitForLockAttributes: [WAIT_LOCK], executeLockAttributes: [EXECUTE_LOCK]};
  }

  public waitFor(context: Context, input: string): Wait {
    WAIT_BODY
    return Wait.skipImmediately();
  }

  public execute(context: Context, input: string): StepDecision {
    EXECUTE_BODY
    return goTo(DeliverStep, input);
  }

  private countWords(input: string): number {
    return input.split(" ").filter(word => word !== "").length;
  }
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Deliver the reminder."
class DeliverStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly flow: ReminderFlow) {}

  public getStepType(): string {
    return "DeliverStep";
  }

  public getStepOptions(): StepOptions {
    return {executeLockAttributes: [DELIVER_LOCK]};
  }

  public execute(context: Context, input: string): StepDecision {
    DELIVER_BODY
    return gracefulComplete();
  }
}

export class ReminderFlow implements Flow<string> {
  public readonly send = new SendReminderStep(this);
  public readonly deliver = new DeliverStep(this);

  public constructor(public readonly mailer: Mailer, public readonly client: Client) {}

  public getFlowType(): string {
    return "ReminderFlow";
  }

  public getSteps() {
    return StepList.startStep(this.send).otherSteps(this.deliver);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {attributes: [ending, counter]};
  }

  @rpc({inputCodec: stringCodec, outputCodec: stringCodec, lockAttributes: [RPC_LOCK]})
  public stopReminders(context: Context, input: string): RPCResult<string> {
    ending.set(context, true);
    return {output: input};
  }
}

EXTRA_SOURCE
`

type typeScriptLockedMethodCase struct {
	rpcLock     string
	waitLock    string
	executeLock string
	deliverLock string
	waitBody    string
	executeBody string
	deliverBody string
	extraSource string
	// projectFiles are other project source files, by path relative to workflow.ts.
	projectFiles map[string]string
	// sendInputCodec replaces SendReminderStep's stringCodec input codec when set.
	sendInputCodec string
	// linkedOutsideFiles are written under node_modules outside the project and linked from the given path.
	linkedOutsideFiles map[string]string
	// linkedProjectFiles maps a link path next to workflow.ts to a project file it points to.
	linkedProjectFiles map[string]string
}

const typeScriptReminderStateSource = `import { jsonCodec } from "@superdurable/dex";

interface ReminderState {
  sent: string[];
}

const emptyState: ReminderState = {sent: []};
const reminderState = new Attribute("ReminderState", jsonCodec<ReminderState>());`

const typeScriptNamedFallbackSource = typeScriptReminderStateSource + `

interface ReminderTree {
  label: string;
  children: ReminderTree[];
  byName: Record<string, ReminderTree>;
}

interface FormattedState {
  sent: string[];
  format: (text: string) => string;
}

const emptyTree: ReminderTree = {label: "", children: [], byName: {}};
const reminderTree = new Attribute("ReminderTree", jsonCodec<ReminderTree>());
const formattedState = new Attribute("FormattedState", jsonCodec<FormattedState>());
const emptyFormattedState: FormattedState = {sent: [], format: text => text};
const untypedState: any = {sent: []};`

const typeScriptFallbackExecuteBody =`const current = reminderState.get(context) ?? {sent: []};
    const other = reminderState.get(context) || {sent: []};
    const chosen = input === "" ? current : {sent: []};
    counter.set(context, current.sent.filter(Boolean).length + other.sent.slice(0).length + chosen.sent.map(String).length);`

const typeScriptTicketsModule = `export interface Ticket {
  id: string;
}

export function ticketLabel(text: string): string {
  return text.trim().toUpperCase();
}

export function notifyCustomer(text: string): Promise<Response> {
  return fetch("https://example.com/" + text);
}
`

const typeScriptTicketHelperSource = `import { notifyCustomer, ticketLabel, type Ticket } from "./tickets.js";

function ticketIds(tickets: readonly Ticket[]): string[] {
  return tickets.map(ticket => ticket.id.trim());
}`

const typeScriptPureExecuteBody = `const names: string[] = [input.trim().toUpperCase(), String(counter.get(context)), context.flowId.toLowerCase()];
    names.sort((left, right) => left.localeCompare(right));
    void JSON.stringify({names, at: new Date().toISOString(), max: Math.max(1, names.length)});
    const lengths = new Map<string, number>(names.map(name => [name, name.length]));
    const label = new Intl.NumberFormat("en-US").format(lengths.size);
    counter.set(context, lengths.size + formatReminder(label).length + this.countWords(input));
    const digest = createHash("sha256").update(Buffer.from(input).toString("base64")).digest("hex");
    const keys = JSON.stringify({digest}, ["digest"]) + names.map(String).filter(Boolean).join(",");
    ending.set(context, Number.parseInt(digest.slice(0, 1), 16) > keys.length || names.some(name => isBlank(name)));`

const typeScriptPureHelperSource = `function formatReminder(input: string): string {
  return ` + "`reminder for ${input.trim()}`" + `.padEnd(3);
}

function isBlank(value: string): boolean {
  return value.trim() === "";
}`

const typeScriptLockedMessageSuffix = "; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step."

const typeScriptSecondFlowSource = `// dex:group group-id:other group-label:"Other"
// dex:explanation text:"Finish the other Flow."
class OtherStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "OtherStep";
  }

  public execute(_context: Context, _input: string): StepDecision {
    return gracefulComplete();
  }
}

export class OtherFlow implements Flow<string> {
  public readonly other = new OtherStep();

  public getFlowType(): string {
    return "OtherFlow";
  }

  public getSteps() {
    return StepList.startStep(this.other);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {};
  }
}`

func TestTypeScriptLockedMethodExternalCall(t *testing.T) {
	testCases := map[string]struct {
		source typeScriptLockedMethodCase
		// messages lists every locked_method_external_call message fragment, in report order.
		messages []string
		// isExecuteClean requires a checked SendReminderStep execute with no possible external call.
		isExecuteClean bool
		// uncertainMessages lists every locked_method_uncertain_call message fragment, in report order.
		uncertainMessages []string
	}{
		"execute calls an injected service while holding an RPC lock": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void this.flow.mailer.send(input);"},
			messages: []string{"Execute of SendReminderStep holds lock Ending, which RPC stopReminders also takes, and calls this.flow.mailer.send" + typeScriptLockedMessageSuffix},
		},
		"call moved to the next unlocked Step": {
			source: typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "ending.set(context, false);", deliverBody: "void this.flow.mailer.send(input);"},
		},
		"lock shared only between Steps": {
			source: typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "counter.lock()", deliverLock: "counter.lock()", executeBody: "void this.flow.mailer.send(input);", deliverBody: "void this.flow.mailer.send(input);"},
		},
		"locked execute with only SDK, pure built-in, and pure helper calls": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: typeScriptPureExecuteBody,
				extraSource: typeScriptPureHelperSource,
			},
		},
		"helper calls fetch": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "notifyCustomer(input);",
				extraSource: "function notifyCustomer(input: string): void {\n  void fetch(\"https://example.com/\" + input);\n}",
			},
			messages: []string{"and calls notifyCustomer (calls fetch)" + typeScriptLockedMessageSuffix},
		},
		"execute calls the Dex Client under the shared lock": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void this.flow.client.stopFlow(input);"},
			messages: []string{"and calls this.flow.client.stopFlow" + typeScriptLockedMessageSuffix},
		},
		"execute constructs a Dex Client": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void new Client(undefined as never, undefined as never);"},
			messages: []string{"and calls new Client" + typeScriptLockedMessageSuffix},
		},
		"wait for calls a function value while holding an RPC lock": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", waitLock: "ending.lock()", waitBody: "const notify = this.flow.mailer.send;\n    void notify(input);"},
			messages: []string{"WaitFor of SendReminderStep holds lock Ending, which RPC stopReminders also takes, and calls notify" + typeScriptLockedMessageSuffix},
		},
		"timer sleep": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void new Promise(resolve => setTimeout(resolve, 1000));"},
			messages: []string{"and calls setTimeout" + typeScriptLockedMessageSuffix},
		},
		"execute reads a file": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "ending.set(context, readFileSync(input, \"utf8\") === \"\");"},
			messages: []string{"and calls readFileSync" + typeScriptLockedMessageSuffix},
		},
		"map receives an imported callback": {
			source:            typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void [input].map(sendMail);"},
			uncertainMessages: []string{"and calls sendMail. dexcli cannot determine whether this call performs I/O."},
		},
		"then receives an injected method": {
			source:   typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void Promise.resolve(input).then(this.flow.mailer.send);"},
			messages: []string{"and calls this.flow.mailer.send" + typeScriptLockedMessageSuffix},
		},
		"no RPC lock": {
			source: typeScriptLockedMethodCase{executeLock: "ending.lock()", executeBody: "void this.flow.mailer.send(input);"},
		},
		"file with two Flows draws the Step locks": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void this.flow.mailer.send(input);",
				extraSource: typeScriptSecondFlowSource,
			},
			messages: []string{"Execute of SendReminderStep holds lock Ending, which RPC stopReminders also takes, and calls this.flow.mailer.send" + typeScriptLockedMessageSuffix},
		},
		"Attribute values with fallbacks use collection methods": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: typeScriptFallbackExecuteBody,
				extraSource: typeScriptReminderStateSource,
			},
			isExecuteClean: true,
		},
		"pure helper and data type from a project file": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "counter.set(context, ticketIds([{id: input}]).length + ticketLabel(input).length);",
				extraSource: typeScriptTicketHelperSource, projectFiles: map[string]string{"tickets.ts": typeScriptTicketsModule},
			},
			isExecuteClean: true,
		},
		"fallback branch reaches a Dex Client": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const target = input ? ending : this.flow.client;\n    if (\"stopFlow\" in target) void target.stopFlow(input);",
			},
			messages: []string{"and calls target.stopFlow" + typeScriptLockedMessageSuffix},
		},
		"fallback branch is a written array": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const local: Mailer[] = [];\n    local.push(this.flow.mailer);\n    const chosen = reminderState.get(context) ?? local;\n    const item = chosen[0] as Mailer;\n    void item.send(input);",
				extraSource: typeScriptReminderStateSource,
			},
			messages: []string{"and calls item.send" + typeScriptLockedMessageSuffix},
		},
		"fallback branch is a named data constant": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const current = reminderState.get(context) ?? emptyState;\n    const chosen = input === \"\" ? current : emptyState;\n    counter.set(context, current.sent.filter(Boolean).length + chosen.sent.slice(0).length);",
				extraSource: typeScriptNamedFallbackSource,
			},
			isExecuteClean: true,
		},
		"fallback branch is a named constant of a recursive data type": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const tree = reminderTree.get(context) ?? emptyTree;\n    counter.set(context, tree.children.filter(child => child.label !== \"\").length);",
				extraSource: typeScriptNamedFallbackSource,
			},
			isExecuteClean: true,
		},
		"fallback branch type has a function-valued property": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const current = formattedState.get(context) ?? emptyFormattedState;\n    counter.set(context, current.sent.filter(Boolean).length);",
				extraSource: typeScriptNamedFallbackSource,
			},
			messages: []string{"and calls current.sent.filter" + typeScriptLockedMessageSuffix},
		},
		"fallback branch is a narrow data type holding an injected service": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const fallback: {} = this.flow.mailer;\n    const destination = input ? ending : fallback;\n    if (\"send\" in destination && typeof destination.send === \"function\") void destination.send(input);",
			},
			messages: []string{"and calls destination.send" + typeScriptLockedMessageSuffix},
		},
		"fallback branch narrowed to a method an alias attached": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const fallback = {id: \"local\"};\n    const prepared: {id: string; send?: Mailer[\"send\"]} = fallback;\n    prepared.send = this.flow.mailer.send;\n    if (\"send\" in fallback && typeof fallback.send === \"function\") {\n      const destination = input ? ending : fallback;\n      if (\"send\" in destination && typeof destination.send === \"function\") void destination.send(input);\n    }",
			},
			messages: []string{"and calls destination.send" + typeScriptLockedMessageSuffix},
		},
		"fallback branch is typed any": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const current = reminderState.get(context) ?? untypedState;\n    counter.set(context, current.sent.filter(Boolean).length);",
				extraSource: typeScriptNamedFallbackSource,
			},
			messages: []string{"and calls current.sent.filter" + typeScriptLockedMessageSuffix},
		},
		"fallback branch is a written array through an alias": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()",
				executeBody: "const local: Mailer[] = [];\n    const alias = input ? local : local;\n    alias.push(this.flow.mailer);\n    const chosen = reminderState.get(context) ?? local;\n    const item = chosen[0] as Mailer;\n    void item.send(input);",
				extraSource: typeScriptReminderStateSource,
			},
			messages: []string{"and calls item.send" + typeScriptLockedMessageSuffix},
		},
		"Step input decoded by a configured SDK codec": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void (input as unknown as Mailer).send(\"hello\");",
				extraSource:    "import { jsonCodec as configuredJsonCodec } from \"@superdurable/dex\";",
				sendInputCodec: "configuredJsonCodec<string>({decode: () => ({send: async (url: string) => { await fetch(url); }})} as never)",
			},
			messages: []string{"(input as unknown as Mailer).send" + typeScriptLockedMessageSuffix},
		},
		"Step input decoded by an unconfigured SDK codec": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "counter.set(context, input.trim().length);",
				extraSource:    "import { jsonCodec as plainJsonCodec } from \"@superdurable/dex\";",
				sendInputCodec: "plainJsonCodec<string>()",
			},
			isExecuteClean: true,
		},
		"symbolic link inside the project is not followed": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void parse(input);",
				extraSource:        "import { parse } from \"./shim.js\";",
				projectFiles:       map[string]string{"real.ts": "export function parse(text: string): string { return text.trim(); }\n"},
				linkedProjectFiles: map[string]string{"shim.ts": "real.ts"},
			},
			messages: []string{"and calls parse" + typeScriptLockedMessageSuffix},
		},
		"Step input decoded by a custom codec": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void (input as unknown as Mailer).send(\"hello\");",
				extraSource:    "const mailerCodec = {...stringCodec, decode: () => ({send: async (url: string) => { await fetch(url); }})};",
				sendInputCodec: "mailerCodec",
			},
			messages: []string{"(input as unknown as Mailer).send" + typeScriptLockedMessageSuffix},
		},
		"symbolic link into node_modules is not followed": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void parse(input);",
				extraSource:        "import { parse } from \"./shim.js\";",
				linkedOutsideFiles: map[string]string{"shim.ts": "export function parse(text: string): string { return text.trim(); }\n"},
			},
			uncertainMessages: []string{"and calls parse. dexcli cannot determine whether this call performs I/O."},
		},
		"package function that may be pure": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "counter.set(context, uniq([input]).length);",
				extraSource: "import { uniq } from \"lodash\";",
			},
			uncertainMessages: []string{"Execute of SendReminderStep holds lock Ending, which RPC stopReminders also takes, and calls uniq. dexcli cannot determine whether this call performs I/O. Dex fails the RPC while the lock is held. Move any call that reaches an external system to the next, unlocked Step."},
		},
		"helper logs before fetch": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "announce(input);\n    announce(input);",
				extraSource: "function announce(text: string): void {\n  console.log(text);\n  void fetch(\"https://example.com/\" + text);\n}",
			},
			messages: []string{"and calls announce (calls fetch) (and 1 more)" + typeScriptLockedMessageSuffix},
		},
		"call of a returned callback is dynamic": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "getCallback()(input);",
				extraSource: "import { getCallback } from \"callbacks\";",
			},
			messages: []string{"and calls getCallback() (and 1 uncertain)" + typeScriptLockedMessageSuffix},
		},
		"console output is uncertain": {
			source:            typeScriptLockedMethodCase{rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "console.log(input);"},
			uncertainMessages: []string{"and calls console.log. dexcli cannot determine whether this call performs I/O."},
		},
		"injected service and an uncertain call": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "console.log(uniq([input]));\n    void this.flow.mailer.send(input);",
				extraSource: "import { uniq } from \"lodash\";",
			},
			messages: []string{"and calls this.flow.mailer.send (and 2 uncertain)" + typeScriptLockedMessageSuffix},
		},
		"project file helper calls fetch": {
			source: typeScriptLockedMethodCase{
				rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void notifyCustomer(input);",
				extraSource: typeScriptTicketHelperSource, projectFiles: map[string]string{"tickets.ts": typeScriptTicketsModule},
			},
			messages: []string{"and calls notifyCustomer (calls fetch)" + typeScriptLockedMessageSuffix},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzeTypeScriptLockedMethodSource(t, testCase.source)
			requireLockedMethodDiagnostics(t, graph, testCase.messages, testCase.uncertainMessages)
			if testCase.isExecuteClean {
				executeIndex := slices.IndexFunc(graph.methodExternalCalls, func(marker methodExternalCalls) bool {
					return marker.NodeID == "step:SendReminderStep" && marker.Phase == "execute"
				})
				require.NotEqual(t, -1, executeIndex, "%+v", graph.methodExternalCalls)
				require.Empty(t, graph.methodExternalCalls[executeIndex].Calls)
			}
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func TestTypeScriptLockedMethodExternalCallMarksOnlyPhasesWithSharedLocks(t *testing.T) {
	graph := analyzeTypeScriptLockedMethodSource(t, typeScriptLockedMethodCase{
		rpcLock: "ending.lock()", executeLock: "ending.lock()", deliverLock: "counter.lock()", executeBody: typeScriptPureExecuteBody,
		waitBody: "void this.flow.mailer.send(input);", deliverBody: "void this.flow.mailer.send(input);", extraSource: typeScriptPureHelperSource,
	})
	require.Empty(t, graph.Diagnostics)
	markers := make(map[string][]string)
	for _, marker := range graph.methodExternalCalls {
		callees := make([]string, 0, len(marker.Calls))
		for _, call := range marker.Calls {
			callees = append(callees, call.Callee)
		}
		markers[marker.NodeID+" "+marker.Phase] = callees
	}
	require.Equal(t, map[string][]string{"step:SendReminderStep execute": {}}, markers)
}

// A single-Flow graph leaves Step locks undrawn; the shared-lock intersection must still see them.
func TestTypeScriptLockedMethodSingleFlowUsesUndrawnStepLocks(t *testing.T) {
	graph := analyzeTypeScriptLockedMethodSource(t, typeScriptLockedMethodCase{
		rpcLock: "ending.lock()", executeLock: "ending.lock()", executeBody: "void this.flow.mailer.send(input);",
	})
	requireStepLocksOnlyUndrawn(t, graph)
	messages := lockedMethodWarningMessages(graph)
	require.Len(t, messages, 1, "%+v", graph.Diagnostics)
	require.Contains(t, messages[0], "Execute of SendReminderStep holds lock Ending")
}

func analyzeTypeScriptLockedMethodSource(t *testing.T, source typeScriptLockedMethodCase) *Graph {
	t.Helper()
	compilerDirectory := trustedTypeScriptCompilerDirectory(t)
	replacer := strings.NewReplacer(
		"RPC_LOCK", source.rpcLock,
		"WAIT_LOCK", source.waitLock,
		"EXECUTE_LOCK", source.executeLock,
		"DELIVER_LOCK", source.deliverLock,
		"WAIT_BODY", source.waitBody,
		"EXECUTE_BODY", source.executeBody,
		"DELIVER_BODY", source.deliverBody,
		"EXTRA_SOURCE", source.extraSource,
	)
	sourceDirectory := t.TempDir()
	for relativePath, contents := range source.projectFiles {
		writeTestFile(t, filepath.Join(sourceDirectory, relativePath), contents)
	}
	outsideDirectory := filepath.Join(t.TempDir(), "node_modules", "pkg")
	for relativePath, contents := range source.linkedOutsideFiles {
		writeTestFile(t, filepath.Join(outsideDirectory, relativePath), contents)
		require.NoError(t, os.Symlink(filepath.Join(outsideDirectory, relativePath), filepath.Join(sourceDirectory, relativePath)))
	}
	for linkPath, targetPath := range source.linkedProjectFiles {
		require.NoError(t, os.Symlink(filepath.Join(sourceDirectory, targetPath), filepath.Join(sourceDirectory, linkPath)))
	}
	contents := replacer.Replace(typeScriptLockedMethodSource)
	if source.sendInputCodec != "" {
		contents = strings.Replace(contents, "public readonly inputCodec = stringCodec;", "public readonly inputCodec = "+source.sendInputCodec+";", 1)
	}
	sourcePath := filepath.Join(sourceDirectory, "workflow.ts")
	writeTestFile(t, sourcePath, contents)
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow", TypeScriptCompilerDirectory: compilerDirectory})
	require.NoError(t, err)
	return graph
}
