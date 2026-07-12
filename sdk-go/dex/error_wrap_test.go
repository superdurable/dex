package dex

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type methodErrorTestStep struct {
	StepDefaults[any]
}

func (s *methodErrorTestStep) Execute(_ Context, _ any) (StepDecision, error) {
	return nil, ErrorWrap(fmt.Errorf("fail"))
}

func methodErrorTestHelper() error {
	return ErrorWrap(fmt.Errorf("boom"))
}

func methodErrorTestOuterHelper() error {
	return methodErrorTestHelper()
}

func TestMethodError_CapturesCallSiteStack(t *testing.T) {
	err := func() error {
		return ErrorWrap(fmt.Errorf("boom"))
	}()
	require.Error(t, err)
	frames := methodErrorUserFrames(err)
	require.NotEmpty(t, frames)
	stack := formatStandardStackFrame(frames[0])
	assert.Contains(t, stack, "error_wrap_test.go:")
	assert.Contains(t, stack, "+0x")
	assert.NotContains(t, stack, "<-")
	assert.NotContains(t, stack, "via sdk")
}

func TestMethodError_CapturesMultiLayerUserStack(t *testing.T) {
	err := methodErrorTestOuterHelper()
	require.Error(t, err)
	frames := methodErrorUserFrames(err)
	require.GreaterOrEqual(t, len(frames), 2)
	assert.Contains(t, frames[0].Function, "methodErrorTestHelper")
	assert.Contains(t, frames[1].Function, "methodErrorTestOuterHelper")
	stack := joinStandardStackFrames(frames...)
	assert.Contains(t, stack, "methodErrorTestHelper")
	assert.Contains(t, stack, "methodErrorTestOuterHelper")
}

func TestFormatMethodEntryStack(t *testing.T) {
	frame := methodEntryFrame(&methodErrorTestStep{}, "Execute")
	stack := formatStandardStackFrame(frame)
	assert.Contains(t, stack, "error_wrap_test.go:")
	assert.Contains(t, stack, ".Execute")
	assert.Contains(t, stack, "+0x")
}

func TestJoinStandardStackFrames_UserAndSDK(t *testing.T) {
	userFrame := methodEntryFrame(&methodErrorTestStep{}, "Execute")
	sdkFrame := methodEntryFrame(&methodErrorTestStep{}, "Execute")
	stack := joinStandardStackFrames(userFrame, sdkFrame)
	assert.Contains(t, stack, "+0x")
	lines := strings.Split(stack, "\n")
	assert.Len(t, lines, 4)
}

func TestFormatMethodFailureStack_PrefersMethodErrorStack(t *testing.T) {
	err := ErrorWrap(fmt.Errorf("fail"))
	frames := methodErrorUserFrames(err)
	require.NotEmpty(t, frames)
	stack := formatStandardStackFrame(frames[0])
	assert.True(t, strings.Contains(stack, "error_wrap_test.go:"))
}

func TestIsInternalRuntimeFrame_SkipsReflect(t *testing.T) {
	assert.True(t, isInternalRuntimeFrame(runtime.Frame{
		Function: "reflect.Value.call",
		File:     "reflect/value.go",
	}))
	assert.True(t, isInternalRuntimeFrame(runtime.Frame{
		Function: "reflect.Value.Call",
		File:     "/usr/local/go/src/reflect/value.go",
	}))
}
