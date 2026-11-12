// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package typedpaths reads the file paths a person types or pastes into the
// agents terminal: /image arguments, completion, and dropped files.
package typedpaths

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// ErrEmptyPath is returned for a path argument with no text.
var ErrEmptyPath = errors.New("the path is empty")

// errNoHomeDirectory is returned for a path that starts with ~ without HOME.
var errNoHomeDirectory = errors.New("~ needs HOME")

// maximumCompletions bounds the candidates one completion returns.
const maximumCompletions = 50

// imageExtensions are the file types a pasted path offers to attach.
var imageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// PathResolver turns typed text into absolute paths. Its zero value resolves
// relative paths against "/" and has no home directory.
type PathResolver struct {
	homeDirectory    string
	workingDirectory string
}

// NewPathResolver returns a resolver that expands ~ to homeDirectory and
// resolves relative paths against workingDirectory. Either may be empty.
func NewPathResolver(homeDirectory string, workingDirectory string) PathResolver {
	return PathResolver{homeDirectory: homeDirectory, workingDirectory: workingDirectory}
}

// ResolveTypedPath removes one level of matching quotes, expands a leading
// ~, and returns the cleaned absolute path. It returns ErrEmptyPath for
// blank text, and an error for ~ without a home directory.
func (resolver PathResolver) ResolveTypedPath(text string) (string, error) {
	path := strings.TrimSpace(text)
	if len(path) >= 2 && (path[0] == '"' || path[0] == '\'') && path[len(path)-1] == path[0] {
		path = path[1 : len(path)-1]
	}
	if path == "" {
		return "", ErrEmptyPath
	}
	return resolver.absolutePath(path)
}

// PastedImagePath returns the absolute path when text, as a terminal pastes
// a dropped file, names one existing image file.
func (resolver PathResolver) PastedImagePath(text string) (string, bool) {
	path := strings.TrimSpace(text)
	if path == "" || strings.ContainsAny(path, "\n\r") {
		return "", false
	}
	path = unquotePastedPath(path)
	if strings.HasPrefix(path, "file://") {
		parsed, err := url.Parse(path)
		if err != nil || (parsed.Host != "" && parsed.Host != "localhost") {
			return "", false
		}
		path = parsed.Path
	}
	if !imageExtensions[strings.ToLower(filepath.Ext(path))] {
		return "", false
	}
	absolute, err := resolver.absolutePath(path)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return absolute, true
}

// CompletePath returns the typed completions of partial, sorted, at most
// maximumCompletions. A directory completion ends with "/". The completions
// keep partial's form: a leading ~ or a relative path stays.
func (resolver PathResolver) CompletePath(partial string) []string {
	typedDirectory, namePrefix := splitTypedPath(partial)
	directory := typedDirectory
	if directory == "" {
		directory = "."
	}
	absoluteDirectory, err := resolver.absolutePath(directory)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(absoluteDirectory)
	if err != nil {
		return nil
	}
	isHiddenShown := strings.HasPrefix(namePrefix, ".")
	var completions []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, namePrefix) || (!isHiddenShown && strings.HasPrefix(name, ".")) {
			continue
		}
		completion := typedDirectory + name
		if resolver.isDirectory(filepath.Join(absoluteDirectory, name), entry) {
			completion += "/"
		}
		completions = append(completions, completion)
	}
	sort.Strings(completions)
	if len(completions) > maximumCompletions {
		completions = completions[:maximumCompletions]
	}
	return completions
}

// LongestCommonPrefix returns the longest prefix all completions share.
func LongestCommonPrefix(completions []string) string {
	if len(completions) == 0 {
		return ""
	}
	prefix := completions[0]
	for _, completion := range completions[1:] {
		for !strings.HasPrefix(completion, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	// A byte-wise prefix may end inside a multi-byte character.
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func (resolver PathResolver) isDirectory(path string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (resolver PathResolver) absolutePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if resolver.homeDirectory == "" {
			return "", errNoHomeDirectory
		}
		path = filepath.Join(resolver.homeDirectory, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		workingDirectory := resolver.workingDirectory
		if workingDirectory == "" {
			workingDirectory = string(filepath.Separator)
		}
		path = filepath.Join(workingDirectory, path)
	}
	return filepath.Clean(path), nil
}

// splitTypedPath splits partial after its last slash, keeping the slash
// with the directory.
func splitTypedPath(partial string) (string, string) {
	if partial == "~" {
		return "~/", ""
	}
	index := strings.LastIndex(partial, "/")
	if index < 0 {
		return "", partial
	}
	return partial[:index+1], partial[index+1:]
}

// unquotePastedPath removes one level of matching quotes, or the backslash
// escapes a terminal adds before spaces and other shell characters.
func unquotePastedPath(path string) string {
	if len(path) >= 2 && (path[0] == '"' || path[0] == '\'') && path[len(path)-1] == path[0] {
		return path[1 : len(path)-1]
	}
	if !strings.Contains(path, `\`) {
		return path
	}
	var builder strings.Builder
	isEscaped := false
	for _, character := range path {
		if character == '\\' && !isEscaped {
			isEscaped = true
			continue
		}
		isEscaped = false
		builder.WriteRune(character)
	}
	return builder.String()
}
