package dex

import (
	"context"
	"testing"
)

type fakeContext struct {
	context.Context
}

func (fakeContext) RunID() string                       { return "run" }
func (fakeContext) StepExecutionID() string             { return "step" }
func (fakeContext) FromStepExecutionID() string         { return "" }
func (fakeContext) GetShutdownChannel() <-chan struct{} { return nil }
func (fakeContext) TimerFired() bool                   { return false }

func TestContext_TimerFired(t *testing.T) {
	ctx := NewTestContext(context.Background(), PersistenceSchema{}, nil, true, nil)
	if !ctx.TimerFired() {
		t.Fatal("expected timer fired")
	}
	notFired := NewTestContext(context.Background(), PersistenceSchema{}, nil, false, nil)
	if notFired.TimerFired() {
		t.Fatal("expected timer not fired")
	}
}

func TestAsStepContext_InvalidContext(t *testing.T) {
	keyCount := NewStateKey[int]("count")
	_, err := keyCount.GetValue(fakeContext{Context: context.Background()})
	if err == nil {
		t.Fatal("expected error for invalid Context type")
	}
}
