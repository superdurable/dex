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

const goLockedMethodSource = `package reminders

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	_ = json.Marshal
	_ = fmt.Sprintf
	_ = slog.Info
	_ = http.Get
	_ = sort.Strings
	_ = strings.ToUpper
	_ = time.Now
)

var (
	ending  = dex.DefineAttribute[bool]("ending")
	counter = dex.DefineAttribute[int]("counter")
)

type Mailer interface{ Send(to string) error }

type ReminderFlow struct {
	dex.FlowDefaults
	mailer Mailer
}

func (flow *ReminderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(SendReminderStep{mailer: flow.mailer}),
		dex.DefineStep(DeliverStep{mailer: flow.mailer}),
	}
}

func (*ReminderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{ending, counter}}
}

func (flow *ReminderFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.StopReminders, &dex.RPCOptions{LockAttributes: []dex.AttributeLock{RPC_LOCK}}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*ReminderFlow) GetDexSummary(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*ReminderFlow) GetDexDisplay(dex.Context, dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*ReminderFlow) StopReminders(ctx dex.Context, input string) (*dex.RPCResult[string], error) {
	_ = ending.Set(ctx, true)
	return &dex.RPCResult[string]{Output: input}, nil
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Send one reminder."
type SendReminderStep struct {
	dex.StepDefaults
	mailer Mailer
}

func (SendReminderStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{WaitForLockAttributes: []dex.AttributeLock{WAIT_LOCK}, ExecuteLockAttributes: []dex.AttributeLock{EXECUTE_LOCK}}
}

func (step SendReminderStep) WaitFor(ctx dex.Context, input string) (*dex.Wait, error) {
	WAIT_BODY
	return dex.SkipWaitImmediately(), nil
}

func (step SendReminderStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	EXECUTE_BODY
	return dex.GoTo(DeliverStep{}, input), nil
}

// dex:group group-id:reminders group-label:"Reminders"
// dex:explanation text:"Deliver the reminder."
type DeliverStep struct {
	dex.StepDefaultsNoWaitFor[string]
	mailer Mailer
}

func (DeliverStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteLockAttributes: []dex.AttributeLock{DELIVER_LOCK}}
}

DELIVER_EXECUTE

EXTRA_SOURCE
`

type goLockedMethodCase struct {
	rpcLock     string
	waitLock    string
	executeLock string
	deliverLock string
	waitBody    string
	executeBody string
	deliverBody string
	// deliverExecute, when set, replaces the whole DeliverStep Execute method.
	deliverExecute string
	extraSource    string
	// sourceReplacements are old and new text pairs applied to the source before the placeholders.
	sourceReplacements []string
	// packageFiles are other files of the reminders module, by path relative to its root.
	packageFiles map[string]string
}

const goDefaultDeliverExecute = `func (step DeliverStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	DELIVER_BODY
	return dex.GracefulComplete(input), nil
}`

const goPureExecuteBody = `encoded, _ := json.Marshal(map[string]string{"to": strings.ToUpper(input)})
	slog.Info("reminder", "at", time.Now().Format(time.RFC3339), "body", fmt.Sprintf("%s", encoded))
	names := []string{input}
	sort.Strings(names)
	value, _ := counter.Get(ctx)
	_ = counter.Set(ctx, value+len(names))
	_ = ending.Set(ctx, formatReminder(input) == "")`

const goPureHelperSource = `func formatReminder(input string) string {
	return strings.TrimSpace(fmt.Sprintf("reminder for %s", input))
}`

