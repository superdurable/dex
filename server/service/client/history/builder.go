// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package history

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const rpcSourcePrefix = "__rpc/"

type Builder struct {
	flowID              string
	runID               string
	events              []*dexpb.FlowHistoryEvent
	scheduledActivities map[int64]*scheduledActivity
	startEvent          *dexpb.FlowHistoryEvent
	continueDumpPages   map[int32][]byte
	continueDumpTotal   int32
	transientSteps      map[string]bool
}

type scheduledActivity struct {
	scheduledTime    time.Time
	durability       dexpb.StepDurability
	methodOptions    *dexpb.StepMethodOptions
	waitInput        *dexpb.InvokeWaitForMethodActivityInput
	executeInput     *dexpb.InvokeExecuteMethodActivityInput
	previousFailures []*dexpb.StepMethodAttemptFailure
	priorAttempts    int32
	firstStartedTime time.Time
	finalAttempt     int32
}

func NewBuilder(flowID string, runID string) *Builder {
	return &Builder{
		flowID:              flowID,
		runID:               runID,
		scheduledActivities: map[int64]*scheduledActivity{},
		continueDumpPages:   map[int32][]byte{},
		transientSteps:      map[string]bool{},
	}
}

func (b *Builder) RecordStart(
	eventID int64,
	eventTime time.Time,
	input *dexpb.InterpreterWorkflowInput,
) {
	payload := &dexpb.FlowStartedOrContinuedHistoryEvent{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: b.flowID, RunId: b.runID},
		FlowType:        input.GetFlowType(),
		FlowConfig:      input.GetConfig(),
	}
	if input.GetIsResumeFromContinueAsNew() {
		payload.StartOrContinue = &dexpb.FlowStartedOrContinuedHistoryEvent_ContinuedStart{
			ContinuedStart: &dexpb.FlowContinuedStart{
				PreviousRunId: input.GetContinueAsNewInput().GetPreviousInternalRunId(),
			},
		}
	} else {
		payload.StartOrContinue = &dexpb.FlowStartedOrContinuedHistoryEvent_InitialStart{
			InitialStart: &dexpb.FlowInitialStart{
				StartStepType:     input.GetStartStepType(),
				StepInput:         input.GetStepInput(),
				StepOptions:       input.GetStepOptions(),
				InitialAttributes: input.GetInitAttributes(),
			},
		}
	}
	b.startEvent = newEvent(
		eventID,
		eventTime,
		&dexpb.FlowHistoryEvent_FlowStartedOrContinued{FlowStartedOrContinued: payload},
	)
}

func (b *Builder) RecordWaitScheduled(
	eventID int64,
	eventTime time.Time,
	input *dexpb.InvokeWaitForMethodActivityInput,
	durability dexpb.StepDurability,
	methodOptions *dexpb.StepMethodOptions,
	previousFailures []*dexpb.StepMethodAttemptFailure,
) {
	b.scheduledActivities[eventID] = &scheduledActivity{
		scheduledTime:    eventTime,
		durability:       durability,
		methodOptions:    methodOptions,
		waitInput:        input,
		previousFailures: previousFailures,
		priorAttempts:    previousAttemptCount(previousFailures),
	}
}

func (b *Builder) RecordExecuteScheduled(
	eventID int64,
	eventTime time.Time,
	input *dexpb.InvokeExecuteMethodActivityInput,
	durability dexpb.StepDurability,
	methodOptions *dexpb.StepMethodOptions,
	previousFailures []*dexpb.StepMethodAttemptFailure,
) {
	b.scheduledActivities[eventID] = &scheduledActivity{
		scheduledTime:    eventTime,
		durability:       durability,
		methodOptions:    methodOptions,
		executeInput:     input,
		previousFailures: previousFailures,
		priorAttempts:    previousAttemptCount(previousFailures),
	}
}

