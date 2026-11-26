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

func (step DeliverStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	DELIVER_BODY
	return dex.GracefulComplete(input), nil
}

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
	extraSource string
}

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
	}{
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
		})
	}
}

func analyzeGoLockedMethodSource(t *testing.T, source goLockedMethodCase) *Graph {
	t.Helper()
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
	sourcePath := filepath.Join(directory, "workflow.go")
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacer.Replace(goLockedMethodSource)), 0o644))
	graph, err := Analyze(t.Context(), sourcePath, AnalyzeOptions{FlowType: "ReminderFlow"})
	require.NoError(t, err)
	return graph
}
