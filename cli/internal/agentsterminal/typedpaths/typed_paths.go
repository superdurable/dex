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
	"io"
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

const (
	// maximumCompletions bounds the candidates one completion returns.
	maximumCompletions = 50
	// MaximumScannedEntries bounds the directory entries one completion reads.
	MaximumScannedEntries = 2000
	// directoryReadBatch is how many entries one directory read returns.
	directoryReadBatch = 256
)

// imageExtensions are the file types a pasted path offers to attach.
var imageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// PathResolver turns typed text into absolute paths. Its zero value has no
// home directory.
type PathResolver struct {
	homeDirectory    string
	workingDirectory string
}

// NewPathResolver returns a resolver that expands ~ to homeDirectory and
// resolves relative paths against workingDirectory. Either may be empty.
func NewPathResolver(homeDirectory string, workingDirectory string) PathResolver {
	return PathResolver{homeDirectory: homeDirectory, workingDirectory: workingDirectory}
}

// ResolveTypedPath reads text as one path, the way a shell reads one word:
// quotes group, and a backslash outside single quotes escapes the next
// character. It expands a leading ~ and returns the cleaned absolute path.
// It returns ErrEmptyPath for blank text, and an error for ~ without a home
// directory.
func (resolver PathResolver) ResolveTypedPath(text string) (string, error) {
	path := lexTypedPath(text).value
	if path == "" {
		return "", ErrEmptyPath
	}
	return resolver.absolutePath(path)
}

// PastedImagePath returns the absolute path when text, as a terminal pastes
// a dropped file, names one existing image file.
func (resolver PathResolver) PastedImagePath(text string) (string, bool) {
	if strings.ContainsAny(strings.TrimSpace(text), "\n\r") {
		return "", false
	}
	path := lexTypedPath(text).value
	if strings.HasPrefix(path, "file://") {
		parsed, err := url.Parse(path)
		if err != nil || (parsed.Host != "" && parsed.Host != "localhost") {
			return "", false
		}
		path = parsed.Path
	}
	if path == "" || !imageExtensions[strings.ToLower(filepath.Ext(path))] {
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

// PathCompletions are the candidates of one completion.
type PathCompletions struct {
	// Candidates are sorted, written in the typed text's quoting. A directory
	// ends with "/"; a quoted file also gets its closing quote.
	Candidates []string
	// IsPartial reports that the scan stopped at MaximumScannedEntries or the
	// candidates at their limit, so more may match.
	IsPartial bool
}

// CompletePath completes partial, typed as ResolveTypedPath reads it. It
// reads at most MaximumScannedEntries directory entries and keeps partial's
// form: its quotes or escapes, a leading ~, or a relative path.
func (resolver PathResolver) CompletePath(partial string) PathCompletions {
	token := lexTypedPath(partial)
	typedDirectory, namePrefix := splitTypedPath(token.value)
	directory := typedDirectory
	if directory == "" {
		directory = "."
	}
	absoluteDirectory, err := resolver.absolutePath(directory)
	if err != nil {
		return PathCompletions{}
	}
	names, isPartial := scanMatchingNames(absoluteDirectory, namePrefix)
	sort.Strings(names)
	if len(names) > maximumCompletions {
		names, isPartial = names[:maximumCompletions], true
	}
	completions := PathCompletions{IsPartial: isPartial}
	for _, name := range names {
		isDirectory := isDirectoryPath(filepath.Join(absoluteDirectory, name))
		completions.Candidates = append(completions.Candidates, token.encode(typedDirectory+name, isDirectory))
	}
	return completions
}

// scanMatchingNames returns the entry names in directory that start with
// namePrefix, reading at most MaximumScannedEntries, and whether it stopped
// early. Hidden names need a prefix that starts with ".".
func scanMatchingNames(directory string, namePrefix string) ([]string, bool) {
	file, err := os.Open(directory)
	if err != nil {
		return nil, false
	}
	defer func() {
		// A read-only directory handle has nothing to flush on close.
		_ = file.Close()
	}()
	isHiddenShown := strings.HasPrefix(namePrefix, ".")
	var names []string
	for scanned := 0; scanned < MaximumScannedEntries; {
		entries, err := file.ReadDir(min(directoryReadBatch, MaximumScannedEntries-scanned))
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, namePrefix) && (isHiddenShown || !strings.HasPrefix(name, ".")) {
				names = append(names, name)
			}
		}
		scanned += len(entries)
		if err != nil || len(entries) == 0 {
			return names, err != nil && !errors.Is(err, io.EOF)
		}
	}
	return names, true
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

func isDirectoryPath(path string) bool {
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

// typedPathToken is typed text read as one shell word.
type typedPathToken struct {
	value string
	// openingQuote is the quote the text starts with, or 0 for none.
	openingQuote rune
}

// lexTypedPath reads trimmed text as one shell word. Quotes group and are
// removed; a backslash escapes the next character, except inside single
// quotes. An unclosed quote runs to the end, as while typing.
func lexTypedPath(text string) typedPathToken {
	trimmed := strings.TrimSpace(text)
	token := typedPathToken{}
	if strings.HasPrefix(trimmed, "'") || strings.HasPrefix(trimmed, `"`) {
		token.openingQuote = rune(trimmed[0])
	}
	var builder strings.Builder
	var quote rune
	isEscaped := false
	for _, character := range trimmed {
		switch {
		case isEscaped:
			builder.WriteRune(character)
			isEscaped = false
		case character == '\\' && quote != '\'':
			isEscaped = true
		case quote == 0 && (character == '\'' || character == '"'):
			quote = character
		case quote != 0 && character == quote:
			quote = 0
		default:
			builder.WriteRune(character)
		}
	}
	token.value = builder.String()
	return token
}

// shellSpecialCharacters are escaped in an unquoted completion.
const shellSpecialCharacters = " \t\\'\"()&;|<>$`*?[]{}!#"

// encode writes value in the token's form: inside its opening quote, closed
// for a file, or unquoted with backslash escapes.
func (token typedPathToken) encode(value string, isDirectory bool) string {
	if isDirectory {
		value += "/"
	}
	var builder strings.Builder
	switch token.openingQuote {
	case '\'':
		builder.WriteRune('\'')
		builder.WriteString(strings.ReplaceAll(value, "'", `'\''`))
	case '"':
		builder.WriteRune('"')
		for _, character := range value {
			if strings.ContainsRune(`"\$`+"`", character) {
				builder.WriteRune('\\')
			}
			builder.WriteRune(character)
		}
	default:
		for index, character := range value {
			isHomePrefix := index == 0 && character == '~'
			if !isHomePrefix && strings.ContainsRune(shellSpecialCharacters, character) {
				builder.WriteRune('\\')
			}
			builder.WriteRune(character)
		}
		return builder.String()
	}
	if !isDirectory {
		builder.WriteRune(token.openingQuote)
	}
	return builder.String()
}
