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

package workflows

import (
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	"github.com/superdurable/dex/examples/go/workflows/jobpost"
	"github.com/superdurable/dex/examples/go/workflows/microservices"
	"github.com/superdurable/dex/examples/go/workflows/moneytransfer"
	"github.com/superdurable/dex/examples/go/workflows/patterns/cron"
	draininternal "github.com/superdurable/dex/examples/go/workflows/patterns/drainchannels/draininternal"
	drainsignal "github.com/superdurable/dex/examples/go/workflows/patterns/drainchannels/signal"
	"github.com/superdurable/dex/examples/go/workflows/patterns/interruptible"
	"github.com/superdurable/dex/examples/go/workflows/patterns/intervention"
	"github.com/superdurable/dex/examples/go/workflows/patterns/parallel"
	"github.com/superdurable/dex/examples/go/workflows/patterns/parentchild"
	patternspolling "github.com/superdurable/dex/examples/go/workflows/patterns/polling"
	"github.com/superdurable/dex/examples/go/workflows/patterns/recovery"
	"github.com/superdurable/dex/examples/go/workflows/patterns/reminders"
	"github.com/superdurable/dex/examples/go/workflows/patterns/resettabletimer"
	"github.com/superdurable/dex/examples/go/workflows/patterns/scalableparallel"
	patternsservice "github.com/superdurable/dex/examples/go/workflows/patterns/service"
	"github.com/superdurable/dex/examples/go/workflows/patterns/storage"
	"github.com/superdurable/dex/examples/go/workflows/patterns/timeout"
	"github.com/superdurable/dex/examples/go/workflows/patterns/waitforstatecompletion"
	"github.com/superdurable/dex/examples/go/workflows/polling"
	"github.com/superdurable/dex/examples/go/workflows/service"
	"github.com/superdurable/dex/examples/go/workflows/shortlistcandidates"
	"github.com/superdurable/dex/examples/go/workflows/signup"
	"github.com/superdurable/dex/examples/go/workflows/subscription"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ClientProvider returns the shared Client after bootstrap completes.
type ClientProvider func() *dex.Client

var (
	applicationService = service.NewMyService()
	patternService     = patternsservice.NewServiceDependency()

	Engagement    *engagement.EngagementFlow
	Microservices *microservices.OrchestrationFlow
	MoneyTransfer *moneytransfer.MoneyTransferFlow
	Polling       *polling.PollingFlow
	Subscription  *subscription.SubscriptionFlow
	Signup        *signup.UserSignupFlow
	JobPost       *jobpost.JobPostFlow
	EmployerOptIn *shortlistcandidates.EmployerOptInFlow
	Shortlist     *shortlistcandidates.ShortlistFlow

	CronSchedule           *cron.CronScheduleFlow
	SimplePolling          *patternspolling.SimplePollingFlow
	BackoffPolling         *patternspolling.BackoffPollingFlow
	InterruptibleExecution *interruptible.InterruptibleExecutionFlow
	Reminder               *reminders.ReminderFlow
	Storage                *storage.StorageFlow
	ManualIntervention     *intervention.ManualInterventionFlow
	ResettableTimer        *resettabletimer.ResettableTimerFlow
	SimpleParallel         *parallel.SimpleParallelStatesFlow
	ParallelWithAwait      *parallel.ParallelStatesWithAwaitFlow
	FailureRecovery        *recovery.FailureRecoveryFlow
	RequestReceiver        *scalableparallel.RequestReceiverFlow
	ScalableParent         *scalableparallel.ParentFlow
	ScalableChild          *scalableparallel.ChildFlow
	ParentChild            *parentchild.ParentFlowV2
	ParentChildChild       *parentchild.ChildFlow
	DrainInternal          *draininternal.DrainInternalChannelsFlow
	DrainSignal            *drainsignal.DrainSignalChannelsFlow
	WaitForStateCompletion *waitforstatecompletion.WaitForStateCompletionFlow
	GracefulTimeout        *timeout.FlowGracefulTimeout
)

// New constructs every sample flow. getClient may return nil until the Client
// is created; flows that need it call the provider at Execute/RPC time.
func New(applicationSvc service.MyService, getClient ClientProvider) []dex.Flow {
	if applicationSvc == nil {
		panic("workflows: application service is required")
	}
	if getClient == nil {
		panic("workflows: client provider is required")
	}
	applicationService = applicationSvc

	Engagement = engagement.NewEngagementFlow(applicationService)
	Microservices = microservices.NewOrchestrationFlow(applicationService)
	MoneyTransfer = moneytransfer.NewMoneyTransferFlow(applicationService)
	Polling = polling.NewPollingFlow(applicationService)
	Subscription = subscription.NewSubscriptionFlow(applicationService)
	Signup = signup.NewUserSignupFlow(applicationService)
	JobPost = jobpost.NewJobPostFlow(applicationService)
	EmployerOptIn = shortlistcandidates.NewEmployerOptInFlow()
	Shortlist = shortlistcandidates.NewShortlistFlow(
		applicationService,
		getClient,
		EmployerOptIn,
	)

	CronSchedule = cron.NewCronScheduleFlow()
	SimplePolling = patternspolling.NewSimplePollingFlow()
	BackoffPolling = patternspolling.NewBackoffPollingFlow(patternService)
	InterruptibleExecution = interruptible.NewInterruptibleExecutionFlow()
	Reminder = reminders.NewReminderFlow(patternService)
	Storage = storage.NewStorageFlow()
	ManualIntervention = intervention.NewManualInterventionFlow(patternService)
	ResettableTimer = resettabletimer.NewResettableTimerFlow()
	SimpleParallel = parallel.NewSimpleParallelStatesFlow(patternService)
	ParallelWithAwait = parallel.NewParallelStatesWithAwaitFlow(patternService)
	FailureRecovery = recovery.NewFailureRecoveryFlow()
	ScalableChild = scalableparallel.NewChildFlow(getClient, func() *scalableparallel.ParentFlow {
		return ScalableParent
	})
	ScalableParent = scalableparallel.NewParentFlow(getClient, ScalableChild)
	RequestReceiver = scalableparallel.NewRequestReceiverFlow(getClient, ScalableParent)
	ParentChildChild = parentchild.NewChildFlow()
	ParentChild = parentchild.NewParentFlowV2(getClient, ParentChildChild)
	DrainInternal = draininternal.NewDrainInternalChannelsFlow(patternService)
	DrainSignal = drainsignal.NewDrainSignalChannelsFlow()
	WaitForStateCompletion = waitforstatecompletion.NewWaitForStateCompletionFlow(patternService)
	GracefulTimeout = timeout.NewFlowGracefulTimeout()

	return Flows()
}

func Flows(additional ...dex.Flow) []dex.Flow {
	flows := []dex.Flow{
		Engagement,
		Microservices,
		MoneyTransfer,
		Polling,
		Subscription,
		Signup,
		JobPost,
		EmployerOptIn,
		Shortlist,
		CronSchedule,
		SimplePolling,
		BackoffPolling,
		InterruptibleExecution,
		Reminder,
		Storage,
		ManualIntervention,
		ResettableTimer,
		SimpleParallel,
		ParallelWithAwait,
		FailureRecovery,
		RequestReceiver,
		ScalableParent,
		ScalableChild,
		ParentChild,
		ParentChildChild,
		DrainInternal,
		DrainSignal,
		WaitForStateCompletion,
		GracefulTimeout,
	}
	return append(flows, additional...)
}
