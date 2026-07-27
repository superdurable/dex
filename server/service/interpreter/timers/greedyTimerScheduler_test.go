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

package timers

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
)

type fakeWorkflowProvider struct {
	nowUnixSeconds int64
	awaitErr       error
}

func (p *fakeWorkflowProvider) NewWorkflowError(iwfpb.FlowErrorType, interface{}) error {
	return nil
}

func (p *fakeWorkflowProvider) NewUpdateError(iwfpb.UpdateErrorType, interface{}) error {
	return nil
}

func (p *fakeWorkflowProvider) IsApplicationError(error) bool {
	return false
}

func (p *fakeWorkflowProvider) GetWorkflowInfo(interfaces.UnifiedContext) interfaces.WorkflowInfo {
	return interfaces.WorkflowInfo{}
}

func (p *fakeWorkflowProvider) GetSearchAttributeKeywordArray(
	interfaces.UnifiedContext,
	string,
) ([]string, error) {
	return nil, nil
}

func (p *fakeWorkflowProvider) UpsertSearchAttributes(
	interfaces.UnifiedContext,
	map[string]interface{},
) error {
	return nil
}

func (p *fakeWorkflowProvider) SetQueryHandler(
	interfaces.UnifiedContext,
	string,
	interface{},
) error {
	return nil
}

func (p *fakeWorkflowProvider) SetInvokeRPCUpdateHandler(
	interfaces.UnifiedContext,
	interfaces.InvokeRPCUpdateValidator,
	interfaces.InvokeRPCUpdateHandler,
) error {
	return nil
}

func (p *fakeWorkflowProvider) SetWaitForStepCompletionUpdateHandler(
	interfaces.UnifiedContext,
	interfaces.WaitForStepCompletionUpdateValidator,
	interfaces.WaitForStepCompletionUpdateHandler,
) error {
	return nil
}

func (p *fakeWorkflowProvider) SetWaitForAttributeUpdateHandler(
	interfaces.UnifiedContext,
	interfaces.WaitForAttributeUpdateValidator,
	interfaces.WaitForAttributeUpdateHandler,
) error {
	return nil
}

func (p *fakeWorkflowProvider) ExtendContextWithValue(
	ctx interfaces.UnifiedContext,
	_ string,
	_ interface{},
) interfaces.UnifiedContext {
	return ctx
}

func (p *fakeWorkflowProvider) GoNamed(
	_ interfaces.UnifiedContext,
	_ string,
	run func(interfaces.UnifiedContext),
) {
	run(nil)
}

func (p *fakeWorkflowProvider) GetThreadCount() int {
	return 0
}

func (p *fakeWorkflowProvider) GetPendingThreadNames() map[string]int {
	return nil
}

func (p *fakeWorkflowProvider) Await(
	_ interfaces.UnifiedContext,
	_ func() bool,
) error {
	return p.awaitErr
}

func (p *fakeWorkflowProvider) WithActivityOptions(
	ctx interfaces.UnifiedContext,
	_ interfaces.ActivityOptions,
) interfaces.UnifiedContext {
	return ctx
}

func (p *fakeWorkflowProvider) ExecuteActivity(
	interface{},
	iwfpb.StepDurability,
	interfaces.UnifiedContext,
	interface{},
	...interface{},
) error {
	return nil
}

func (p *fakeWorkflowProvider) ExecuteLocalActivity(
	interface{},
	interfaces.UnifiedContext,
	interface{},
	...interface{},
) error {
	return nil
}

func (p *fakeWorkflowProvider) Now(interfaces.UnifiedContext) time.Time {
	return time.Unix(p.nowUnixSeconds, 0)
}

func (p *fakeWorkflowProvider) IsReplaying(interfaces.UnifiedContext) bool {
	return false
}

func (p *fakeWorkflowProvider) Sleep(interfaces.UnifiedContext, time.Duration) error {
	return nil
}

func (p *fakeWorkflowProvider) NewTimer(
	interfaces.UnifiedContext,
	time.Duration,
) interfaces.Future {
	return nil
}

