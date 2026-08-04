// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

package integ

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type executeOnlyFlow struct {
	emptyFlowSchema
}

func (executeOnlyFlow) GetFlowType() string {
	return "go-sdk-execute-only"
}

func (executeOnlyFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(executeOnlyFirstStep{}),
		dex.DefineStep(executeOnlySecondStep{}),
	}
}

type executeOnlyValueFlow struct {
	emptyFlowSchema
}

func (executeOnlyValueFlow) GetFlowType() string {
	return "go-sdk-execute-only-value-flow"
}

func (executeOnlyValueFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(executeOnlyFirstStep{}),
		dex.DefineStep(executeOnlySecondStep{}),
	}
}

type executeOnlyFirstStep struct {
	dex.StepDefaults[int]
}

func (executeOnlyFirstStep) GetStepType() string {
	return "first"
}

func (executeOnlyFirstStep) Execute(
	_ dex.Context,
	input int,
) (dex.StepDecision, error) {
	return dex.GoTo(executeOnlySecondStep{}, input+1), nil
}

type executeOnlySecondStep struct {
	dex.StepDefaults[int]
}

func (executeOnlySecondStep) GetStepType() string {
	return "second"
}

func (executeOnlySecondStep) Execute(
	_ dex.Context,
	input int,
) (dex.StepDecision, error) {
	return dex.GracefulComplete(input + 1), nil
}

func TestExecuteOnlySteps(t *testing.T) {
	tests := []struct {
		name string
		flow dex.Flow
	}{
		{name: "pointer-style flow", flow: executeOnlyFlow{}},
		{name: "value-style flow", flow: executeOnlyValueFlow{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flowID := newFlowID(t, "execute-only")
			_, err := integClient.StartFlow(
				integrationContext(t),
				test.flow,
				flowID,
				1,
				dex.StartFlowOptions{},
			)
			require.NoError(t, err)
			result := waitForFlow(t, flowID, true)
			require.Equal(t, dex.FlowCompleted, result.Status)
			require.Len(t, result.Completions, 1)
			var output int
			require.NoError(t, result.Completions[0].Output.Decode(&output))
			require.Equal(t, 3, output)
		})
	}
}
