// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

// Package main adds and verifies repository license headers.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type config struct {
	rootDir    string
	verifyOnly bool
	filePaths  string
}

type policy struct {
	Cutoff                  string            `json:"cutoff"`
	IncludedPrefixes        []string          `json:"included_prefixes"`
	ForcedNewPrefixes       []string          `json:"forced_new_prefixes"`
	ThirdPartyMixedPrefixes []string          `json:"third_party_mixed_prefixes"`
	ReviewedMixedPaths      []string          `json:"reviewed_mixed_paths"`
	ExcludedPrefixes        []string          `json:"excluded_prefixes"`
	RetainedLicensePrefixes map[string]string `json:"retained_license_prefixes"`
}

type manifest struct {
	Cutoff  string                   `json:"cutoff"`
	Entries map[string]manifestEntry `json:"entries"`
}

type manifestEntry struct {
	Classification string `json:"classification"`
	BaselineHash   string `json:"baseline_hash"`
}

type headerTask struct {
	config        *config
	policy        *policy
	manifest      *manifest
	templates     map[string]string
	renameSources map[string]lineageSource
}

type lineageSource struct {
	path string
	kind string
}

type expectedHeader struct {
	classification string
	templateID     string
}

const (
	headersDirName   = "script/licenseheaders"
	policyFileName   = "policy.json"
	manifestFileName = "legacy-manifest.json"
)

var skipDirNames = map[string]bool{
	".git":         true,
	".bin":         true,
	".build":       true,
	".tools":       true,
	"vendor":       true,
	"target":       true,
	"node_modules": true,
	"__pycache__":  true,
	".idea":        true,
	".vscode":      true,
	"gen":          true,
	"dexpb":        true,
	"build":        true,
	"dist":         true,
}

var headerMarkers = []string{
	"copyright",
	"licensed under",
	"permission is hereby granted",
	"spdx-license-identifier",
	"legacy materials",
	"modifications after the legacy cutoff",
	"dual-licensed",
}

func main() {
	var taskConfig config
	flag.StringVar(&taskConfig.rootDir, "rootDir", ".", "project root directory")
	flag.BoolVar(&taskConfig.verifyOnly, "verifyOnly", false, "verify without changing files")
	flag.StringVar(&taskConfig.filePaths, "filePaths", "", "comma-separated files to process")
	flag.Parse()

	task, err := newHeaderTask(&taskConfig)
	if err == nil {
		err = task.run()
	}
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func newHeaderTask(taskConfig *config) (*headerTask, error) {
	rootDir, err := filepath.Abs(taskConfig.rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve rootDir: %w", err)
	}
	taskConfig.rootDir = rootDir
	task := &headerTask{
		config:    taskConfig,
		policy:    &policy{},
		manifest:  &manifest{},
		templates: map[string]string{},
	}
	if err := readJSON(filepath.Join(rootDir, headersDirName, policyFileName), task.policy); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(rootDir, headersDirName, manifestFileName), task.manifest); err != nil {
		return nil, err
	}
	if task.policy.Cutoff != task.manifest.Cutoff {
		return nil, fmt.Errorf("policy cutoff %s differs from manifest cutoff %s", task.policy.Cutoff, task.manifest.Cutoff)
	}
	for _, templateID := range []string{"new", "mixed", "third-party-mixed", "legacy-reference", "mit", "apache-2.0"} {
		fileName := templateID + ".txt"
		if templateID == "new" {
			fileName = "super-durable-1.0.txt"
		}
		content, err := os.ReadFile(filepath.Join(rootDir, headersDirName, fileName))
		if err != nil {
			return nil, fmt.Errorf("read template %s: %w", templateID, err)
		}
		task.templates[templateID] = strings.TrimRight(string(content), "\n")
	}
	renameSources, err := task.findRenameSources()
	if err != nil {
		return nil, err
	}
	task.renameSources = renameSources
	return task, nil
}

