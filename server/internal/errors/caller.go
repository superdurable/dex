// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

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
