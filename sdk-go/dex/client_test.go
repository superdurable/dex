package dex

import (
	"errors"
	"testing"
)

// TestRunOptions_TaskListNameDefaultsToDefault pins the default-tasklist
// contract on both sides: a nil RunOptions and an empty TaskListName both
// resolve to DefaultTaskListName, matching the WorkerOptions default so the
// no-options-on-either-side case Just Works.
func TestRunOptions_TaskListNameDefaultsToDefault(t *testing.T) {
	if got := (*RunOptions)(nil).taskListName(); got != DefaultTaskListName {
		t.Errorf("nil RunOptions taskListName() = %q, want %q", got, DefaultTaskListName)
	}
	if got := (&RunOptions{}).taskListName(); got != DefaultTaskListName {
		t.Errorf("empty RunOptions taskListName() = %q, want %q", got, DefaultTaskListName)
	}
	if got := (&RunOptions{TaskListName: "g1"}).taskListName(); got != "g1" {
		t.Errorf("explicit TaskListName taskListName() = %q, want %q", got, "g1")
	}
	if got := (WorkerOptions{}).taskListName(); got != DefaultTaskListName {
		t.Errorf("WorkerOptions{} taskListName() = %q, want %q", got, DefaultTaskListName)
	}
	if got := (WorkerOptions{TaskListName: "g2"}).taskListName(); got != "g2" {
		t.Errorf("WorkerOptions{TaskListName:g2} taskListName() = %q, want %q", got, "g2")
	}
}

// fakeStep is a placeholder step used only for buildStartingSteps tests.
// The function only needs reg.steps[stepID] to be non-nil and ShouldSkipWaitFor
// to evaluate against the value's type — it does not invoke the step itself.
type fakeStep struct {
	StepDefaults[any]
}

func (fakeStep) Execute(_ Context, _ any) (StepDecision, error) {
	return DeadEnd(), nil
}

func makeRegWithStartingSteps(ids ...string) flowRegistration {
	reg := flowRegistration{
		steps:         map[string]stepCommon{},
		startingSteps: append([]string(nil), ids...),
	}
	for _, id := range ids {
		reg.steps[id] = &fakeStep{}
	}
	return reg
}

