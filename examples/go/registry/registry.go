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

package registry

import (
	"github.com/superdurable/dex/examples/go/patterns/cron"
	drainexternal "github.com/superdurable/dex/examples/go/patterns/drain-channels/external-publishing"
	draininternal "github.com/superdurable/dex/examples/go/patterns/drain-channels/internal-drain"
	"github.com/superdurable/dex/examples/go/patterns/entity-store"
	"github.com/superdurable/dex/examples/go/patterns/interruptible"
	"github.com/superdurable/dex/examples/go/patterns/intervention"
	"github.com/superdurable/dex/examples/go/patterns/parallel"
	"github.com/superdurable/dex/examples/go/patterns/parent-child"
	patternspolling "github.com/superdurable/dex/examples/go/patterns/polling"
	"github.com/superdurable/dex/examples/go/patterns/recovery"
	"github.com/superdurable/dex/examples/go/patterns/reminders"
	"github.com/superdurable/dex/examples/go/patterns/resettable-timer"
	"github.com/superdurable/dex/examples/go/patterns/scalable-parallel"
	patternsservice "github.com/superdurable/dex/examples/go/patterns/shared/service"
	"github.com/superdurable/dex/examples/go/patterns/timeout"
	"github.com/superdurable/dex/examples/go/patterns/wait-for-state-completion"
	"github.com/superdurable/dex/examples/go/primitives/attribute"
	"github.com/superdurable/dex/examples/go/primitives/channel"
	"github.com/superdurable/dex/examples/go/primitives/client-apis"
	"github.com/superdurable/dex/examples/go/primitives/custom-retry"
	"github.com/superdurable/dex/examples/go/primitives/durability"
	"github.com/superdurable/dex/examples/go/primitives/flow"
	"github.com/superdurable/dex/examples/go/primitives/heartbeat"
	"github.com/superdurable/dex/examples/go/primitives/options-override"
	"github.com/superdurable/dex/examples/go/primitives/proceed-on-wait-failure"
	"github.com/superdurable/dex/examples/go/primitives/rpc"
	"github.com/superdurable/dex/examples/go/primitives/step"
	"github.com/superdurable/dex/examples/go/primitives/step-decision"
	"github.com/superdurable/dex/examples/go/primitives/step-execution-local"
	"github.com/superdurable/dex/examples/go/primitives/stream"
	"github.com/superdurable/dex/examples/go/primitives/subflow"
	"github.com/superdurable/dex/examples/go/primitives/timer"
	"github.com/superdurable/dex/examples/go/primitives/wait-types"
	"github.com/superdurable/dex/examples/go/products/engagement"
	"github.com/superdurable/dex/examples/go/products/job-post"
	"github.com/superdurable/dex/examples/go/products/microservices"
	"github.com/superdurable/dex/examples/go/products/money-transfer"
	"github.com/superdurable/dex/examples/go/products/order-processing"
	"github.com/superdurable/dex/examples/go/products/polling"
	"github.com/superdurable/dex/examples/go/products/shortlist-candidates"
	"github.com/superdurable/dex/examples/go/products/signup"
	"github.com/superdurable/dex/examples/go/products/subscription"
	"github.com/superdurable/dex/examples/go/shared/service"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ClientProvider returns the shared Client after bootstrap completes.
type ClientProvider func() *dex.Client

var (
	applicationService = service.NewMyService()
	patternService     = patternsservice.NewServiceDependency()

	Engagement      *engagement.EngagementFlow
	Microservices   *microservices.OrchestrationFlow
	MoneyTransfer   *moneytransfer.MoneyTransferFlow
	OrderProcessing *orderprocessing.OrderProcessingFlow
	Polling         *polling.PollingFlow
	Subscription    *subscription.SubscriptionFlow
	Signup          *signup.UserSignupFlow
	JobPost         *jobpost.JobPostFlow
	EmployerOptIn   *shortlistcandidates.EmployerOptInFlow
	Shortlist       *shortlistcandidates.ShortlistFlow

	CronSchedule           *cron.CronScheduleFlow
	PollingWithTimer       *patternspolling.PollingWithTimerFlow
	BackoffPolling         *patternspolling.BackoffPollingFlow
	Iteration              *patternspolling.IterationFlow
	Interruptible          *interruptible.InterruptibleFlow
	Reminder               *reminders.ReminderFlow
	UserProfile            *entitystore.UserProfileFlow
	ManualRecovery         *intervention.ManualRecoveryFlow
	ResettableTimer        *resettabletimer.ResettableTimerFlow
	StaticParallel         *parallel.StaticParallelStepsFlow
	DynamicParallel        *parallel.DynamicParallelStepsFlow
	AwaitParallel          *parallel.AwaitParallelStepsFlow
	FirstWinParallel       *parallel.FirstWinParallelStepsFlow
	FailureRecovery        *recovery.FailureRecoveryFlow
	RequestReceiver        *scalableparallel.RequestReceiverFlow
	ScalableParent         *scalableparallel.ParentFlow
	ScalableChild          *scalableparallel.ChildFlow
	ParentChild            *parentchild.ParentFlowV2
	ParentChildChild       *parentchild.ChildFlow
	DrainInternal          *draininternal.DrainInternalChannelFlow
	DrainExternal          *drainexternal.DrainingExternalChannelFlow
	WaitForStateCompletion *waitforstatecompletion.WaitForStateCompletionFlow
	GracefulTimeout        *timeout.FlowGracefulTimeout

	Step                 *step.StepFlow
	ExampleFlow          *flow.ExampleFlow
	StepRetry            *step.RetryFlow
	CustomRetry          *customretry.CustomRetryFlow
	Durability           *durability.DurabilityFlow
	Heartbeat            *heartbeat.HeartbeatFlow
	OptionsOverride      *optionsoverride.OptionsOverrideFlow
	ProceedOnWaitFailure *proceedonwaitfailure.ProceedOnWaitFailureFlow
	StepExecutionLocal   *stepexecutionlocal.StepExecutionLocalFlow
	StepDecision         *stepdecision.StepDecisionFlow
	WaitTypes            *waittypes.WaitTypesFlow
	Attribute            *attribute.AttributeFlow
	Channel              *channel.ChannelFlow
	Stream               *stream.StreamFlow
	Timer                *timer.TimerFlow
	Rpc                  *rpc.RpcFlow
	SubFlowChild         *subflow.SubFlowChildFlow
	SubFlowParent        *subflow.SubFlowParentFlow
	ClientApis           *clientapis.ClientApisFlow
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
	OrderProcessing = orderprocessing.NewOrderProcessingFlow(applicationService)
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
	PollingWithTimer = patternspolling.NewPollingWithTimerFlow()
	BackoffPolling = patternspolling.NewBackoffPollingFlow(patternService)
	Iteration = patternspolling.NewIterationFlow()
	Interruptible = interruptible.NewInterruptibleFlow()
	Reminder = reminders.NewReminderFlow(patternService)
	UserProfile = entitystore.NewUserProfileFlow()
	ManualRecovery = intervention.NewManualRecoveryFlow()
	ResettableTimer = resettabletimer.NewResettableTimerFlow()
	StaticParallel = parallel.NewStaticParallelStepsFlow()
	DynamicParallel = parallel.NewDynamicParallelStepsFlow()
	AwaitParallel = parallel.NewAwaitParallelStepsFlow()
	FirstWinParallel = parallel.NewFirstWinParallelStepsFlow()
	FailureRecovery = recovery.NewFailureRecoveryFlow()
	ScalableChild = scalableparallel.NewChildFlow(getClient, func() *scalableparallel.ParentFlow {
		return ScalableParent
	})
	ScalableParent = scalableparallel.NewParentFlow(getClient, ScalableChild)
	RequestReceiver = scalableparallel.NewRequestReceiverFlow(getClient, ScalableParent)
	ParentChildChild = parentchild.NewChildFlow()
	ParentChild = parentchild.NewParentFlowV2(getClient, ParentChildChild)
	DrainInternal = draininternal.NewDrainInternalChannelFlow(patternService)
	DrainExternal = drainexternal.NewDrainingExternalChannelFlow()
	WaitForStateCompletion = waitforstatecompletion.NewWaitForStateCompletionFlow(patternService)
	GracefulTimeout = timeout.NewFlowGracefulTimeout()

	Step = step.NewStepFlow()
	ExampleFlow = flow.NewExampleFlow()
	StepRetry = step.NewRetryFlow()
	CustomRetry = customretry.NewCustomRetryFlow()
	Durability = durability.NewDurabilityFlow()
	Heartbeat = heartbeat.NewHeartbeatFlow()
	OptionsOverride = optionsoverride.NewOptionsOverrideFlow()
	ProceedOnWaitFailure = proceedonwaitfailure.NewProceedOnWaitFailureFlow()
	StepExecutionLocal = stepexecutionlocal.NewStepExecutionLocalFlow()
	StepDecision = stepdecision.NewStepDecisionFlow()
	WaitTypes = waittypes.NewWaitTypesFlow()
	Attribute = attribute.NewAttributeFlow()
	Channel = channel.NewChannelFlow()
	Stream = stream.NewStreamFlow()
	Timer = timer.NewTimerFlow()
	Rpc = rpc.NewRpcFlow()
	SubFlowChild = subflow.NewSubFlowChildFlow()
	SubFlowParent = subflow.NewSubFlowParentFlow(SubFlowChild)
	ClientApis = clientapis.NewClientApisFlow()

	return Flows()
}

func Flows(additional ...dex.Flow) []dex.Flow {
	flows := []dex.Flow{
		Engagement,
		Microservices,
		MoneyTransfer,
		OrderProcessing,
		Polling,
		Subscription,
		Signup,
		JobPost,
		EmployerOptIn,
		Shortlist,
		CronSchedule,
		PollingWithTimer,
		BackoffPolling,
		Iteration,
		Interruptible,
		Reminder,
		UserProfile,
		ManualRecovery,
		ResettableTimer,
		StaticParallel,
		DynamicParallel,
		AwaitParallel,
		FirstWinParallel,
		FailureRecovery,
		RequestReceiver,
		ScalableParent,
		ScalableChild,
		ParentChild,
		ParentChildChild,
		DrainInternal,
		DrainExternal,
		WaitForStateCompletion,
		GracefulTimeout,
		Step,
		ExampleFlow,
		StepRetry,
		CustomRetry,
		Durability,
		Heartbeat,
		OptionsOverride,
		ProceedOnWaitFailure,
		StepExecutionLocal,
		StepDecision,
		WaitTypes,
		Attribute,
		Channel,
		Stream,
		Timer,
		Rpc,
		SubFlowChild,
		SubFlowParent,
		ClientApis,
	}
	return append(flows, additional...)
}
