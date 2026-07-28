// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package replayTests

import (
	"testing"

	iwfconverter "github.com/superdurable/iwf/service/common/converter"
	"github.com/superdurable/iwf/service/interpreter/temporal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/worker"
)

// gRPC-era Temporal histories recaptured after the interpreter rewrite.
// Global versioning restarted at v1; do not keep pre-rewrite version baselines.
var jsonHistoryFiles = []string{
	"v1-persistence.json",
	"v1-persistence-continue-as-new-CAN1.json",
	"v1-persistence-continue-as-new-CAN2.json",
	"v1-persistence-continue-as-new-wf-finish.json",
	"v1-basic.json",
	"v1-basic-disable-system-searchattributes.json",
	"v1-basic-continue-as-new-CAN1.json",
	"v1-basic-continue-as-new-wf-finish.json",
	"v1-any-timer-signal.json",
	"v1-any-timer-signal-continue-as-new-CAN1.json",
	"v1-any-timer-signal-continue-as-new-CAN2.json",
	"v1-any-timer-signal-continue-as-new-wf-finish.json",
	"v1-skip-start.json",
	"v1-bug-no-state-stuck.json",
	"v1-continue-as-new-on-no-state.json",
	"v1-yield-on-conditional-complete.json",
	"v1-yield-on-conditional-complete-continue-as-new-CAN1.json",
	"v1-yield-on-conditional-complete-continue-as-new-CAN2.json",
	"v1-yield-on-conditional-complete-continue-as-new-wf-finish.json",
	"v1-activity-for-sync-updates-rpcs.json",
	"v1-activity-for-sync-updates-rpcs-continue-as-new-CAN1.json",
	"v1-activity-for-sync-updates-rpcs-continue-as-new-wf-finish.json",
	"v1-locking-sync-update.json",
	"v1-command-thread-completion.json",
	"v1-command-thread-completion-CAN1.json",
	"v1-command-thread-completion-CAN2.json",
	"v1-command-thread-completion-CAN3.json",
	"v1-command-thread-completion-wf-finish.json",
	"v1-any-command-thread-completion-CAN1.json",
	"v1-any-command-thread-completion-wf-finish.json",
	"v1-wait-for-step-completion.json",
	"v1-wait-for-step-completion-by-step-type.json",
	"v1-wait-for-attribute.json",
	"v1-wait-for-attribute-timeout.json",
	"v1-signal.json",
	"v1-signal-continue-as-new-CAN1.json",
	"v1-signal-continue-as-new-CAN2.json",
	"v1-signal-continue-as-new-wf-finish.json",
}

func TestTemporalReplay(t *testing.T) {
	worker.EnableVerboseLogging(true)

	replayer, err := worker.NewWorkflowReplayerWithOptions(
		worker.WorkflowReplayerOptions{
			EnableLoggingInReplay: true,
			DataConverter:         iwfconverter.NewTemporalDataConverter(),
		})
	require.NoError(t, err)

	replayer.RegisterWorkflow(temporal.NewWorkerForReplay().Engine)

	for _, f := range jsonHistoryFiles {
		err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, "history/"+f)
		assert.Nil(t, err, "fail at replay history for: "+f)
	}
}
