package dex

import (
	"reflect"
	"strings"
)

// stepCommon is the type-erased surface shared by every Step[IN].
type stepCommon interface {
	GetStepId() string
	GetStepOptions() *StepOptions
}

// Step is the core building block of a workflow.
type Step[IN any] interface {
	stepCommon
	WaitFor(ctx Context, input IN) (WaitForCondition, error)
	Execute(ctx Context, input IN) (StepDecision, error)
}

// DefaultStepId provides a default empty GetStepId.
type DefaultStepId struct{}

func (DefaultStepId) GetStepId() string { return "" }

// DefaultStepOptions provides a default nil GetStepOptions.
type DefaultStepOptions struct{}

func (DefaultStepOptions) GetStepOptions() *StepOptions { return nil }

// NoWaitFor is embedded in steps that skip the WaitFor phase.
type NoWaitFor[IN any] struct{}

func (NoWaitFor[IN]) WaitFor(_ Context, _ IN) (WaitForCondition, error) {
	panic("NoWaitFor: WaitFor must not be called; the framework should skip it")
}

// StepDefaults combines defaults for execute-only steps.
type StepDefaults[IN any] struct {
	DefaultStepId
	DefaultStepOptions
	NoWaitFor[IN]
}

// GetFinalStepId returns the step ID to use.
func GetFinalStepId[IN any](step Step[IN]) string {
	return stepIDFromCommon(step)
}

func stepIDFromCommon(step stepCommon) string {
	id := step.GetStepId()
	if id != "" {
		return id
	}
	return defaultStepIdFromType(step)
}

func defaultStepIdFromType(value any) string {
	rt := reflect.TypeOf(value)
	return strings.TrimLeft(rt.String(), "*")
}

// ShouldSkipWaitFor returns true if the step embeds NoWaitFor or StepDefaults.
func ShouldSkipWaitFor(step stepCommon) bool {
	rt := reflect.TypeOf(step)
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return false
	}
	for index := 0; index < rt.NumField(); index++ {
		name := rt.Field(index).Type.Name()
		if strings.HasPrefix(name, "NoWaitFor[") || strings.HasPrefix(name, "StepDefaults[") {
			return true
		}
	}
	return false
}