func (b *Builder) RecordActivityStarted(
	eventTime time.Time,
	scheduledEventID int64,
	attempt int32,
	lastFailure *dexpb.StepMethodFailure,
) {
	scheduled := b.scheduledActivities[scheduledEventID]
	if scheduled == nil {
		return
	}
	if scheduled.firstStartedTime.IsZero() {
		scheduled.firstStartedTime = eventTime
	}
	if attempt > scheduled.finalAttempt {
		scheduled.finalAttempt = attempt
	}
	if attempt > 1 && lastFailure != nil {
		scheduled.previousFailures = append(
			scheduled.previousFailures,
			&dexpb.StepMethodAttemptFailure{
				Attempt: scheduled.priorAttempts + attempt - 1,
				Failure: lastFailure,
			},
		)
	}
}

func (b *Builder) RecordActivityCompleted(
	eventID int64,
	eventTime time.Time,
	scheduledEventID int64,
	waitOutput *dexpb.InvokeWaitForMethodActivityOutput,
	executeOutput *dexpb.InvokeExecuteMethodActivityOutput,
) error {
	scheduled := b.scheduledActivities[scheduledEventID]
	if scheduled == nil {
		return fmt.Errorf("scheduled activity %d is missing", scheduledEventID)
	}
	startedTime, finalAttempt := scheduled.executionTiming()
	switch {
	case scheduled.waitInput != nil && waitOutput != nil:
		b.recordTransientStep(waitOutput.GetResponse().GetTransientStepMovement())
		b.events = append(b.events, newEvent(
			eventID,
			eventTime,
			&dexpb.FlowHistoryEvent_StepWaitForCompleted{
				StepWaitForCompleted: &dexpb.StepWaitForCompletedEvent{
					Execution: executionInfo(
						scheduled.waitInput.GetRequest().GetContext(),
						scheduled.waitInput.GetRequest().GetStepType(),
						scheduled.durability,
						false,
						startedTime,
						eventTime,
						finalAttempt,
						scheduled.previousFailures,
						scheduled.methodOptions,
					),
					Request:  scheduled.waitInput.GetRequest(),
					Response: waitOutput.GetResponse(),
				},
			},
		))
	case scheduled.executeInput != nil && executeOutput != nil:
		b.events = append(b.events, newEvent(
			eventID,
			eventTime,
			&dexpb.FlowHistoryEvent_StepExecuteCompleted{
				StepExecuteCompleted: &dexpb.StepExecuteCompletedEvent{
					Execution: executionInfo(
						scheduled.executeInput.GetRequest().GetContext(),
						scheduled.executeInput.GetRequest().GetStepType(),
						scheduled.durability,
						scheduled.executeInput.GetIsTransientStep(),
						startedTime,
						eventTime,
						finalAttempt,
						scheduled.previousFailures,
						scheduled.methodOptions,
					),
					Request:  scheduled.executeInput.GetRequest(),
					Response: executeOutput.GetResponse(),
				},
			},
		))
	default:
		return fmt.Errorf("activity %d output does not match its input", scheduledEventID)
	}
	delete(b.scheduledActivities, scheduledEventID)
	return nil
}

