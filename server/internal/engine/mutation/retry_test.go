package mutation

import (
	"strings"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStepRetryState_RejectsOversizedLastError(t *testing.T) {
	validateErr := validateStepRetryStatePB(&pb.StepRetryState{
		LastError: strings.Repeat("e", 5000),
	}, 2048)
	require.NotNil(t, validateErr)
	assert.True(t, validateErr.IsInvalidInputError())
}

func TestValidateStepRetryState_RejectsOversizedStackTrace(t *testing.T) {
	validateErr := validateStepRetryStatePB(&pb.StepRetryState{
		LastErrorStackTrace: strings.Repeat("s", 5000),
	}, 2048)
	require.NotNil(t, validateErr)
	assert.True(t, validateErr.IsInvalidInputError())
}

func TestConvertStepRetryState_AcceptsWithinLimit(t *testing.T) {
	state, err := convertStepRetryState(&pb.StepRetryState{
		FirstAttemptTimeMs:  time.Now().UnixMilli(),
		CurrentAttempts:     2,
		LastError:           "boom",
		LastErrorStackTrace: "goroutine 1 [running]:\nmain.main()",
	}, 2048)
	require.Nil(t, err)
	require.NotNil(t, state)
	assert.Equal(t, "boom", state.LastError)
	assert.Equal(t, "goroutine 1 [running]:\nmain.main()", state.LastErrorStackTrace)
}
