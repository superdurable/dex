package caller

import (
	"strings"
	"testing"
)

func TestPathToCaller(t *testing.T) {
	result := callPathToCaller(1)

	if result == "" {
		t.Fatal("PathToCaller returned empty string")
	}

	if strings.Contains(result, " -> ") {
		t.Errorf("PathToCaller result should NOT contain ' -> ' by default, got: %s", result)
	}

	if !strings.Contains(result, ":") {
		t.Errorf("PathToCaller result should contain line numbers, got: %s", result)
	}

	if strings.Contains(result, "/dex/utils") {
		t.Errorf("PathToCaller should shorten paths with /dex/utils, got: %s", result)
	}

	if !strings.Contains(result, "**/") {
		t.Errorf("PathToCaller should contain shortened path markers (**), got: %s", result)
	}

	if !strings.Contains(result, "path_test.go") {
		t.Errorf("PathToCaller should contain 'path_test.go', got: %s", result)
	}

	t.Logf("PathToCaller output (default): %s", result)
}

func TestPathToCallerWithPreviousPath(t *testing.T) {
	SetEnablePreviousPathForLoggingCaller(true)
	defer SetEnablePreviousPathForLoggingCaller(false)

	result := callPathToCaller(1)

	if result == "" {
		t.Fatal("PathToCaller returned empty string")
	}

	if !strings.Contains(result, " -> ") {
		t.Errorf("PathToCaller result should contain ' -> ' when enabled, got: %s", result)
	}

	if !strings.Contains(result, ":") {
		t.Errorf("PathToCaller result should contain line numbers, got: %s", result)
	}

	if !strings.Contains(result, "**/") {
		t.Errorf("PathToCaller should contain shortened path markers (**), got: %s", result)
	}

	if !strings.Contains(result, "path_test.go") {
		t.Errorf("PathToCaller should contain 'path_test.go', got: %s", result)
	}

	t.Logf("PathToCaller output (with previous path): %s", result)
}

func callPathToCaller(skip int) string {
	return PathToCaller(skip)
}

func TestPathToCallerNested(t *testing.T) {
	result := level1()

	if result == "" {
		t.Fatal("PathToCaller returned empty string")
	}

	if strings.Contains(result, " -> ") {
		t.Errorf("PathToCaller result should NOT contain ' -> ' by default, got: %s", result)
	}

	if !strings.Contains(result, ":") {
		t.Errorf("PathToCaller result should contain line numbers, got: %s", result)
	}

	t.Logf("Nested PathToCaller output (default): %s", result)
}

func TestPathToCallerNestedWithPreviousPath(t *testing.T) {
	SetEnablePreviousPathForLoggingCaller(true)
	defer SetEnablePreviousPathForLoggingCaller(false)

	result := level1WithPrevious()

	if result == "" {
		t.Fatal("PathToCaller returned empty string")
	}

	if !strings.Contains(result, " -> ") {
		t.Errorf("PathToCaller result should contain ' -> ' when enabled, got: %s", result)
	}

	if !strings.Contains(result, ":") {
		t.Errorf("PathToCaller result should contain line numbers, got: %s", result)
	}

	t.Logf("Nested PathToCaller output (with previous path): %s", result)
}

func level1() string {
	return level2()
}

func level2() string {
	return level3()
}

func level3() string {
	return PathToCaller(1)
}

func level1WithPrevious() string {
	return level2WithPrevious()
}

func level2WithPrevious() string {
	return level3WithPrevious()
}

func level3WithPrevious() string {
	return PathToCaller(1)
}

func TestShortenPathIfPossible(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "path with /dex/common marker",
			input:    "/root/mydir/dex/common/errors/errors.go",
			expected: "**/errors/errors.go",
		},
		{
			name:     "path with /dex/utils marker",
			input:    "/root/mydir/dex/utils/backoff/retry.go",
			expected: "**/backoff/retry.go",
		},
		{
			name:     "path with /dex/cmd marker",
			input:    "/root/mydir/dex/cmd/server/main.go",
			expected: "**/server/main.go",
		},
		{
			name:     "path without marker",
			input:    "/some/other/path/file.go",
			expected: "/some/other/path/file.go",
		},
		{
			name:     "path with multiple levels after marker",
			input:    "/usr/local/project/dex/utils/caller/path.go",
			expected: "**/caller/path.go",
		},
		{
			name:     "path with marker at beginning",
			input:    "/dex/common/log/logger.go",
			expected: "**/log/logger.go",
		},
		{
			name:     "empty path",
			input:    "",
			expected: "",
		},
		{
			name:     "path with just /dex/common",
			input:    "/some/path/dex/common",
			expected: "**",
		},
		{
			name:     "path with just /dex/cmd",
			input:    "/some/path/dex/cmd",
			expected: "**",
		},
		{
			name:     "path with trailing slash",
			input:    "/project/dex/common/log/",
			expected: "**/log/",
		},
		{
			name:     "path with /app/internal docker marker",
			input:    "/app/internal/persistence/store.go",
			expected: "**/persistence/store.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shortenPathIfPossible(tt.input)
			if result != tt.expected {
				t.Errorf("shortenPathIfPossible(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