func (b *Builder) RecordActivityFailed(
	eventID int64,
	eventTime time.Time,
	scheduledEventID int64,
	failure *dexpb.StepMethodFailure,
) error {
	scheduled := b.scheduledActivities[scheduledEventID]
	if scheduled == nil {
		return fmt.Errorf("scheduled activity %d is missing", scheduledEventID)
	}
	startedTime, finalAttempt := scheduled.executionTiming()
	switch {
	case scheduled.waitInput != nil:
		b.events = append(b.events, newEvent(
			eventID,
			eventTime,
			&dexpb.FlowHistoryEvent_StepWaitForFailed{
				StepWaitForFailed: &dexpb.StepWaitForFailedEvent{
					Execution: executionInfo(
						scheduled.waitInput.GetRequest().GetContext(),
						scheduled.waitInput.GetRequest().GetStepType(),
						scheduled.durability,
						false,
						startedTime,
						eventTime,
						finalAttempt,
						scheduled.previousFailures,
						scheduled.methodOptions,
					),
					Request: scheduled.waitInput.GetRequest(),
					Failure: failure,
				},
			},
		))
	case scheduled.executeInput != nil:
		b.events = append(b.events, newEvent(
			eventID,
			eventTime,
			&dexpb.FlowHistoryEvent_StepExecuteFailed{
				StepExecuteFailed: &dexpb.StepExecuteFailedEvent{
					Execution: executionInfo(
						scheduled.executeInput.GetRequest().GetContext(),
						scheduled.executeInput.GetRequest().GetStepType(),
						scheduled.durability,
						scheduled.executeInput.GetIsTransientStep(),
						startedTime,
						eventTime,
						finalAttempt,
						scheduled.previousFailures,
						scheduled.methodOptions,
					),
					Request: scheduled.executeInput.GetRequest(),
					Failure: failure,
				},
			},
		))
	default:
		return fmt.Errorf("activity %d is not a step method", scheduledEventID)
	}
	delete(b.scheduledActivities, scheduledEventID)
	return nil
}

func (b *Builder) RecordLocalWaitCompleted(
	eventID int64,
	eventTime time.Time,
	output *dexpb.InvokeWaitForMethodActivityOutput,
	finalAttempt int32,
	previousFailures []*dexpb.StepMethodAttemptFailure,
) {
	response := output.GetResponse()
	b.recordTransientStep(response.GetTransientStepMovement())
	b.events = append(b.events, newEvent(
		eventID,
		eventTime,
		&dexpb.FlowHistoryEvent_StepWaitForCompleted{
			StepWaitForCompleted: &dexpb.StepWaitForCompletedEvent{
				Execution: localExecutionInfo(
					response.GetLocalActivityInput(),
					false,
					finalAttempt,
					previousFailures,
				),
				Response: response,
			},
		},
	))
}

func (b *Builder) RecordLocalExecuteCompleted(
	eventID int64,
	eventTime time.Time,
	output *dexpb.InvokeExecuteMethodActivityOutput,
	finalAttempt int32,
	previousFailures []*dexpb.StepMethodAttemptFailure,
) {
	response := output.GetResponse()
	localInput := response.GetLocalActivityInput()
	isTransient := b.consumeTransientStep(localInput)
	b.events = append(b.events, newEvent(
		eventID,
		eventTime,
		&dexpb.FlowHistoryEvent_StepExecuteCompleted{
			StepExecuteCompleted: &dexpb.StepExecuteCompletedEvent{
				Execution: localExecutionInfo(
					localInput,
					isTransient,
					finalAttempt,
					previousFailures,
				),
				Response: response,
			},
		},
	))
}

func (b *Builder) RecordContinueDump(output *dexpb.DumpFlowForContinueAsNewActivityOutput) {
	response := output.GetResponse()
	b.continueDumpPages[response.GetPageNum()] = response.GetPageContent()
	b.continueDumpTotal = response.GetTotalPages()
}

func (b *Builder) RecordSignal(
	eventID int64,
	eventTime time.Time,
	request *dexpb.ExecuteRpcSignalRequest,
) {
	if isExternalPublish(request) {
		b.events = append(b.events, newEvent(
			eventID,
			eventTime,
			&dexpb.FlowHistoryEvent_ChannelExternalPublish{
				ChannelExternalPublish: &dexpb.ChannelExternalPublishEvent{
					Messages: request.GetPublishToChannel(),
				},
			},
		))
		return
	}
	rpcName := rpcNameFromDecision(request.GetStepDecision())
	if rpcName == "" && request.GetRpcInput() == nil && request.GetRpcOutput() == nil {
		return
	}
	b.events = append(b.events, newEvent(
		eventID,
		eventTime,
		&dexpb.FlowHistoryEvent_RpcExecutionCompleted{
			RpcExecutionCompleted: &dexpb.RpcExecutionCompletedEvent{
				RpcName:          rpcName,
				Input:            request.GetRpcInput(),
				Output:           request.GetRpcOutput(),
				StepDecision:     request.GetStepDecision(),
				UpsertAttributes: request.GetUpsertAttributes(),
				RecordEvents:     request.GetRecordEvents(),
				PublishToChannel: request.GetPublishToChannel(),
			},
		},
	))
}

