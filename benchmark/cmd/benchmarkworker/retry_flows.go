package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

var retryFlowCounters sync.Map

type retryTriggerInput struct {
	// FinalOutcome is "succeed" (default) or "fail".
	// succeed: fail attempts 1-4, Complete on 5+.
	// fail: fail attempts 1-4, then permanent errors until retry exhausted.
	FinalOutcome string
}

type retryBenchmarkFlow struct {
	dex.FlowDefaults
}

func (retryBenchmarkFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&retryBenchmarkStep{}),
	}
}

type retryBenchmarkStep struct {
	dex.StepDefaults[retryTriggerInput]
}

func (s *retryBenchmarkStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 500 * time.Millisecond,
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:        5,
			InitialInterval:    3 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
		},
	}
}

func (s *retryBenchmarkStep) Execute(ctx dex.Context, input retryTriggerInput) (dex.StepDecision, error) {
	counterValue, _ := retryFlowCounters.LoadOrStore(ctx.RunID(), &atomic.Int32{})
	counter := counterValue.(*atomic.Int32)
	attempt := counter.Add(1)
	if attempt < 5 {
		return nil, retryTransientError(attempt)
	}
	if parseRetryFinalOutcome(input.FinalOutcome) == "fail" {
		return nil, retryPermanentError(attempt)
	}
	return dex.Complete(map[string]any{"attempts": attempt, "finalOutcome": "succeed"}), nil
}

func retryTransientError(attempt int32) error {
	return retryWrapMethodError(fmt.Errorf("transient benchmark failure attempt %d", attempt))
}

func retryPermanentError(attempt int32) error {
	return retryWrapMethodError(fmt.Errorf("permanent failure on attempt %d", attempt))
}

func retryWrapMethodError(err error) error {
	return dex.ErrorWrap(err)
}

func parseRetryFinalOutcome(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "fail", "failed":
		return "fail"
	default:
		return "succeed"
	}
}