func readJSON(path string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func (task *headerTask) findRenameSources() (map[string]lineageSource, error) {
	result := map[string]lineageSource{}
	for _, arguments := range [][]string{
		{"diff", "--name-status", "-z", "-M20%", "-C20%", "--find-copies-harder", task.policy.Cutoff, "HEAD"},
		{"diff", "--name-status", "-z", "-M20%", "-C20%", "--find-copies-harder", "HEAD"},
	} {
		output, err := task.git(arguments...)
		if err != nil {
			return nil, err
		}
		fields := strings.Split(string(output), "\x00")
		for index := 0; index < len(fields) && fields[index] != ""; {
			status := fields[index]
			index++
			if index >= len(fields) {
				break
			}
			sourcePath := fields[index]
			index++
			if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
				if index >= len(fields) {
					break
				}
				destinationPath := fields[index]
				index++
				kind := "copy"
				if strings.HasPrefix(status, "R") {
					kind = "rename"
				}
				result[destinationPath] = lineageSource{path: sourcePath, kind: kind}
			}
		}
	}
	return result, nil
}

func (task *headerTask) run() error {
	if task.config.filePaths != "" {
		for _, filePath := range strings.Split(task.config.filePaths, ",") {
			filePath = strings.TrimSpace(filePath)
			if filePath == "" {
				continue
			}
			if !filepath.IsAbs(filePath) {
				filePath = filepath.Join(task.config.rootDir, filePath)
			}
			fileInfo, err := os.Stat(filePath)
			if err != nil {
				return err
			}
			if err := task.handleFile(filePath, fileInfo, nil); err != nil {
				return err
			}
		}
		return nil
	}
	return filepath.Walk(task.config.rootDir, task.handleFile)
}

func (task *headerTask) handleFile(path string, fileInfo fs.FileInfo, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if fileInfo.IsDir() {
		if skipDirNames[fileInfo.Name()] || strings.HasPrefix(fileInfo.Name(), "_vendor-") {
			return filepath.SkipDir
		}
		return nil
	}
	relativePath, err := filepath.Rel(task.config.rootDir, path)
	if err != nil {
		return err
	}
	relativePath = filepath.ToSlash(relativePath)
	if !isSupportedSourceFile(relativePath) || isFileAutogenerated(relativePath) {
		return nil
	}
	expected, managed, err := task.expectedHeaderFor(relativePath, path)
	if err != nil || !managed {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := task.validate(relativePath, data, expected); err == nil {
		return nil
	} else if task.config.verifyOnly {
		return err
	}
	updated, err := task.render(relativePath, data, expected)
	if err != nil {
		return err
	}
	if err := atomicWrite(path, updated, fileInfo.Mode()); err != nil {
		return err
	}
	if err := task.validate(relativePath, updated, expected); err != nil {
		return err
	}
	return nil
}

func isSupportedSourceFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".java", ".proto", ".py", ".rs":
		return true
	case ".ts", ".tsx":
		return hasPathPrefix(path, "web") || hasPathPrefix(path, "sdk-typescript")
	case ".css", ".html":
		return hasPathPrefix(path, "web")
	case ".yaml", ".yml":
		return hasPathPrefix(path, "protos")
	default:
		return false
	}
}

func isFileAutogenerated(path string) bool {
	baseName := filepath.Base(path)
	slashPath := filepath.ToSlash(path)
	return strings.Contains(baseName, ".gen.") ||
		strings.HasSuffix(baseName, "_pb.go") ||
		strings.HasSuffix(baseName, ".pb.go") ||
		strings.HasSuffix(baseName, "_pb2.py") ||
		strings.HasSuffix(baseName, "_pb2_grpc.py") ||
		strings.HasSuffix(baseName, "_pb2.pyi") ||
		strings.Contains(slashPath, "/gen/") ||
		strings.Contains(slashPath, "/dexpb/") ||
		strings.Contains(slashPath, "/.openapi-generator/")
}

