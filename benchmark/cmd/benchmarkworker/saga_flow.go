package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

type sagaTriggerInput struct {
	Token      string `json:"token"`
	MethodKind string `json:"method_kind"`
}

type sagaWaitForBenchmarkFlow struct {
	dex.FlowDefaults
}

func (sagaWaitForBenchmarkFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[sagaTriggerInput](&sagaFailingWaitStep{}),
		dex.NonStartingStep(&sagaHandlerStep{}),
	}
}

type sagaExecuteBenchmarkFlow struct {
	dex.FlowDefaults
}

func (sagaExecuteBenchmarkFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[sagaTriggerInput](&sagaFailingExecuteStep{}),
		dex.NonStartingStep(&sagaHandlerStep{}),
	}
}

type sagaFailingWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *sagaFailingWaitStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 500 * time.Millisecond,
		},
		WaitForMethodProceedToAfterRetryExhausted: &sagaHandlerStep{},
	}
}

func (s *sagaFailingWaitStep) WaitFor(_ dex.Context, _ sagaTriggerInput) (dex.WaitForCondition, error) {
	return nil, sagaWrapError(fmt.Errorf("saga waitfor always fails"))
}

func (s *sagaFailingWaitStep) Execute(_ dex.Context, _ sagaTriggerInput) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type sagaFailingExecuteStep struct {
	dex.StepDefaults[sagaTriggerInput]
}

func (s *sagaFailingExecuteStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 500 * time.Millisecond,
		},
		ExecuteMethodProceedToAfterRetryExhausted: &sagaHandlerStep{},
	}
}

func (s *sagaFailingExecuteStep) Execute(_ dex.Context, _ sagaTriggerInput) (dex.StepDecision, error) {
	return nil, sagaWrapError(fmt.Errorf("saga execute always fails"))
}

type sagaHandlerStep struct {
	dex.StepDefaults[sagaTriggerInput]
}

func (s *sagaHandlerStep) Execute(ctx dex.Context, input sagaTriggerInput) (dex.StepDecision, error) {
	return dex.Complete(map[string]any{
		"recovered":      true,
		"fromStepExeId":  ctx.FromStepExecutionID(),
		"fromStepId":     dex.StepIDFromStepExecutionID(ctx.FromStepExecutionID()),
		"token":          input.Token,
		"methodKindSeen": input.MethodKind,
	}), nil
}

func sagaWrapError(err error) error {
	return dex.ErrorWrap(err)
}

func parseSagaMethodKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "waitfor", "wait_for", "wait":
		return "waitFor"
	default:
		return "execute"
	}
}