func (p *fakeWorkflowProvider) GetSignalChannel(
	interfaces.UnifiedContext,
	string,
) interfaces.ReceiveChannel {
	return nil
}

func (p *fakeWorkflowProvider) GetContextValue(
	interfaces.UnifiedContext,
	string,
) interface{} {
	return nil
}

func (p *fakeWorkflowProvider) GetVersion(
	interfaces.UnifiedContext,
	string,
	int,
	int,
) int {
	return 0
}

func (p *fakeWorkflowProvider) GetLogger(
	interfaces.UnifiedContext,
) interfaces.UnifiedLogger {
	return fakeLogger{}
}

func (p *fakeWorkflowProvider) NewInterpreterContinueAsNewError(
	interfaces.UnifiedContext,
	*iwfpb.InterpreterWorkflowInput,
) error {
	return nil
}

type fakeLogger struct{}

func (fakeLogger) Debug(string, ...interface{}) {}
func (fakeLogger) Info(string, ...interface{})  {}
func (fakeLogger) Warn(string, ...interface{})  {}
func (fakeLogger) Error(string, ...interface{}) {}

func TestPruneToNextTimer_PrunesCorrectly_WithTwoScheduled(t *testing.T) {
	timerScheduler := &timerScheduler{
		pendingScheduling: []*iwfpb.TimerInfo{
			{FiringUnixTimestampSeconds: 1751395615, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
			{FiringUnixTimestampSeconds: 1751395955, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
			{FiringUnixTimestampSeconds: 1751395755, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
			{FiringUnixTimestampSeconds: 1751395555, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
		},
		providerScheduledTimerUnixTs: []int64{1751395955, 1751395555},
	}

	pruned := timerScheduler.pruneToNextTimer(1751395755)
	assert.NotNil(t, pruned)
	assert.Equal(t, int64(1751395955), pruned.GetFiringUnixTimestampSeconds())
	assert.Equal(t, []int64{1751395955}, timerScheduler.providerScheduledTimerUnixTs)
	if assert.Equal(t, 2, len(timerScheduler.pendingScheduling)) {
		assert.Equal(t, int64(1751395615), timerScheduler.pendingScheduling[0].GetFiringUnixTimestampSeconds())
		assert.Equal(t, int64(1751395955), timerScheduler.pendingScheduling[1].GetFiringUnixTimestampSeconds())
	}
}

func TestPruneToNextTimer_PrunesCorrectly_WithOneScheduled(t *testing.T) {
	timerScheduler := &timerScheduler{
		pendingScheduling: []*iwfpb.TimerInfo{
			{FiringUnixTimestampSeconds: 1751395615, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
			{FiringUnixTimestampSeconds: 1751395955, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
			{FiringUnixTimestampSeconds: 1751395755, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
			{FiringUnixTimestampSeconds: 1751395555, Status: iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING},
		},
		providerScheduledTimerUnixTs: []int64{1751395555},
	}

	pruned := timerScheduler.pruneToNextTimer(1751395755)
	assert.NotNil(t, pruned)
	assert.Equal(t, int64(1751395955), pruned.GetFiringUnixTimestampSeconds())
	assert.Equal(t, []int64(nil), timerScheduler.providerScheduledTimerUnixTs)
	if assert.Equal(t, 2, len(timerScheduler.pendingScheduling)) {
		assert.Equal(t, int64(1751395615), timerScheduler.pendingScheduling[0].GetFiringUnixTimestampSeconds())
		assert.Equal(t, int64(1751395955), timerScheduler.pendingScheduling[1].GetFiringUnixTimestampSeconds())
	}
}

func TestStartGreedyTimerScheduler_AwaitErrorBreaksLoop(t *testing.T) {
	provider := &fakeWorkflowProvider{
		awaitErr:       errors.New("test error"),
		nowUnixSeconds: 100,
	}
	_ = startGreedyTimerScheduler(nil, provider, nil)
}