func (task *headerTask) expectedHeaderFor(relativePath string, absolutePath string) (expectedHeader, bool, error) {
	if templateID, ok := longestPrefixValue(relativePath, task.policy.RetainedLicensePrefixes); ok {
		return expectedHeader{classification: "retained", templateID: templateID}, true, nil
	}
	if hasAnyPrefix(relativePath, task.policy.ExcludedPrefixes) {
		return expectedHeader{}, false, nil
	}
	if !hasAnyPrefix(relativePath, task.policy.IncludedPrefixes) {
		return expectedHeader{}, false, nil
	}
	if hasAnyPrefix(relativePath, task.policy.ThirdPartyMixedPrefixes) {
		return expectedHeader{classification: "third-party-mixed", templateID: "third-party-mixed"}, true, nil
	}
	if hasAnyPrefix(relativePath, task.policy.ForcedNewPrefixes) {
		return expectedHeader{classification: "new", templateID: "new"}, true, nil
	}
	if containsPath(task.policy.ReviewedMixedPaths, relativePath) {
		return expectedHeader{classification: "mixed", templateID: "mixed"}, true, nil
	}
	entry, ok := task.manifest.Entries[relativePath]
	if !ok {
		if source, sourceFound := task.renameSources[relativePath]; sourceFound {
			entry, ok = task.manifest.Entries[source.path]
			if ok && source.kind == "copy" && entry.Classification != "new" {
				return expectedHeader{classification: "mixed", templateID: "mixed"}, true, nil
			}
		}
	}
	if ok {
		if entry.Classification == "legacy-only" {
			data, err := os.ReadFile(absolutePath)
			if err != nil {
				return expectedHeader{}, false, err
			}
			if hashBytes(task.normalizedBody(data, filepath.Ext(relativePath))) != entry.BaselineHash {
				return expectedHeader{classification: "mixed", templateID: "mixed"}, true, nil
			}
		}
		return expectedHeader{classification: entry.Classification, templateID: entry.Classification}, true, nil
	}
	hasLegacy, err := task.hasLegacyBoundary(relativePath)
	if err != nil {
		return expectedHeader{}, false, err
	}
	if hasLegacy {
		return expectedHeader{classification: "mixed", templateID: "mixed"}, true, nil
	}
	return expectedHeader{classification: "new", templateID: "new"}, true, nil
}

func longestPrefixValue(path string, values map[string]string) (string, bool) {
	prefixes := make([]string, 0, len(values))
	for prefix := range values {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(left int, right int) bool {
		return len(prefixes[left]) > len(prefixes[right])
	})
	for _, prefix := range prefixes {
		if hasPathPrefix(path, prefix) {
			return values[prefix], true
		}
	}
	return "", false
}

func hasAnyPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if hasPathPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func hasPathPrefix(path string, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/")
}

func (task *headerTask) hasLegacyBoundary(relativePath string) (bool, error) {
	output, err := task.git(
		"blame",
		"-C",
		"-C",
		"-C",
		"--line-porcelain",
		task.policy.Cutoff+"..HEAD",
		"--",
		relativePath,
	)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(task.config.rootDir, filepath.FromSlash(relativePath)))
	if err != nil {
		return false, err
	}
	header, _ := splitHeader(splitShebang(string(data)).body, filepath.Ext(relativePath))
	headerLineCount := len(strings.Split(header, "\n"))
	boundary := false
	currentLine := 0
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && len(fields[0]) == 40 {
			parsedLine, parseErr := strconv.Atoi(fields[2])
			if parseErr == nil {
				currentLine = parsedLine
			}
		} else if line == "boundary" {
			boundary = true
		} else if boundary && strings.HasPrefix(line, "filename ") {
			if currentLine > headerLineCount {
				return true, nil
			}
			boundary = false
		}
	}
	return false, nil
}