func TestGoLockedMethodExternalCall(t *testing.T) {
	testCases := map[string]struct {
		source goLockedMethodCase
		// messages lists every locked_method_external_call message fragment, in report order.
		messages []string
		// uncertainMessages lists every locked_method_uncertain_call message fragment, in report order.
		uncertainMessages []string
	}{
		"printing is uncertain": {
			source:            goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "fmt.Println(input)"},
			uncertainMessages: []string{"Execute of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls fmt.Println. dexcli cannot determine whether this call performs I/O. Dex fails the RPC while the lock is held. Move any call that reaches an external system to the next, unlocked Step."},
		},
		"helper prints before network I/O": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "notifyCustomer(input)\n\tnotifyCustomer(input)",
				extraSource: "func notifyCustomer(input string) {\n\tfmt.Println(input)\n\t_, _ = http.Get(\"https://example.com/\" + input)\n}",
			},
			messages: []string{"and calls notifyCustomer (calls http.Get) (and 1 more); Dex fails that RPC"},
		},
		"builtin println is uncertain": {
			source:            goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "println(input)"},
			uncertainMessages: []string{"and calls println. dexcli cannot determine whether this call performs I/O."},
		},
		"concrete field of another package and its local alias": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)",
				executeBody: "sender := step.gateway\n\t_ = sender.Send(input)\n\t_ = gateway.Format(input)",
				sourceReplacements: []string{
					"\"github.com/superdurable/dex/sdk-go/dex\"\n)", "\"github.com/superdurable/dex/sdk-go/dex\"\n\t\"reminders/gateway\"\n)",
					"type SendReminderStep struct {\n\tdex.StepDefaults\n\tmailer Mailer\n}", "type SendReminderStep struct {\n\tdex.StepDefaults\n\tmailer  Mailer\n\tgateway *gateway.Gateway\n}",
				},
				packageFiles: map[string]string{"gateway/gateway.go": "package gateway\n\nimport \"net/http\"\n\ntype Gateway struct{}\n\nfunc (*Gateway) Send(to string) error {\n\t_, err := http.Get(\"https://example.com/\" + to)\n\treturn err\n}\n\nfunc Format(text string) string { return text }\n"},
			},
			messages: []string{"and calls sender.Send (and 1 uncertain); Dex fails that RPC"},
		},
		"field alias rebound after the call": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)",
				executeBody: "sender := step.gateway\n\t_ = sender.Send(input)\n\tsender = &gateway.Gateway{}\n\t_ = sender",
				sourceReplacements: []string{
					"\"github.com/superdurable/dex/sdk-go/dex\"\n)", "\"github.com/superdurable/dex/sdk-go/dex\"\n\t\"reminders/gateway\"\n)",
					"type SendReminderStep struct {\n\tdex.StepDefaults\n\tmailer Mailer\n}", "type SendReminderStep struct {\n\tdex.StepDefaults\n\tmailer  Mailer\n\tgateway *gateway.Gateway\n}",
				},
				packageFiles: map[string]string{"gateway/gateway.go": "package gateway\n\nimport \"net/http\"\n\ntype Gateway struct{}\n\nfunc (*Gateway) Send(to string) error {\n\t_, err := http.Get(\"https://example.com/\" + to)\n\treturn err\n}\n"},
			},
			messages: []string{"and calls sender.Send; Dex fails that RPC"},
		},
		"injected service and an uncertain call": {
			source:   goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "fmt.Println(input)\n\t_ = step.mailer.Send(input)\n\tfmt.Println(input)"},
			messages: []string{"and calls step.mailer.Send (and 2 uncertain); Dex fails that RPC"},
		},
		"execute calls an injected service while holding an RPC lock": {
			source:   goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "_ = step.mailer.Send(input)"},
			messages: []string{"Execute of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls step.mailer.Send; Dex fails that RPC while the lock is held. Make the call in the next, unlocked Step."},
		},
		"call moved to the next unlocked Step": {
			source: goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "_ = ending.Set(ctx, false)", deliverBody: "_ = step.mailer.Send(input)"},
		},
		"lock shared only between Steps": {
			source: goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(counter)", deliverLock: "dex.LockAttribute(counter)", executeBody: "_ = step.mailer.Send(input)", deliverBody: "_ = step.mailer.Send(input)"},
		},
		"locked Execute with only SDK, pure standard library, and pure helper calls": {
			source: goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: goPureExecuteBody, extraSource: goPureHelperSource},
		},
		"helper calls net/http": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "notifyCustomer(input)",
				extraSource: "func notifyCustomer(input string) {\n\t_, _ = http.Get(\"https://example.com/\" + input)\n}",
			},
			messages: []string{"and calls notifyCustomer (calls http.Get); Dex fails that RPC"},
		},
		"wait for calls a function value while holding an RPC lock": {
			source:   goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", waitLock: "dex.LockAttribute(ending)", waitBody: "notify := step.mailer.Send\n\t_ = notify(input)"},
			messages: []string{"WaitFor of SendReminderStep holds lock ending, which RPC StopReminders also takes, and calls step.mailer.Send (and 1 more)"},
		},
		"recursive pure helpers": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "_ = countDown(3)",
				extraSource: "func countDown(n int) int {\n\tif n == 0 {\n\t\treturn 0\n\t}\n\treturn countUp(n - 1)\n}\n\nfunc countUp(n int) int { return countDown(n) }",
			},
		},
		"execute starts another Flow through the Dex client": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)",
				executeBody: "_, _ = reminderClient.StartFlow(ctx, &ReminderFlow{}, \"reminder-\"+input, input, dex.StartFlowOptions{})",
				extraSource: "var reminderClient *dex.Client",
			},
			messages: []string{"and calls reminderClient.StartFlow;"},
		},
		"bodyless Step method sharing an RPC lock": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", deliverLock: "dex.LockAttribute(ending)",
				deliverExecute: "func (step DeliverStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error)",
			},
			uncertainMessages: []string{"and calls Execute of DeliverStep, whose body dexcli cannot read. dexcli cannot determine whether this call performs I/O."},
		},
		"bodyless same-package helper": {
			source: goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "linkedHelper(input)",
				extraSource: "func linkedHelper(input string)",
			},
			uncertainMessages: []string{"and calls linkedHelper. dexcli cannot determine whether this call performs I/O."},
		},
		"standard library sleep": {
			source:   goLockedMethodCase{rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "time.Sleep(time.Second)"},
			messages: []string{"and calls time.Sleep;"},
		},
		"no RPC lock": {
			source: goLockedMethodCase{executeLock: "dex.LockAttribute(ending)", executeBody: "_ = step.mailer.Send(input)"},
		},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzeGoLockedMethodSource(t, testCase.source)
			requireLockedMethodDiagnostics(t, graph, testCase.messages, testCase.uncertainMessages)
			require.True(t, graph.Valid, "%+v", graph.Diagnostics)
		})
	}
}

