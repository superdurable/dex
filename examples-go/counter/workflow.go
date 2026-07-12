// Package counter demonstrates a simple self-looping workflow.
//
// Flow: Increment step loops, adding to the counter each iteration,
// until the count reaches the threshold, then completes.
package counter

import (
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	keyCount     = dex.NewStateKey[int]("Count")
	keyThreshold = dex.NewStateKey[int]("Threshold")
)

// --- Flow ---

type CounterFlow struct {
	dex.FlowDefaults
}

func (f *CounterFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keyCount),
			dex.DefineStateKey(keyThreshold),
		},
	}
}

func (f *CounterFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&InitStep{}),
		dex.NonStartingStep(&IncrementStep{}),
	}
}

// --- Step 1: Init (set threshold, transition to increment loop) ---

type InitStep struct {
	dex.StepDefaults[int]
}

func (s *InitStep) Execute(ctx dex.Context, threshold int) (dex.StepDecision, error) {
	if err := keyCount.SetValue(ctx, 0); err != nil {
		return nil, err
	}
	if err := keyThreshold.SetValue(ctx, threshold); err != nil {
		return nil, err
	}
	return dex.GoTo(&IncrementStep{}, 1), nil
}

// --- Step 2: Increment (self-loop until threshold) ---

type IncrementStep struct {
	dex.StepDefaults[int]
}

func (s *IncrementStep) Execute(ctx dex.Context, addValue int) (dex.StepDecision, error) {
	count, err := keyCount.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	threshold, err := keyThreshold.GetValue(ctx)
	if err != nil {
		return nil, err
	}

	newCount := count + addValue
	if err := keyCount.SetValue(ctx, newCount); err != nil {
		return nil, err
	}

	if newCount >= threshold {
		return dex.Complete(newCount), nil
	}
	return dex.GoTo(&IncrementStep{}, addValue), nil
}