func (task *headerTask) validate(relativePath string, data []byte, expected expectedHeader) error {
	extension := filepath.Ext(relativePath)
	parts := splitShebang(string(data))
	header, _ := splitHeader(parts.body, extension)
	newHeader := formatHeader(task.templates["new"], extension)
	mixedHeader := formatHeader(task.templates["mixed"], extension)
	thirdPartyMixedHeader := formatHeader(task.templates["third-party-mixed"], extension)
	legacyReference := formatHeader(task.templates["legacy-reference"], extension)
	switch expected.classification {
	case "new":
		if strings.TrimSpace(header) != strings.TrimSpace(newHeader) {
			return fmt.Errorf("%s must use the new Super Durable header", relativePath)
		}
	case "mixed":
		if !strings.Contains(header, mixedHeader) {
			return fmt.Errorf("%s must use the mixed header", relativePath)
		}
		legacyHeader := strings.TrimSpace(strings.ReplaceAll(header, mixedHeader, ""))
		if legacyHeader == "" {
			return fmt.Errorf("%s mixed header is missing its legacy notice", relativePath)
		}
	case "third-party-mixed":
		if strings.TrimSpace(header) != strings.TrimSpace(thirdPartyMixedHeader) {
			return fmt.Errorf("%s must use the third-party mixed header", relativePath)
		}
	case "legacy-only":
		if strings.Contains(header, newHeader) || strings.Contains(header, mixedHeader) {
			return fmt.Errorf("%s must remain legacy-only", relativePath)
		}
		if strings.TrimSpace(header) == "" {
			return fmt.Errorf("%s is missing its legacy header", relativePath)
		}
	case "retained":
		marker := "Permission is hereby granted"
		if expected.templateID == "apache-2.0" {
			marker = "Licensed under the Apache License"
		}
		if !strings.Contains(header, marker) {
			return fmt.Errorf("%s must retain its %s header", relativePath, expected.templateID)
		}
	default:
		return fmt.Errorf("%s has unknown classification %q", relativePath, expected.classification)
	}
	if strings.Contains(header, legacyReference) && expected.classification == "new" {
		return fmt.Errorf("%s new header contains a legacy notice", relativePath)
	}
	return nil
}

func (task *headerTask) render(relativePath string, data []byte, expected expectedHeader) ([]byte, error) {
	extension := filepath.Ext(relativePath)
	parts := splitShebang(string(data))
	cleaned := task.stripManagedBlocks(parts.body, extension)
	preservedHeader, body := splitHeader(cleaned, extension)
	var headers []string
	switch expected.classification {
	case "new":
		headers = []string{formatHeader(task.templates["new"], extension)}
	case "mixed":
		if strings.TrimSpace(preservedHeader) == "" {
			preservedHeader = formatHeader(task.templates["legacy-reference"], extension)
		}
		headers = []string{strings.TrimSpace(preservedHeader), formatHeader(task.templates["mixed"], extension)}
	case "third-party-mixed":
		headers = []string{formatHeader(task.templates["third-party-mixed"], extension)}
	case "legacy-only":
		if strings.TrimSpace(preservedHeader) == "" {
			preservedHeader = formatHeader(task.templates["legacy-reference"], extension)
		}
		headers = []string{strings.TrimSpace(preservedHeader)}
	case "retained":
		if strings.TrimSpace(preservedHeader) != "" {
			return nil, fmt.Errorf("%s has a non-%s header; refusing to replace it", relativePath, expected.templateID)
		}
		headers = []string{formatHeader(task.templates[expected.templateID], extension)}
	default:
		return nil, fmt.Errorf("%s has unknown classification %q", relativePath, expected.classification)
	}
	body = strings.TrimLeft(body, "\n")
	separator := "\n\n"
	if body == "" {
		separator = "\n"
	}
	rendered := parts.shebang + strings.Join(headers, "\n\n") + separator + body
	return []byte(rendered), nil
}

func (task *headerTask) normalizedBody(data []byte, extension string) []byte {
	parts := splitShebang(string(data))
	_, body := splitHeader(parts.body, extension)
	body = task.stripManagedBlocks(body, extension)
	return []byte(strings.TrimLeft(body, "\n"))
}

func (task *headerTask) stripManagedBlocks(content string, extension string) string {
	result := content
	for _, templateID := range []string{"new", "mixed", "third-party-mixed", "legacy-reference"} {
		header := formatHeader(task.templates[templateID], extension)
		result = strings.ReplaceAll(result, "\n\n"+header+"\n\n", "\n")
		result = strings.ReplaceAll(result, header, "")
	}
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}

type shebangParts struct {
	shebang string
	body    string
}

