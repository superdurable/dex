package errors

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

var enablePreviousPathForLoggingCaller = atomic.Bool{}

func init() {
	enable := os.Getenv("EnablePreviousPathForLoggingCaller")
	if enable == "true" {
		enablePreviousPathForLoggingCaller.Store(true)
	}
}

var corePaths = []string{
	"/server/common",
	"/server/internal",
	"/server/cmd",
	"/dex/server/common",
	"/dex/server/internal",
	"/dex/server/cmd",
	"/dex/common",
	"/dex/utils",
	"/dex/cmd",
	"/server/common",
	"/server/internal",
	"/server/cmd",
	// docker image paths
	"/app/internal",
	"/app/cmd",
}

func SetEnablePreviousPathForLoggingCaller(enable bool) {
	enablePreviousPathForLoggingCaller.Store(enable)
}

// PathToCaller returns the go code path of calling this function.
// It's built on Golang's runtime.Caller which requires a skip parameter.
// skip is the "magic" number that for runtime.Caller -- (this is copied from its comment)
//
//	The argument skip is the number of stack frames
//	to ascend, with 0 identifying the caller of Caller. (For historical reasons the
//	meaning of skip differs between Caller and [Callers].)
func PathToCaller(skip int) string {
	_, path, lineno, lineOk := runtime.Caller(skip)
	path = shortenPathIfPossible(path)
	if !enablePreviousPathForLoggingCaller.Load() {
		return fmt.Sprintf("%v:%v", path, lineno)
	}

	_, prevPath, prevLineno, prevLineOk := runtime.Caller(skip + 1)
	prevPath = shortenPathIfPossible(prevPath)
	if !lineOk && !prevLineOk {
		return ""
	}
	return fmt.Sprintf("%v:%v", prevPath, prevLineno) + " -> " + fmt.Sprintf("%v:%v", path, lineno)
}

// shortenPathIfPossible shortens known paths to "**" prefix for readability
func shortenPathIfPossible(path string) string {
	for _, marker := range corePaths {
		idx := strings.Index(path, marker)
		if idx != -1 {
			remaining := path[idx+len(marker):]
			return "**" + remaining
		}
	}
	return path
}
