package dex

import (
	"reflect"
	"strings"
	"testing"
)

type proceedTestInput struct {
	Token string
}

type proceedFailingStep struct {
	StepDefaults[proceedTestInput]
}

func (s *proceedFailingStep) GetStepOptions() *StepOptions {
	return &StepOptions{
		ExecuteMethodProceedToAfterRetryExhausted: &proceedHandlerStep{},
	}
}

func (s *proceedFailingStep) Execute(_ Context, _ proceedTestInput) (StepDecision, error) {
	return nil, ErrorWrap(nil)
}

type proceedHandlerStep struct {
	StepDefaults[proceedTestInput]
}

func (s *proceedHandlerStep) Execute(_ Context, _ proceedTestInput) (StepDecision, error) {
	return Complete(nil), nil
}

type proceedWrongInputStep struct {
	StepDefaults[string]
}

func (s *proceedWrongInputStep) Execute(_ Context, _ string) (StepDecision, error) {
	return Complete(nil), nil
}

type proceedAnyHandlerStep struct {
	StepDefaults[any]
}

func (s *proceedAnyHandlerStep) Execute(_ Context, _ any) (StepDecision, error) {
	return Complete(nil), nil
}

type proceedValidFlow struct {
	FlowDefaults
}

func (proceedValidFlow) GetSteps() []StepDef {
	return []StepDef{
		StartingStep[proceedTestInput](&proceedFailingStep{}),
		NonStartingStep(&proceedHandlerStep{}),
	}
}

type proceedUnregisteredHandlerFlow struct {
	FlowDefaults
}

func (proceedUnregisteredHandlerFlow) GetSteps() []StepDef {
	return []StepDef{
		StartingStep[proceedTestInput](&proceedFailingStep{}),
	}
}

type proceedMismatchFailingStep struct {
	StepDefaults[proceedTestInput]
}

func (s *proceedMismatchFailingStep) GetStepOptions() *StepOptions {
	return &StepOptions{
		ExecuteMethodProceedToAfterRetryExhausted: &proceedWrongInputStep{},
	}
}

func (s *proceedMismatchFailingStep) Execute(_ Context, _ proceedTestInput) (StepDecision, error) {
	return nil, ErrorWrap(nil)
}

type proceedMismatchFlow struct {
	FlowDefaults
}

func (proceedMismatchFlow) GetSteps() []StepDef {
	return []StepDef{
		StartingStep[proceedTestInput](&proceedMismatchFailingStep{}),
		NonStartingStep(&proceedWrongInputStep{}),
	}
}

type proceedAnyFailingStep struct {
	StepDefaults[proceedTestInput]
}

func (s *proceedAnyFailingStep) GetStepOptions() *StepOptions {
	return &StepOptions{
		ExecuteMethodProceedToAfterRetryExhausted: &proceedAnyHandlerStep{},
	}
}

func (s *proceedAnyFailingStep) Execute(_ Context, _ proceedTestInput) (StepDecision, error) {
	return nil, ErrorWrap(nil)
}

type proceedAnyHandlerFlow struct {
	FlowDefaults
}

func (proceedAnyHandlerFlow) GetSteps() []StepDef {
	return []StepDef{
		StartingStep[proceedTestInput](&proceedAnyFailingStep{}),
		NonStartingStep(&proceedAnyHandlerStep{}),
	}
}

func TestRegistry_RegisterProceedHandlerValid(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&proceedValidFlow{})
}

func TestRegistry_RegisterProceedHandlerNotInFlow(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic")
		}
		msg, ok := recovered.(string)
		if !ok || !strings.Contains(msg, "not registered") {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()
	registry := NewRegistry()
	registry.Register(&proceedUnregisteredHandlerFlow{})
}

func TestRegistry_RegisterProceedHandlerInputMismatch(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic")
		}
		msg, ok := recovered.(string)
		if !ok || !strings.Contains(msg, "incompatible") {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()
	registry := NewRegistry()
	registry.Register(&proceedMismatchFlow{})
}

func TestRegistry_RegisterProceedHandlerAnyInput(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&proceedAnyHandlerFlow{})
}

func TestProceedInputTypesCompatible(t *testing.T) {
	if !proceedInputTypesCompatible(reflectTypeOf[proceedTestInput](), reflectTypeOf[proceedTestInput]()) {
		t.Fatal("same type should be compatible")
	}
	if !proceedInputTypesCompatible(reflectTypeOf[proceedTestInput](), anyInputType()) {
		t.Fatal("any handler should accept concrete failing input")
	}
	if proceedInputTypesCompatible(reflectTypeOf[string](), reflectTypeOf[proceedTestInput]()) {
		t.Fatal("mismatched types should not be compatible")
	}
}

func reflectTypeOf[T any]() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

func anyInputType() reflect.Type {
	return reflect.TypeOf((*any)(nil)).Elem()
}
