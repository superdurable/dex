// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestVersionCommandsCheckForUpdates(t *testing.T) {
	originalVersion := version
	version = "v0.1.0"
	t.Cleanup(func() {
		version = originalVersion
	})
	for _, testCase := range []struct {
		name             string
		arguments        []string
		wantsVersionLine bool
	}{
		{name: "version", arguments: []string{"version"}, wantsVersionLine: true},
		{name: "long flag", arguments: []string{"--version"}, wantsVersionLine: true},
		{name: "short flag", arguments: []string{"-v"}, wantsVersionLine: true},
		{name: "compatibility check", arguments: []string{"version", "check", "--timeout", "1ms"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			output, err := captureVersionCommandOutput(testCase.arguments)
			if testCase.wantsVersionLine && !strings.Contains(output, "dexcli v0.1.0") {
				t.Fatalf("version output = %q", output)
			}
			if !strings.Contains(output, "update notice for v0.1.0") {
				t.Fatalf("update notice output = %q", output)
			}
			if testCase.name != "compatibility check" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func captureVersionCommandOutput(arguments []string) (string, error) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		return "", err
	}
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	os.Stdout = writeFile
	os.Stderr = writeFile
	defer func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	}()
	err = runWithUpdateNotice(context.Background(), arguments, func(_ context.Context, output io.Writer, currentVersion string) {
		fmt.Fprintf(output, "update notice for %s\n", currentVersion)
	})
	if closeErr := writeFile.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	output, readErr := io.ReadAll(readFile)
	if closeErr := readFile.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	if err != nil {
		return string(output), err
	}
	return string(output), readErr
}