func TestGoLockedMethodServerFacingSDKCalls(t *testing.T) {
	const declarations = `var (
	reminderWorker      *dex.Worker
	reminderCredential  dex.Credential
	reminderCodec       dex.PayloadCodec
	reminderKeyProvider dex.PayloadEncryptionKeyProvider
	reminderAESCodec    *dex.AESGCMPayloadCodec
)`
	testCases := map[string]struct{ body, callee string }{
		"Worker.Start":                            {"_ = reminderWorker.Start()", "reminderWorker.Start"},
		"NewClient":                               {"_, _ = dex.NewClient(nil, nil, dex.ClientOptions{})", "dex.NewClient"},
		"NewWorker":                               {"_, _ = dex.NewWorker(nil, nil, dex.WorkerOptions{})", "dex.NewWorker"},
		"FileCredential":                          {"_, _ = dex.FileCredential(input)", "dex.FileCredential"},
		"APIKeyCredential":                        {"_, _ = dex.APIKeyCredential(input, input, input)", "dex.APIKeyCredential"},
		"Credential.Token":                        {"_, _ = reminderCredential.Token(ctx)", "reminderCredential.Token"},
		"ReadFlowDefinitionFiles":                 {"_, _ = dex.ReadFlowDefinitionFiles([]string{input}, nil)", "dex.ReadFlowDefinitionFiles"},
		"NewFilePayloadEncryptionKeyProvider":     {"_, _ = dex.NewFilePayloadEncryptionKeyProvider(input)", "dex.NewFilePayloadEncryptionKeyProvider"},
		"PayloadCodec.Decode":                     {"_, _ = reminderCodec.Decode(ctx, dex.PayloadContext{}, nil)", "reminderCodec.Decode"},
		"PayloadEncryptionKeyProvider.CurrentKey": {"_, _ = reminderKeyProvider.CurrentKeyID(ctx, dex.PayloadContext{})", "reminderKeyProvider.CurrentKeyID"},
		"AESGCMPayloadCodec.Encode":               {"_, _ = reminderAESCodec.Encode(ctx, dex.PayloadContext{}, nil)", "reminderAESCodec.Encode"},
	}
	for _, name := range slices.Sorted(maps.Keys(testCases)) {
		testCase := testCases[name]
		t.Run(name, func(t *testing.T) {
			graph := analyzeGoLockedMethodSource(t, goLockedMethodCase{
				rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)",
				executeBody: testCase.body, extraSource: declarations,
			})
			var messages []string
			for _, diagnostic := range graph.Diagnostics {
				if diagnostic.Code == "locked_method_external_call" {
					messages = append(messages, diagnostic.Message)
				}
			}
			require.Len(t, messages, 1, "%+v", graph.Diagnostics)
			require.Contains(t, messages[0], "and calls "+testCase.callee+";")
		})
	}
}

func TestGoLockedMethodMarksOnlyMethodsSharingRPCLocks(t *testing.T) {
	graph := analyzeGoLockedMethodSource(t, goLockedMethodCase{
		rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", deliverLock: "dex.LockAttribute(counter)",
		executeBody: "_ = step.mailer.Send(input)", deliverBody: "_ = step.mailer.Send(input)",
	})
	var marked []string
	for _, marker := range graph.methodExternalCalls {
		marked = append(marked, marker.NodeID+" "+marker.Phase)
	}
	require.Equal(t, []string{"step:SendReminderStep execute"}, marked)
}

