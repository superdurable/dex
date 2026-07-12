package dex

import (
	"testing"
)

type cancelTestStep struct {
	StepDefaults[any]
}

func (*cancelTestStep) Execute(_ Context, _ any) (StepDecision, error) {
	return DeadEnd(), nil
}

func TestCancelOf_DerivesStepIDFromType(t *testing.T) {
	ref := CancelOf[any](&cancelTestStep{})

	want := defaultStepIdFromType(&cancelTestStep{})
	if ref.StepID != want {
		t.Errorf("CancelOf(&cancelTestStep{}).StepID = %q, want %q", ref.StepID, want)
	}
	if ref.StepID == "" {
		t.Error("CancelOf must never return an empty StepID for a struct argument")
	}
}

type customIdStep struct {
	StepDefaults[any]
}

func (*customIdStep) GetStepId() string { return "custom-id" }

func (*customIdStep) Execute(_ Context, _ any) (StepDecision, error) {
	return DeadEnd(), nil
}

func TestCancelOf_PrefersExplicitGetStepId(t *testing.T) {
	ref := CancelOf[any](&customIdStep{})
	if ref.StepID != "custom-id" {
		t.Errorf("CancelOf must use explicit GetStepId() result; got %q, want custom-id", ref.StepID)
	}
}

func TestCancelOf_AcceptsExplicitTypeParams(t *testing.T) {
	ref := CancelOf[any](&cancelTestStep{})
	want := defaultStepIdFromType(&cancelTestStep{})
	if ref.StepID != want {
		t.Errorf("explicit-type-params form returned %q, want %q", ref.StepID, want)
	}
}

func TestWithCancelingSiblingStepExecution_Appends(t *testing.T) {
	decision := Complete(nil).(*stepDecisionImpl)
	decision.WithCancelingSiblingStepExecution(
		CancelSiblingStepRef{StepID: "a"},
		CancelSiblingStepRef{StepID: "b"},
	)
	decision.WithCancelingSiblingStepExecution(
		CancelSiblingStepRef{StepID: "c"},
	)
	want := []string{"a", "b", "c"}
	if len(decision.CancelSiblingStepIDs) != len(want) {
		t.Fatalf("CancelSiblingStepIDs len = %d, want %d", len(decision.CancelSiblingStepIDs), len(want))
	}
	for i, id := range want {
		if decision.CancelSiblingStepIDs[i] != id {
			t.Errorf("CancelSiblingStepIDs[%d] = %q, want %q", i, decision.CancelSiblingStepIDs[i], id)
		}
	}
}

func TestWithCancelingSiblingStepExecution_NoArgsNoOp(t *testing.T) {
	decision := Complete(nil).(*stepDecisionImpl)
	decision.WithCancelingSiblingStepExecution()

	if decision.CancelSiblingStepIDs != nil {
		t.Errorf("zero-ref call must leave CancelSiblingStepIDs nil; got %v", decision.CancelSiblingStepIDs)
	}
}

func TestWithCancelingSiblingStepExecution_ReturnsSameDecisionForChaining(t *testing.T) {
	decision := Complete(nil).(*stepDecisionImpl)
	got := decision.WithCancelingSiblingStepExecution(CancelSiblingStepRef{StepID: "x"})
	if got != decision {
		t.Error("WithCancelingSiblingStepExecution must return the same StepDecision for chaining")
	}
}
