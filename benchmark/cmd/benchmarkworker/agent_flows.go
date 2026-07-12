package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/superdurable/dex/benchmark/internal/log"
	"github.com/superdurable/dex/benchmark/internal/log/tag"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ============================================================================
// Multi-Agent Benchmark Flow
// ============================================================================

var agentClient *dex.Client
var agentLogger log.Logger
var agentTaskListName = dex.DefaultTaskListName

func setAgentClient(client *dex.Client)       { agentClient = client }
func setAgentLogger(logger log.Logger)          { agentLogger = logger }
func setAgentTaskListName(taskListName string)  { agentTaskListName = taskListName }

var (
	keySubAgentResponseCount = dex.NewStateKey[int]("subagent_response_count")
	keyFastLLMRunning        = dex.NewStateKey[bool]("fast_llm_running")
	keySlowLLMRunning        = dex.NewStateKey[bool]("slow_llm_running")
	keyMainNotes             = dex.NewStateKey[[]string]("notes")
	keySubCounter            = dex.NewStateKey[int]("counter")
	keySubParentRunID        = dex.NewStateKey[string]("parent_run_id")
)

var SubAgentRequestCh = dex.NewChannel[subAgentRequest]("SubAgentRequest")
var MainAgentMessageCh = dex.NewChannel[mainAgentMessage]("MainAgentMessage")
var InterruptLLMCh = dex.NewChannel[interruptSignal]("InterruptLLM")
var SubAgentResponseCh = dex.NewDynamicChannel[subAgentResponse]("SubAgentResponse-")

type mainAgentMessageKind string

const (
	msgKindHumanStartSubAgents mainAgentMessageKind = "human.start_subagents"
	msgKindHumanStartLLMLoop   mainAgentMessageKind = "human.start_llm_loop"
	msgKindHumanComplete       mainAgentMessageKind = "human.complete"
	msgKindSubAgentResponse    mainAgentMessageKind = "subagent.response"
)

type mainAgentMessage struct {
	Kind         mainAgentMessageKind `json:"kind"`
	NumSubAgents int                  `json:"num_subagents,omitempty"`
	SubAgentResp *subAgentResponse    `json:"subagent_response,omitempty"`
}

type subAgentRequest struct {
	Source string `json:"source"`
}

type subAgentResponse struct {
	SubAgentRunID string `json:"subagent_run_id"`
	Counter       int    `json:"counter"`
	Message       string `json:"message"`
	IsLast        bool   `json:"is_last"`
}

type interruptSignal struct {
	Reason string `json:"reason,omitempty"`
}

type mainAgentInitInput struct {
	MaxConcurrentSubAgents int `json:"max_concurrent_subagents"`
}

type subAgentSlotInput struct {
	Slot int `json:"slot"`
}

type waitForSubAgentInput struct {
	SubAgentRunID string `json:"subagent_run_id"`
}

type mainAgentFlow struct {
	dex.DefaultFlowType
}

func (f *mainAgentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keySubAgentResponseCount),
			dex.DefineStateKey(keyFastLLMRunning),
			dex.DefineStateKey(keySlowLLMRunning),
			dex.DefineStateKey(keyMainNotes),
		},
		Channels: []dex.ChannelDef{
			dex.DefineChannel(SubAgentRequestCh),
			dex.DefineChannel(MainAgentMessageCh),
			dex.DefineChannel(InterruptLLMCh),
			dex.DefineDynamicChannel(SubAgentResponseCh),
		},
	}
}

func (f *mainAgentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&mainInitStep{}),
		dex.NonStartingStep(&startSubAgentStep{}),
		dex.NonStartingStep(&waitForSubAgentStep{}),
		dex.NonStartingStep(&mainLoopStep{}),
		dex.NonStartingStep(&startLLMStep{}),
		dex.NonStartingStep(&fastLLMStep{}),
		dex.NonStartingStep(&slowLLMStep{}),
		dex.NonStartingStep(&llmLoopStep{}),
	}
}

type mainInitStep struct {
	dex.StepDefaults[mainAgentInitInput]
}

func (s *mainInitStep) Execute(ctx dex.Context, input mainAgentInitInput) (dex.StepDecision, error) {
	maxConcurrent := input.MaxConcurrentSubAgents
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	movements := make([]dex.StepMovement, 0, maxConcurrent+1)
	for slot := 0; slot < maxConcurrent; slot++ {
		movements = append(movements,
			dex.MovementOf(&startSubAgentStep{}, subAgentSlotInput{Slot: slot}))
	}
	movements = append(movements, dex.MovementOf(&mainLoopStep{}, nil))
	return dex.GoToMany(movements...), nil
}

type startSubAgentStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *startSubAgentStep) WaitFor(_ dex.Context, _ subAgentSlotInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		SubAgentRequestCh.ConditionWithMinMax(1, 1),
	), nil
}

func (s *startSubAgentStep) Execute(ctx dex.Context, _ subAgentSlotInput) (dex.StepDecision, error) {
	subagentRunID := buildSubAgentRunID(ctx.RunID(), ctx.StepExecutionID())

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := agentClient.StartRunWithOptions(
		startCtx, subagentRunID, &subAgentFlow{},
		&dex.RunOptions{TaskListName: agentTaskListName},
		subAgentInitInput{ParentRunID: ctx.RunID()},
	)
	if err != nil && !isAlreadyExists(err) {
		if agentLogger != nil {
			agentLogger.Error("failed to start subagent",
				tag.RunID(ctx.RunID()),
				tag.SubAgentRunID(subagentRunID),
				tag.Error(err),
			)
		}
		return dex.DeadEnd(), nil
	}

	return dex.GoTo(&waitForSubAgentStep{}, waitForSubAgentInput{SubAgentRunID: subagentRunID}), nil
}

func buildSubAgentRunID(parentRunID, stepExecutionID string) string {
	return "subagent-" + parentRunID + "-" + stepExecutionID
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	return status.Code(err) == codes.AlreadyExists
}

type waitForSubAgentStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

const subAgentStuckTimeout = 30 * time.Second

func (s *waitForSubAgentStep) WaitFor(_ dex.Context, input waitForSubAgentInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		SubAgentResponseCh.ConditionWithMinMax(input.SubAgentRunID, 1, 1),
		dex.Timer(subAgentStuckTimeout),
	), nil
}

func (s *waitForSubAgentStep) Execute(ctx dex.Context, input waitForSubAgentInput) (dex.StepDecision, error) {
	msgs, err := SubAgentResponseCh.GetConsumedMessages(ctx, input.SubAgentRunID)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		if agentLogger != nil {
			agentLogger.Debug("subagent stuck detector fired",
				tag.RunID(ctx.RunID()),
				tag.SubAgentRunID(input.SubAgentRunID),
			)
		}
		return dex.GoTo(&waitForSubAgentStep{}, input), nil
	}

	resp := msgs[0]
	bridge := mainAgentMessage{
		Kind:         msgKindSubAgentResponse,
		SubAgentResp: &resp,
	}
	if err := MainAgentMessageCh.Publish(ctx, bridge); err != nil {
		return nil, err
	}

	if resp.IsLast {
		return dex.GoTo(&startSubAgentStep{}, subAgentSlotInput{}), nil
	}
	return dex.GoTo(&waitForSubAgentStep{}, input), nil
}

type mainLoopStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *mainLoopStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		MainAgentMessageCh.ConditionWithMinMax(1, 100),
	), nil
}

type mainLoopAction struct {
	complete          bool
	startLLMLoop      bool
	subagentRequests  int
	subagentResponses int
}

func classifyMainAgentMessages(msgs []mainAgentMessage) mainLoopAction {
	action := mainLoopAction{}
	for _, message := range msgs {
		switch message.Kind {
		case msgKindHumanComplete:
			action.complete = true
		case msgKindHumanStartLLMLoop:
			action.startLLMLoop = true
		case msgKindHumanStartSubAgents:
			numSubAgents := message.NumSubAgents
			if numSubAgents <= 0 {
				numSubAgents = 1
			}
			action.subagentRequests += numSubAgents
		case msgKindSubAgentResponse:
			action.subagentResponses++
		}
	}
	return action
}

func (s *mainLoopStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	msgs, err := MainAgentMessageCh.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	action := classifyMainAgentMessages(msgs)

	var responseCount int
	responseCount, err = keySubAgentResponseCount.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	responseCount += action.subagentResponses

	switch {
	case action.complete:
		if agentLogger != nil {
			agentLogger.Info("mainAgent received human complete", tag.RunID(ctx.RunID()))
		}
		if err := keySubAgentResponseCount.SetValue(ctx, responseCount); err != nil {
			return nil, err
		}
		return dex.Complete(nil), nil
	case action.startLLMLoop:
		if agentLogger != nil {
			agentLogger.Info("mainAgent starting LLM loop", tag.RunID(ctx.RunID()))
		}
		if err := keySubAgentResponseCount.SetValue(ctx, responseCount); err != nil {
			return nil, err
		}
		if err := keyFastLLMRunning.SetValue(ctx, true); err != nil {
			return nil, err
		}
		if err := keySlowLLMRunning.SetValue(ctx, true); err != nil {
			return nil, err
		}
		return dex.GoTo(&startLLMStep{}, nil), nil
	}

	if err := keySubAgentResponseCount.SetValue(ctx, responseCount); err != nil {
		return nil, err
	}
	if action.subagentRequests > 0 {
		request := subAgentRequest{Source: "main_agent_loop"}
		for index := 0; index < action.subagentRequests; index++ {
			if err := SubAgentRequestCh.Publish(ctx, request); err != nil {
		return nil, err
	}
		}
	}
	return dex.GoTo(&mainLoopStep{}, nil), nil
}