func splitShebang(content string) shebangParts {
	if !strings.HasPrefix(content, "#!") {
		return shebangParts{body: content}
	}
	newline := strings.IndexByte(content, '\n')
	if newline < 0 {
		return shebangParts{shebang: content + "\n"}
	}
	return shebangParts{shebang: content[:newline+1], body: content[newline+1:]}
}

func splitHeader(content string, extension string) (string, string) {
	if extension == ".css" || extension == ".java" {
		return splitBlockHeader(content)
	}
	if extension == ".html" {
		return splitHTMLHeader(content)
	}
	prefix := "//"
	if extension == ".py" || extension == ".yaml" || extension == ".yml" {
		prefix = "#"
	}
	lines := strings.SplitAfter(content, "\n")
	index := 0
	var headers []string
	for {
		for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
			index++
		}
		start := index
		var candidate strings.Builder
		for index < len(lines) && strings.HasPrefix(strings.TrimLeft(lines[index], " \t"), prefix) {
			candidate.WriteString(lines[index])
			index++
		}
		candidateText := strings.TrimRight(candidate.String(), "\n")
		if candidateText == "" || !isHeaderCandidate(candidateText) {
			if len(headers) > 0 {
				return strings.Join(headers, "\n\n"), strings.Join(lines[start:], "")
			}
			return "", content
		}
		headers = append(headers, candidateText)
	}
}

func splitBlockHeader(content string) (string, string) {
	offset := 0
	var headers []string
	for {
		for offset < len(content) && strings.ContainsRune(" \t\r\n", rune(content[offset])) {
			offset++
		}
		if !strings.HasPrefix(content[offset:], "/*") {
			break
		}
		end := strings.Index(content[offset:], "*/")
		if end < 0 {
			break
		}
		end += offset + 2
		candidate := content[offset:end]
		if !isHeaderCandidate(candidate) {
			break
		}
		headers = append(headers, candidate)
		offset = end
	}
	if len(headers) == 0 {
		return "", content
	}
	return strings.Join(headers, "\n\n"), content[offset:]
}

func splitHTMLHeader(content string) (string, string) {
	offset := 0
	var headers []string
	for {
		for offset < len(content) && strings.ContainsRune(" \t\r\n", rune(content[offset])) {
			offset++
		}
		if !strings.HasPrefix(content[offset:], "<!--") {
			break
		}
		end := strings.Index(content[offset:], "-->")
		if end < 0 {
			break
		}
		end += offset + 3
		candidate := content[offset:end]
		if !isHeaderCandidate(candidate) {
			break
		}
		headers = append(headers, candidate)
		offset = end
	}
	if len(headers) == 0 {
		return "", content
	}
	return strings.Join(headers, "\n\n"), content[offset:]
}

func isHeaderCandidate(content string) bool {
	lowerContent := strings.ToLower(content)
	for _, marker := range headerMarkers {
		if strings.Contains(lowerContent, marker) {
			return true
		}
	}
	return false
}

func formatHeader(plain string, extension string) string {
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if extension == ".css" || extension == ".java" {
		formatted := []string{"/*"}
		for _, line := range lines {
			if line == "" {
				formatted = append(formatted, " *")
			} else {
				formatted = append(formatted, " * "+line)
			}
		}
		return strings.Join(append(formatted, " */"), "\n")
	}
	if extension == ".html" {
		formatted := []string{"<!--"}
		formatted = append(formatted, lines...)
		return strings.Join(append(formatted, "-->"), "\n")
	}
	prefix := "//"
	if extension == ".py" || extension == ".yaml" || extension == ".yml" {
		prefix = "#"
	}
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			formatted = append(formatted, prefix)
		} else {
			formatted = append(formatted, prefix+" "+line)
		}
	}
	return strings.Join(formatted, "\n")
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func atomicWrite(path string, content []byte, mode fs.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".copyright-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "remove temporary file: %v\n", removeErr)
		}
	}()
	if _, err := temporary.Write(content); err != nil {
		if closeErr := temporary.Close(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		if closeErr := temporary.Close(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func (task *headerTask) git(arguments ...string) ([]byte, error) {
	command := exec.Command("git", arguments...)
	command.Dir = task.config.rootDir
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(arguments, " "), err)
	}
	return output, nil
}
