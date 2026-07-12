package dex

import (
	"strconv"
	"strings"
)

// StepIDFromStepExecutionID parses "{stepID}-{counter}" into stepID.
func StepIDFromStepExecutionID(stepExecutionID string) string {
	if stepExecutionID == "" {
		return ""
	}
	lastDash := strings.LastIndex(stepExecutionID, "-")
	if lastDash <= 0 || lastDash >= len(stepExecutionID)-1 {
		return stepExecutionID
	}
	suffix := stepExecutionID[lastDash+1:]
	if _, err := strconv.Atoi(suffix); err != nil {
		return stepExecutionID
	}
	return stepExecutionID[:lastDash]
}