type startLLMStep struct {
	dex.StepDefaults[any]
}

func (s *startLLMStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&fastLLMStep{}, nil),
		dex.MovementOf(&slowLLMStep{}, nil),
		dex.MovementOf(&llmLoopStep{}, nil),
	), nil
}

const fastLLMSleep = 5 * time.Second
const slowLLMSleep = 60 * time.Second

type fastLLMStep struct {
	dex.StepDefaults[any]
}

func (s *fastLLMStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	case <-time.After(fastLLMSleep):
	}
	if err := keyFastLLMRunning.SetValue(ctx, false); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type slowLLMStep struct {
	dex.StepDefaults[any]
}

func (s *slowLLMStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	case <-time.After(slowLLMSleep):
	}
	if err := keySlowLLMRunning.SetValue(ctx, false); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type llmLoopStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *llmLoopStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		InterruptLLMCh.ConditionWithMinMax(1, 100),
	), nil
}

func (s *llmLoopStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if agentLogger != nil {
		agentLogger.Info("mainAgent interrupt fired, cancelling LLM siblings",
			tag.RunID(ctx.RunID()),
			tag.Count(2),
		)
	}
	return dex.GoTo(&mainLoopStep{}, nil).
		WithCancelingSiblingStepExecution(
			dex.CancelOf(&fastLLMStep{}),
			dex.CancelOf(&slowLLMStep{}),
		), nil
}

type subAgentInitInput struct {
	ParentRunID string `json:"parent_run_id"`
}

type subAgentFlow struct {
	dex.DefaultFlowType
}

func (f *subAgentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keySubCounter),
			dex.DefineStateKey(keySubParentRunID),
		},
		Channels: []dex.ChannelDef{
			dex.DefineDynamicChannel(SubAgentResponseCh),
		},
	}
}

func (f *subAgentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&subAgentInitStep{}),
		dex.NonStartingStep(&subAgentLoopStep{}),
	}
}

type subAgentInitStep struct {
	dex.StepDefaults[subAgentInitInput]
}

func (s *subAgentInitStep) Execute(ctx dex.Context, input subAgentInitInput) (dex.StepDecision, error) {
	if err := keySubCounter.SetValue(ctx, 1); err != nil {
		return nil, err
	}
	if err := keySubParentRunID.SetValue(ctx, input.ParentRunID); err != nil {
		return nil, err
	}
	return dex.GoTo(&subAgentLoopStep{}, nil), nil
}

const (
	subAgentMinSleep = 1 * time.Second
	subAgentMaxSleep = 40 * time.Second
	subAgentMaxIters = 3
)

type subAgentLoopStep struct {
	dex.StepDefaults[any]
}

func (s *subAgentLoopStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	span := int64(subAgentMaxSleep - subAgentMinSleep)
	delay := subAgentMinSleep + time.Duration(rand.Int63n(span))
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	case <-time.After(delay):
	}

	counter, err := keySubCounter.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	isLast := counter >= subAgentMaxIters
	response := subAgentResponse{
		SubAgentRunID: ctx.RunID(),
		Counter:       counter,
		Message:       fmt.Sprintf("this is %d message from subagent %s", counter, ctx.RunID()),
		IsLast:        isLast,
	}

	parentRunID, err := keySubParentRunID.GetValue(ctx)
	if err != nil {
		return nil, err
	}

	pubCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := agentClient.PublishToDynamicChannel(pubCtx, parentRunID, SubAgentResponseCh.Prefix, ctx.RunID(), response); err != nil {
		if agentLogger != nil {
			agentLogger.Error("subagent failed to publish response",
				tag.RunID(ctx.RunID()),
				tag.Error(err),
			)
		}
	}

	if isLast {
		return dex.Complete(nil), nil
	}
	if err := keySubCounter.SetValue(ctx, counter+1); err != nil {
		return nil, err
	}
	return dex.GoTo(&subAgentLoopStep{}, nil), nil
}