// A single-Flow graph leaves Step locks undrawn; the shared-lock intersection must still see them.
func TestGoLockedMethodSingleFlowUsesUndrawnStepLocks(t *testing.T) {
	graph := analyzeGoLockedMethodSource(t, goLockedMethodCase{
		rpcLock: "dex.LockAttribute(ending)", executeLock: "dex.LockAttribute(ending)", executeBody: "_ = step.mailer.Send(input)",
	})
	requireStepLocksOnlyUndrawn(t, graph)
	messages := lockedMethodWarningMessages(graph)
	require.Len(t, messages, 1, "%+v", graph.Diagnostics)
	require.Contains(t, messages[0], "Execute of SendReminderStep holds lock ending")
}

func requireStepLocksOnlyUndrawn(t *testing.T, graph *Graph) {
	t.Helper()
	isStepLock := func(edge Edge) bool {
		phase, _ := edge.Metadata["phase"].(string)
		return edge.Kind == "resource_lock" && phase != "rpc"
	}
	require.False(t, slices.ContainsFunc(graph.Edges, isStepLock), "%+v", graph.Edges)
	require.True(t, slices.ContainsFunc(graph.undrawnLockEdges, isStepLock), "%+v", graph.undrawnLockEdges)
}

func TestDiagnosticsSortErrorWarningInfo(t *testing.T) {
	graph := NewGraph("go", "workflow.go")
	graph.AddDiagnostic("info", "locked_method_uncertain_call", "uncertain", nil)
	graph.AddDiagnostic("warning", "locked_method_external_call", "definite", nil)
	graph.AddDiagnostic("error", "flow_not_found", "missing", nil)
	graph.Normalize()
	var severities []string
	for _, diagnostic := range graph.Diagnostics {
		severities = append(severities, diagnostic.Severity)
	}
	require.Equal(t, []string{"error", "warning", "info"}, severities)
}

// requireLockedMethodDiagnostics checks the warning and info fragments of the locked-method check, each in report order.
func requireLockedMethodDiagnostics(t *testing.T, graph *Graph, warnings []string, uncertain []string) {
	t.Helper()
	var warningMessages, uncertainMessages []string
	for _, diagnostic := range graph.Diagnostics {
		switch diagnostic.Code {
		case "locked_method_external_call":
			require.Equal(t, "warning", diagnostic.Severity)
			require.NotNil(t, diagnostic.Span)
			warningMessages = append(warningMessages, diagnostic.Message)
		case "locked_method_uncertain_call":
			require.Equal(t, "info", diagnostic.Severity)
			require.NotNil(t, diagnostic.Span)
			uncertainMessages = append(uncertainMessages, diagnostic.Message)
		}
	}
	require.Len(t, warningMessages, len(warnings), "%+v", graph.Diagnostics)
	for index, fragment := range warnings {
		require.Contains(t, warningMessages[index], fragment)
	}
	require.Len(t, uncertainMessages, len(uncertain), "%+v", graph.Diagnostics)
	for index, fragment := range uncertain {
		require.Contains(t, uncertainMessages[index], fragment)
	}
}

func lockedMethodWarningMessages(graph *Graph) []string {
	var messages []string
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code == "locked_method_external_call" {
			messages = append(messages, diagnostic.Message)
		}
	}
	return messages
}

func analyzeGoLockedMethodSource(t *testing.T, source goLockedMethodCase) *Graph {
	t.Helper()
	deliverExecute := source.deliverExecute
	if deliverExecute == "" {
		deliverExecute = goDefaultDeliverExecute
	}
	contents := strings.Replace(goLockedMethodSource, "DELIVER_EXECUTE", deliverExecute, 1)
	contents = strings.NewReplacer(source.sourceReplacements...).Replace(contents)
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
	directory := t.TempDir()
	sdkDirectory, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk-go"))
	require.NoError(t, err)
	module := "module reminders\n\ngo 1.26.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.ToSlash(sdkDirectory) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0o644))
	for relativePath, packageSource := range source.packageFiles {
		packagePath := filepath.Join(directory, relativePath)
		require.NoError(t, os.MkdirAll(filepath.Dir(packagePath), 0o755))
		require.NoError(t, os.WriteFile(packagePath, []byte(packageSource), 0o644))
	}
	sourcePath := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(contents)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	return graph
}
