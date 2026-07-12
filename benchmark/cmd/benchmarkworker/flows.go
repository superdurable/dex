package main

import (
	"strings"

	"github.com/superdurable/dex/sdk-go/dex"
)

type benchmarkTriggerInput struct {
	NumSteps  int `json:"num_steps"`
	StateSize int `json:"state_size"`
}

type parallelStepInput struct {
	StepIndex int `json:"step_index"`
	StateSize int `json:"state_size"`
}

var (
	keyPayload     = dex.NewStateKey[string]("payload")
	keyCurrentStep = dex.NewStateKey[int]("current_step")
	keyLastStep    = dex.NewStateKey[int]("last_step")
)

func benchmarkFlowSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keyPayload),
			dex.DefineStateKey(keyCurrentStep),
			dex.DefineStateKey(keyLastStep),
		},
	}
}

type sequentialLoopStep struct {
	dex.StepDefaults[benchmarkTriggerInput]
}

func (s *sequentialLoopStep) Execute(ctx dex.Context, input benchmarkTriggerInput) (dex.StepDecision, error) {
	stepCount := maxInt(input.NumSteps, 1)
	currentStep, err := keyCurrentStep.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	current := currentStep + 1
	if err := keyPayload.SetValue(ctx, payloadOfSize(input.StateSize)); err != nil {
		return nil, err
	}
	if err := keyCurrentStep.SetValue(ctx, current); err != nil {
		return nil, err
	}
	if err := keyLastStep.SetValue(ctx, current); err != nil {
		return nil, err
	}

	if current >= stepCount {
		return dex.Complete(nil), nil
	}
	return dex.GoTo(&sequentialLoopStep{}, input), nil
}

type sequentialBenchmarkFlow struct {
	dex.FlowDefaults
}

func (f *sequentialBenchmarkFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return benchmarkFlowSchema()
}

func (f *sequentialBenchmarkFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&sequentialLoopStep{}),
	}
}

type parallelInitStep struct {
	dex.StepDefaults[benchmarkTriggerInput]
}

func (s *parallelInitStep) Execute(ctx dex.Context, input benchmarkTriggerInput) (dex.StepDecision, error) {
	stepCount := maxInt(input.NumSteps, 1)
	movements := make([]dex.StepMovement, 0, stepCount)
	for index := 1; index <= stepCount; index++ {
		movements = append(movements, dex.MovementOf(
			&parallelWorkerStep{},
			parallelStepInput{StepIndex: index, StateSize: input.StateSize},
		))
	}
	return dex.GoToMany(movements...), nil
}

type parallelWorkerStep struct {
	dex.StepDefaults[parallelStepInput]
}

func (s *parallelWorkerStep) Execute(ctx dex.Context, input parallelStepInput) (dex.StepDecision, error) {
	if err := keyPayload.SetValue(ctx, payloadOfSize(input.StateSize)); err != nil {
		return nil, err
	}
	if err := keyLastStep.SetValue(ctx, input.StepIndex); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type parallelBenchmarkFlow struct {
	dex.FlowDefaults
}

func (f *parallelBenchmarkFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return benchmarkFlowSchema()
}

func (f *parallelBenchmarkFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&parallelInitStep{}),
		dex.NonStartingStep(&parallelWorkerStep{}),
	}
}

func payloadOfSize(size int) string {
	if size <= 0 {
		return ""
	}
	return strings.Repeat("x", size)
}

func maxInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
