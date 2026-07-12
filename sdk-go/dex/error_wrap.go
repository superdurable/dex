package dex

import (
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

const maxMethodErrorUserFrames = 10

// ErrorWrap wraps err with step-method stacktrace for reporting.
// So that history & WebUI will show the stacktrace
func ErrorWrap(err error) error {
	if err == nil {
		return nil
	}
	var existing *methodError
	if errors.As(err, &existing) {
		return err
	}
	return &methodError{
		err:        err,
		userFrames: captureUserFrames(2, maxMethodErrorUserFrames),
	}
}

type methodError struct {
	err        error
	userFrames []runtime.Frame
}

func (methodErr *methodError) Error() string { return methodErr.err.Error() }
func (methodErr *methodError) Unwrap() error { return methodErr.err }

func methodErrorUserFrames(err error) []runtime.Frame {
	var wrapped *methodError
	if errors.As(err, &wrapped) {
		return wrapped.userFrames
	}
	return nil
}

func formatMethodFailureStack(err error, step stepCommon, kind stepTaskMethodKind, sdkFrame runtime.Frame) string {
	userFrames := methodErrorUserFrames(err)
	if len(userFrames) == 0 {
		methodName := "Execute"
		if kind == stepTaskMethodKindWaitFor {
			methodName = "WaitFor"
		}
		userFrames = []runtime.Frame{methodEntryFrame(step, methodName)}
	}
	allFrames := append(userFrames, sdkFrame)
	return joinStandardStackFrames(allFrames...)
}

func joinStandardStackFrames(frames ...runtime.Frame) string {
	var parts []string
	for _, frame := range frames {
		if line := formatStandardStackFrame(frame); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "\n")
}

func formatStandardStackFrame(frame runtime.Frame) string {
	if frame.PC == 0 {
		return ""
	}
	function := frame.Func
	if function == nil {
		function = runtime.FuncForPC(frame.PC)
	}
	if function == nil {
		return ""
	}
	file, line := frame.File, frame.Line
	if file == "" {
		file, line = function.FileLine(frame.PC)
	}
	functionName := frame.Function
	if functionName == "" {
		functionName = function.Name()
	}
	offset := frame.PC - function.Entry()
	return functionName + "\n\t" + file + ":" + strconv.Itoa(line) + " +0x" + strconv.FormatUint(uint64(offset), 16)
}

func captureCallerFrame(skip int) runtime.Frame {
	programCounters := make([]uintptr, 1)
	if runtime.Callers(skip+1, programCounters) == 0 {
		return runtime.Frame{}
	}
	frame, _ := runtime.CallersFrames(programCounters).Next()
	return frame
}

func methodEntryFrame(step stepCommon, methodName string) runtime.Frame {
	return receiverMethodEntryFrame(reflect.TypeOf(step), methodName)
}

func receiverMethodEntryFrame(receiverType reflect.Type, methodName string) runtime.Frame {
	method, ok := receiverType.MethodByName(methodName)
	if !ok {
		return runtime.Frame{}
	}
	programCounter := method.Func.Pointer()
	function := runtime.FuncForPC(programCounter)
	if function == nil {
		return runtime.Frame{}
	}
	file, line := function.FileLine(programCounter)
	return runtime.Frame{
		PC:       programCounter,
		Func:     function,
		Function: function.Name(),
		File:     file,
		Line:     line,
		Entry:    function.Entry(),
	}
}

func captureUserFrames(skip, maxFrames int) []runtime.Frame {
	if maxFrames <= 0 {
		return nil
	}
	programCounters := make([]uintptr, 64)
	count := runtime.Callers(skip+1, programCounters)
	frames := runtime.CallersFrames(programCounters[:count])
	var userFrames []runtime.Frame
	for {
		frame, more := frames.Next()
		if shouldStopUserFrameWalk(frame) {
			break
		}
		if shouldSkipUserFrame(frame) {
			if !more {
				break
			}
			continue
		}
		userFrames = append(userFrames, frame)
		if len(userFrames) >= maxFrames {
			break
		}
		if !more {
			break
		}
	}
	return userFrames
}

func shouldStopUserFrameWalk(frame runtime.Frame) bool {
	return isSDKInternalFrame(frame) || isTestRunnerFrame(frame)
}

func isTestRunnerFrame(frame runtime.Frame) bool {
	return strings.HasPrefix(frame.Function, "testing.")
}

func shouldSkipUserFrame(frame runtime.Frame) bool {
	if isInternalRuntimeFrame(frame) {
		return true
	}
	return strings.HasSuffix(frame.Function, ".ErrorWrap") ||
		strings.HasSuffix(frame.Function, ".captureUserFrames")
}

func isSDKInternalFrame(frame runtime.Frame) bool {
	if strings.HasSuffix(frame.File, "_test.go") {
		return false
	}
	return strings.Contains(frame.Function, "github.com/superdurable/dex/sdk-go/dex.") &&
		(strings.Contains(frame.Function, ".(*stepMethodRunner)") ||
			strings.HasSuffix(frame.Function, ".formatUserMethodFailureStack") ||
			strings.HasSuffix(frame.Function, ".executeStepReflect") ||
			strings.HasSuffix(frame.Function, ".executeWaitForReflect") ||
			strings.Contains(frame.Function, ".invokeOnce"))
}

func isInternalRuntimeFrame(frame runtime.Frame) bool {
	if strings.HasPrefix(frame.Function, "runtime.") ||
		strings.HasPrefix(frame.Function, "reflect.") {
		return true
	}
	return strings.HasPrefix(frame.File, "runtime/") || isReflectSourceFile(frame.File)
}

func isReflectSourceFile(file string) bool {
	return strings.HasPrefix(file, "reflect/") || strings.Contains(file, "/reflect/")
}
