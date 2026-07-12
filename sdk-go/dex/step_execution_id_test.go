package dex

import "testing"

func TestStepIDFromStepExecutionID(t *testing.T) {
	tests := []struct {
		exeID  string
		stepID string
	}{
		{"", ""},
		{"ChargeStep-1", "ChargeStep"},
		{"my-step-42", "my-step"},
		{"NoCounterSuffix", "NoCounterSuffix"},
	}
	for _, tc := range tests {
		if got := StepIDFromStepExecutionID(tc.exeID); got != tc.stepID {
			t.Fatalf("StepIDFromStepExecutionID(%q) = %q, want %q", tc.exeID, got, tc.stepID)
		}
	}
}
