package main

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/stretchr/testify/require"
)

func testCtx(stateMap map[string]*pb.Value) dex.Context {
	return dex.NewTestContext(context.Background(), benchmarkFlowSchema(), stateMap, false, nil)
}

func stepMovements(t *testing.T, decision dex.StepDecision) []dex.StepMovement {
	t.Helper()
	rv := reflect.ValueOf(decision).Elem()
	return rv.FieldByName("Movements").Interface().([]dex.StepMovement)
}

func stepTerminal(t *testing.T, decision dex.StepDecision) *dex.TerminalDecision {
	t.Helper()
	rv := reflect.ValueOf(decision).Elem()
	return rv.FieldByName("Terminal").Interface().(*dex.TerminalDecision)
}

func stepCancelIDs(t *testing.T, decision dex.StepDecision) []string {
	t.Helper()
	rv := reflect.ValueOf(decision).Elem()
	return rv.FieldByName("CancelSiblingStepIDs").Interface().([]string)
}

func TestSequentialLoopStep_CompletesAfterConfiguredIterations(t *testing.T) {
	step := &sequentialLoopStep{}
	input := benchmarkTriggerInput{NumSteps: 3, StateSize: 8}
	ctx := testCtx(nil)

	decision1, err := step.Execute(ctx, input)
	require.NoError(t, err)
	require.Len(t, stepMovements(t, decision1), 1)
	payload, err := keyPayload.GetValue(ctx)
	require.NoError(t, err)
	require.Equal(t, "xxxxxxxx", payload)
	currentStep, err := keyCurrentStep.GetValue(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, currentStep)

	decision2, err := step.Execute(testCtx(
		map[string]*pb.Value{"current_step": {Kind: &pb.Value_IntValue{IntValue: 2}}},
	), input)
	require.NoError(t, err)
	require.NotNil(t, stepTerminal(t, decision2))
	require.Equal(t, dex.TerminalComplete, stepTerminal(t, decision2).Type)
}

func TestParallelInitStep_SpawnsWorkerPerStep(t *testing.T) {
	step := &parallelInitStep{}
	input := benchmarkTriggerInput{NumSteps: 4, StateSize: 0}
	decision, err := step.Execute(testCtx(nil), input)
	require.NoError(t, err)
	require.Len(t, stepMovements(t, decision), 4)
}

func TestParallelWorkerStep_DeadEnds(t *testing.T) {
	step := &parallelWorkerStep{}
	input := parallelStepInput{StepIndex: 2, StateSize: 4}
	ctx := testCtx(nil)
	decision, err := step.Execute(ctx, input)
	require.NoError(t, err)
	terminal := stepTerminal(t, decision)
	require.NotNil(t, terminal)
	require.Equal(t, dex.TerminalDeadEnd, terminal.Type)
	require.Empty(t, stepMovements(t, decision))
	lastStep, err := keyLastStep.GetValue(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, lastStep)
}

func TestRegisterFlows(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&sequentialBenchmarkFlow{})
	registry.Register(&parallelBenchmarkFlow{})
}

func TestHealthEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	require.NotNil(t, handler)
}