// TestBuildStartingSteps_ZeroInputsAllNil verifies the "0 inputs" branch:
// every starting step gets a nil input. Encoding nil should not error
// (EncodeValue maps nil to a NullValue Value).
func TestBuildStartingSteps_ZeroInputsAllNil(t *testing.T) {
	reg := makeRegWithStartingSteps("s1", "s2", "s3")
	got, err := buildStartingSteps(reg, DefaultObjectCodec(), nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, ns := range got {
		if ns.Input == nil {
			t.Errorf("step %d Input = nil pb.Value (must be encoded NullValue, not nil ptr)", i)
		}
	}
}

// TestBuildStartingSteps_SingleInputOnSingleStartingStepWorks pins the
// "1 input on a flow with 1 starting step" path — this is the common case
// for the vast majority of flows (one starting step that takes the run's
// input payload). Strictly the N==count rule, no broadcast magic.
func TestBuildStartingSteps_SingleInputOnSingleStartingStepWorks(t *testing.T) {
	reg := makeRegWithStartingSteps("only")
	got, err := buildStartingSteps(reg, DefaultObjectCodec(), []any{map[string]any{"k": "v"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].StepId != "only" {
		t.Errorf("StepId = %q, want %q", got[0].StepId, "only")
	}
	if got[0].Input == nil {
		t.Errorf("Input = nil — single input must be encoded onto the single starting step")
	}
}

// TestBuildStartingSteps_SingleInputOnMultiStartingStepsErrors guards the
// newly-strict contract: 1 input does NOT broadcast to multiple starting
// steps. Silently sending the same payload to N steps is almost always a
// caller bug (the right intent is N distinct payloads), so we fail loudly.
func TestBuildStartingSteps_SingleInputOnMultiStartingStepsErrors(t *testing.T) {
	reg := makeRegWithStartingSteps("a", "b", "c")
	_, err := buildStartingSteps(reg, DefaultObjectCodec(), []any{map[string]any{"k": "v"}})
	if err == nil {
		t.Fatal("expected error on 1 input vs 3 starting steps (broadcast was intentionally removed); got nil")
	}
	if !errors.Is(err, ErrInputCountMismatch) {
		t.Fatalf("expected ErrInputCountMismatch, got %v", err)
	}
}

// TestBuildStartingSteps_PerStepInputsMatchesOrder verifies the
// "N inputs == N starting steps" branch: each starting step gets its own
// input by registration-order index. This is the headline new behavior
// that fixed the prior single-input limitation.
func TestBuildStartingSteps_PerStepInputsMatchesOrder(t *testing.T) {
	reg := makeRegWithStartingSteps("first", "second")
	got, err := buildStartingSteps(reg, DefaultObjectCodec(), []any{
		map[string]any{"who": "first"},
		map[string]any{"who": "second"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].StepId != "first" || got[1].StepId != "second" {
		t.Errorf("step order wrong: got [%q, %q], want [first, second]",
			got[0].StepId, got[1].StepId)
	}
	// Inputs must be distinct encoded values. We don't decode here,
	// just assert both are populated; the round-trip behavior is
	// covered end-to-end by sdke2e tests.
	if got[0].Input == got[1].Input {
		t.Errorf("per-step inputs aliased — buildStartingSteps must encode each input separately")
	}
}

// TestBuildStartingSteps_LengthMismatchErrors verifies the safety guard:
// 2 inputs for 3 steps (or any other non-{0,1,N} length) must error
// rather than silently dropping or aliasing inputs.
func TestBuildStartingSteps_LengthMismatchErrors(t *testing.T) {
	reg := makeRegWithStartingSteps("s1", "s2", "s3")
	_, err := buildStartingSteps(reg, DefaultObjectCodec(), []any{"a", "b"})
	if err == nil {
		t.Fatal("expected error on inputs length 2 vs starting steps 3, got nil")
	}
	if !errors.Is(err, ErrInputCountMismatch) {
		t.Fatalf("expected ErrInputCountMismatch, got %v", err)
	}
}

// TestBuildStartingSteps_NoStartingStepsErrors guards against silently
// returning an empty NextStep list — the engine would reject it, but
// we want a clearer error from the SDK before going on the wire.
func TestBuildStartingSteps_NoStartingStepsErrors(t *testing.T) {
	reg := flowRegistration{steps: map[string]stepCommon{}, startingSteps: nil}
	_, err := buildStartingSteps(reg, DefaultObjectCodec(), nil)
	if err == nil {
		t.Fatal("expected error when flow has no starting steps")
	}
	if !errors.Is(err, ErrNoStartingSteps) {
		t.Fatalf("expected ErrNoStartingSteps, got %v", err)
	}
}

type intInputStep struct {
	StepDefaults[int]
}

func (intInputStep) Execute(_ Context, _ int) (StepDecision, error) {
	return DeadEnd(), nil
}

type stringInputStep struct {
	StepDefaults[string]
}

func (stringInputStep) Execute(_ Context, _ string) (StepDecision, error) {
	return DeadEnd(), nil
}

func TestBuildStartingSteps_InputTypeMismatchErrors(t *testing.T) {
	reg := flowRegistration{
		steps: map[string]stepCommon{
			"intStep":    intInputStep{},
			"stringStep": stringInputStep{},
		},
		startingSteps: []string{"intStep", "stringStep"},
	}
	_, err := buildStartingSteps(reg, DefaultObjectCodec(), []any{"not-an-int", "ok"})
	if err == nil {
		t.Fatal("expected type mismatch error, got nil")
	}
	if !errors.Is(err, ErrStartingStepInputMismatch) {
		t.Fatalf("expected ErrStartingStepInputMismatch, got %v", err)
	}
}

func TestBuildStartingSteps_InputTypeMatchSucceeds(t *testing.T) {
	reg := flowRegistration{
		steps: map[string]stepCommon{
			"intStep":    intInputStep{},
			"stringStep": stringInputStep{},
		},
		startingSteps: []string{"intStep", "stringStep"},
	}
	got, err := buildStartingSteps(reg, DefaultObjectCodec(), []any{42, "hello"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}
