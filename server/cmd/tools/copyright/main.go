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

// Package main adds or verifies per-directory license headers.
//
// Templates and directory→template mapping live under script/licenseheaders/.
// -replace rewrites existing headers to the Super Durable template for that
// directory (destructive). Default mode only adds headers when missing.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type config struct {
	rootDir    string
	verifyOnly bool
	filePaths  string
}

type addLicenseHeaderTask struct {
	config       *config
	replaceAll   bool
	mapping      map[string]string // prefix → template id
	prefixes     []string          // longest first
	rawTemplates map[string]string // template id → plain text
}

const (
	headersDirName   = "script/licenseheaders"
	mappingFileName  = "mapping.yaml"
	defaultFilePerms = os.FileMode(0644)
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
}

func main() {
	var cfg config
	var replaceAll bool
	flag.StringVar(&cfg.rootDir, "rootDir", ".", "project root directory")
	flag.BoolVar(&cfg.verifyOnly, "verifyOnly", false,
		"don't automatically add headers, just verify all files")
	flag.BoolVar(&replaceAll, "replace", false,
		"replace existing license headers with the directory's Super Durable template (destructive)")
	flag.StringVar(&cfg.filePaths, "filePaths", "", "comma separated list of files to process")
	flag.Parse()

	task := &addLicenseHeaderTask{
		config:     &cfg,
		replaceAll: replaceAll,
	}
	if err := task.init(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := task.run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func (task *addLicenseHeaderTask) init() error {
	root, err := filepath.Abs(task.config.rootDir)
	if err != nil {
		return fmt.Errorf("resolve rootDir: %w", err)
	}
	task.config.rootDir = root

	headersDir := filepath.Join(root, headersDirName)
	mappingPath := filepath.Join(headersDir, mappingFileName)
	data, err := os.ReadFile(mappingPath)
	if err != nil {
		return fmt.Errorf("read mapping: %w", err)
	}
	mapping := map[string]string{}
	if err := yaml.Unmarshal(data, &mapping); err != nil {
		return fmt.Errorf("parse mapping: %w", err)
	}
	if len(mapping) == 0 {
		return fmt.Errorf("empty mapping in %s", mappingPath)
	}
	task.mapping = mapping
	task.prefixes = make([]string, 0, len(mapping))
	for prefix := range mapping {
		task.prefixes = append(task.prefixes, prefix)
	}
	sort.Slice(task.prefixes, func(i, j int) bool {
		return len(task.prefixes[i]) > len(task.prefixes[j])
	})

	task.rawTemplates = map[string]string{}
	for _, id := range mapping {
		if _, ok := task.rawTemplates[id]; ok {
			continue
		}
		path := filepath.Join(headersDir, id+".txt")
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read template %s: %w", id, err)
		}
		task.rawTemplates[id] = strings.TrimRight(string(raw), "\n") + "\n"
	}
	return nil
}

func (task *addLicenseHeaderTask) run() error {
	if task.config.filePaths != "" {
		paths := strings.Split(task.config.filePaths, ",")
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if err := task.handleFile(path, info, nil); err != nil {
				return err
			}
		}
		return nil
	}
	return filepath.Walk(task.config.rootDir, task.handleFile)
}

func (task *addLicenseHeaderTask) handleFile(path string, fileInfo os.FileInfo, err error) error {
	if err != nil {
		return err
	}
	if fileInfo.IsDir() {
		name := fileInfo.Name()
		if skipDirNames[name] || strings.HasPrefix(name, "_vendor-") {
			return filepath.SkipDir
		}
		// Skip generated trees and Java/Gradle build outputs.
		if name == "gen" || name == "dexpb" || name == "build" || name == "dist" || name == "target" {
			return filepath.SkipDir
		}
		return nil
	}
	if !mustProcessPath(path) {
		return nil
	}
	if !isSupportedSourceFile(path) {
		return nil
	}
	if isFileAutogenerated(path) {
		return nil
	}

	rel, err := filepath.Rel(task.config.rootDir, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	templateID, ok := task.templateForRel(rel)
	if !ok {
		return nil
	}
	rawTemplate := task.rawTemplates[templateID]
	header, err := formatHeader(rawTemplate, filepath.Ext(path))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body := string(data)
	hasHeader := hasLicenseHeader(body)
	if hasHeader && !task.replaceAll {
		return nil
	}
	if task.config.verifyOnly {
		if !hasHeader {
			return fmt.Errorf("%s missing license header", path)
		}
		return nil
	}

	shebang := ""
	rest := body
	if strings.HasPrefix(body, "#!") {
		nl := strings.IndexByte(body, '\n')
		if nl >= 0 {
			shebang = body[:nl+1]
			rest = body[nl+1:]
		} else {
			shebang = body + "\n"
			rest = ""
		}
	}
	if hasHeader {
		rest = stripLicenseHeader(rest)
	}
	return os.WriteFile(path, []byte(shebang+header+rest), defaultFilePerms)
}

func (task *addLicenseHeaderTask) templateForRel(rel string) (string, bool) {
	for _, prefix := range task.prefixes {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return task.mapping[prefix], true
		}
	}
	return "", false
}

func isSupportedSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".java", ".py", ".rs":
		return true
	case ".yaml", ".yml":
		// Hand-written OpenAPI IDL under protos/ only.
		slash := filepath.ToSlash(path)
		return strings.Contains(slash, "/protos/") || strings.HasSuffix(slash, "/protos")
	default:
		return false
	}
}

func isFileAutogenerated(path string) bool {
	base := filepath.Base(path)
	slash := filepath.ToSlash(path)
	return strings.Contains(base, ".gen.") ||
		strings.HasSuffix(base, "_pb.go") ||
		strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_pb2.py") ||
		strings.HasSuffix(base, "_pb2_grpc.py") ||
		strings.HasSuffix(base, "_pb2.pyi") ||
		strings.Contains(slash, "/gen/") ||
		strings.Contains(slash, "/dexpb/") ||
		strings.Contains(slash, "/.openapi-generator/")
}

func mustProcessPath(path string) bool {
	slash := filepath.ToSlash(path)
	denylist := []string{
		"/vendor/",
		"/target/",
		"/node_modules/",
		"/__pycache__/",
	}
	for _, d := range denylist {
		if strings.Contains(slash, d) {
			return false
		}
	}
	return true
}

func formatHeader(raw string, ext string) (string, error) {
	lines := splitLines(raw)
	switch strings.ToLower(ext) {
	case ".go", ".rs":
		return commentLines(lines, "//"), nil
	case ".py", ".yaml", ".yml":
		return commentLines(lines, "#"), nil
	case ".java":
		return blockComment(lines), nil
	default:
		return "", fmt.Errorf("unsupported extension %q", ext)
	}
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func commentLines(lines []string, prefix string) string {
	var b strings.Builder
	for _, line := range lines {
		if line == "" {
			b.WriteString(prefix + "\n")
		} else {
			b.WriteString(prefix + " " + line + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

func blockComment(lines []string) string {
	var b strings.Builder
	b.WriteString("/*\n")
	for _, line := range lines {
		if line == "" {
			b.WriteString(" *\n")
		} else {
			b.WriteString(" * " + line + "\n")
		}
	}
	b.WriteString(" */\n\n")
	return b.String()
}

func hasLicenseHeader(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for i := 0; i < 40 && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "/*" || line == "*/" || line == "*" || line == "//" || line == "#" {
			continue
		}
		// Strip common comment prefixes before matching.
		check := line
		for _, p := range []string{"//", "#", "*", "/*", "*/"} {
			if strings.HasPrefix(check, p) {
				check = strings.TrimSpace(strings.TrimPrefix(check, p))
			}
		}
		if isLicenseHeaderLine(check) || isLicenseHeaderLine(line) {
			return true
		}
		// Stop once we hit non-comment content.
		if !isCommentOrEmpty(line) {
			return false
		}
	}
	return false
}

func isCommentOrEmpty(line string) bool {
	if line == "" {
		return true
	}
	return strings.HasPrefix(line, "//") ||
		strings.HasPrefix(line, "#") ||
		strings.HasPrefix(line, "/*") ||
		strings.HasPrefix(line, "*") ||
		line == "*/"
}

func isLicenseHeaderLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "copyright") ||
		strings.Contains(lower, "spdx-license-identifier") ||
		strings.Contains(lower, "licensed under the apache") ||
		strings.Contains(lower, "permission is hereby granted") ||
		strings.Contains(lower, "super durable, inc") ||
		strings.Contains(lower, "dual-licensed")
}

func stripLicenseHeader(content string) string {
	lines := strings.Split(content, "\n")
	i := 0
	inBlock := false
	for i < len(lines) && i < 80 {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if strings.HasPrefix(line, "/*") {
			inBlock = true
			i++
			if strings.Contains(line, "*/") {
				inBlock = false
			}
			continue
		}
		if inBlock {
			i++
			if strings.Contains(line, "*/") {
				inBlock = false
			}
			continue
		}
		if isLicenseHeaderLine(stripCommentPrefix(line)) || line == "//" || line == "#" {
			i++
			continue
		}
		if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			// Contiguous leading comment block treated as header when replacing.
			i++
			continue
		}
		break
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return ""
	}
	return strings.Join(lines[i:], "\n")
}

func stripCommentPrefix(line string) string {
	check := strings.TrimSpace(line)
	for _, p := range []string{"//", "#", "*", "/*", "*/"} {
		if strings.HasPrefix(check, p) {
			check = strings.TrimSpace(strings.TrimPrefix(check, p))
		}
	}
	return check
}