func (b *Builder) RecordClose(
	eventID int64,
	eventTime time.Time,
	payload *dexpb.FlowClosedHistoryEvent,
) {
	b.events = append(b.events, newEvent(
		eventID,
		eventTime,
		&dexpb.FlowHistoryEvent_FlowClosed{FlowClosed: payload},
	))
}

func (b *Builder) EventsInRange(
	startInternalEventID int64,
	nextInternalEventID int64,
) ([]*dexpb.FlowHistoryEvent, error) {
	if b.startEvent != nil {
		if err := b.populateContinuedStart(); err != nil {
			return nil, err
		}
		b.events = append(b.events, b.startEvent)
	}
	sort.Slice(b.events, func(left int, right int) bool {
		return b.events[left].GetEventId() < b.events[right].GetEventId()
	})
	for startIndex, event := range b.events {
		if event.GetEventId() < startInternalEventID {
			continue
		}
		for endIndex := startIndex; endIndex < len(b.events); endIndex++ {
			if b.events[endIndex].GetEventId() >= nextInternalEventID {
				return b.events[startIndex:endIndex], nil
			}
		}
		return b.events[startIndex:], nil
	}
	return nil, nil
}

func (b *Builder) populateContinuedStart() error {
	payload := b.startEvent.GetFlowStartedOrContinued()
	continued := payload.GetContinuedStart()
	if continued == nil || b.continueDumpTotal == 0 {
		return nil
	}
	var marshaled []byte
	for pageNumber := int32(0); pageNumber < b.continueDumpTotal; pageNumber++ {
		page, ok := b.continueDumpPages[pageNumber]
		if !ok {
			return fmt.Errorf("continue-as-new dump page %d is missing", pageNumber)
		}
		marshaled = append(marshaled, page...)
	}
	var dump dexpb.ContinueAsNewDump
	if err := proto.Unmarshal(marshaled, &dump); err != nil {
		return fmt.Errorf("decode continue-as-new dump: %w", err)
	}
	continued.StepsToStart = dump.GetStepsToStartFromBeginning()
	continued.StepsToResume = dump.GetStepExecutionsToResume()
	continued.PendingChannelMessages = dump.GetChannelReceived()
	continued.Attributes = dump.GetAttributes()
	continued.CompletedSteps = dump.GetStepOutputs()
	return nil
}

func (s *scheduledActivity) executionTiming() (time.Time, int32) {
	startedTime := s.firstStartedTime
	if startedTime.IsZero() {
		startedTime = s.scheduledTime
	}
	finalAttempt := s.finalAttempt
	if finalAttempt <= 0 {
		switch {
		case s.waitInput != nil:
			finalAttempt = s.waitInput.GetRequest().GetContext().GetAttempt()
		case s.executeInput != nil:
			finalAttempt = s.executeInput.GetRequest().GetContext().GetAttempt()
		}
	}
	if finalAttempt <= 0 {
		finalAttempt = 1
	}
	return startedTime, s.priorAttempts + finalAttempt
}

func (b *Builder) recordTransientStep(movement *dexpb.StepMovement) {
	if movement == nil {
		return
	}
	b.transientSteps[transientStepKey(
		movement.GetFromStepExecutionIdInternalOnly(),
		movement.GetStepType(),
	)] = true
}

func (b *Builder) consumeTransientStep(input *dexpb.LocalActivityInput) bool {
	key := transientStepKey(
		input.GetFromStepExecutionId(),
		stepTypeFromExecutionID(input.GetCurrentStepExecutionId()),
	)
	if !b.transientSteps[key] {
		return false
	}
	delete(b.transientSteps, key)
	return true
}

func transientStepKey(fromStepExecutionID string, stepType string) string {
	return fromStepExecutionID + "\x00" + stepType
}

func previousAttemptCount(failures []*dexpb.StepMethodAttemptFailure) int32 {
	var count int32
	for _, failure := range failures {
		if failure.GetAttempt() > count {
			count = failure.GetAttempt()
		}
	}
	return count
}

func newEvent(
	eventID int64,
	eventTime time.Time,
	payload any,
) *dexpb.FlowHistoryEvent {
	event := &dexpb.FlowHistoryEvent{
		EventId:   eventID,
		EventTime: timestamppb.New(eventTime),
	}
	switch payload := payload.(type) {
	case *dexpb.FlowHistoryEvent_FlowStartedOrContinued:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_FlowClosed:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_StepWaitForCompleted:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_StepWaitForFailed:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_StepExecuteCompleted:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_StepExecuteFailed:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_RpcExecutionCompleted:
		event.Payload = payload
	case *dexpb.FlowHistoryEvent_ChannelExternalPublish:
		event.Payload = payload
	default:
		panic("unsupported flow history payload")
	}
	return event
}

func executionInfo(
	context *dexpb.Context,
	stepType string,
	durability dexpb.StepDurability,
	isTransient bool,
	startedTime time.Time,
	completedTime time.Time,
	finalAttempt int32,
	previousFailures []*dexpb.StepMethodAttemptFailure,
	methodOptions *dexpb.StepMethodOptions,
) *dexpb.StepMethodExecutionInfo {
	return &dexpb.StepMethodExecutionInfo{
		StepExecutionId:         context.GetStepExecutionId(),
		FromStepExecutionId:     context.GetFromStepExecutionId(),
		StepType:                stepType,
		FinalAttempt:            finalAttempt,
		Durability:              durability,
		IsTransientStep:         &isTransient,
		FirstStartedTime:        timestamppb.New(startedTime),
		Duration:                durationpb.New(completedTime.Sub(startedTime)),
		PreviousAttemptFailures: previousFailures,
		MethodOptions:           methodOptions,
	}
}

func localExecutionInfo(
	input *dexpb.LocalActivityInput,
	isTransient bool,
	finalAttempt int32,
	previousFailures []*dexpb.StepMethodAttemptFailure,
) *dexpb.StepMethodExecutionInfo {
	if finalAttempt <= 0 {
		finalAttempt = 1
	}
	return &dexpb.StepMethodExecutionInfo{
		StepExecutionId:         input.GetCurrentStepExecutionId(),
		FromStepExecutionId:     input.GetFromStepExecutionId(),
		StepType:                stepTypeFromExecutionID(input.GetCurrentStepExecutionId()),
		FinalAttempt:            finalAttempt,
		Durability:              dexpb.StepDurability_STEP_DURABILITY_ASYNC,
		IsTransientStep:         &isTransient,
		PreviousAttemptFailures: previousFailures,
	}
}

func stepTypeFromExecutionID(stepExecutionID string) string {
	separator := strings.LastIndex(stepExecutionID, "-")
	if separator < 1 {
		return stepExecutionID
	}
	if _, err := strconv.ParseInt(stepExecutionID[separator+1:], 10, 32); err != nil {
		return stepExecutionID
	}
	return stepExecutionID[:separator]
}

func isExternalPublish(request *dexpb.ExecuteRpcSignalRequest) bool {
	return len(request.GetPublishToChannel()) > 0 &&
		request.GetStepDecision() == nil &&
		len(request.GetUpsertAttributes()) == 0 &&
		len(request.GetRecordEvents()) == 0 &&
		request.GetRpcInput() == nil &&
		request.GetRpcOutput() == nil
}

func rpcNameFromDecision(decision *dexpb.StepDecision) string {
	for _, movement := range decision.GetNextSteps() {
		source := movement.GetFromStepExecutionIdInternalOnly()
		if strings.HasPrefix(source, rpcSourcePrefix) {
			return strings.TrimPrefix(source, rpcSourcePrefix)
		}
	}
	return ""
}
