// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package typedpaths

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// imageExtensions are the file types a pasted path attaches as.
var imageExtensions = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// PastedPathKind says whether one pasted path can become an image
// attachment, and why not.
type PastedPathKind int

const (
	// PastedPathImage is an existing, readable PNG, JPEG, GIF, or WebP file.
	PastedPathImage PastedPathKind = iota + 1
	// PastedPathUnsupportedType is an existing file of another type.
	PastedPathUnsupportedType
	// PastedPathMissing names no file.
	PastedPathMissing
	// PastedPathUnreadable is a file that cannot be read, such as for lack of
	// permission.
	PastedPathUnreadable
	// PastedPathNotRegularFile is a directory or another special file.
	PastedPathNotRegularFile
)

// PastedPath is one path of a pasted file drop.
type PastedPath struct {
	// Text is the path exactly as pasted, with its quotes or escapes.
	Text string
	// Path is the cleaned absolute path.
	Path string
	// Kind says whether the path can become an attachment.
	Kind PastedPathKind
	// Err is the error that made the file unreadable; nil otherwise.
	Err error
}

// PastedFilePaths reads text the way a terminal pastes dropped files: one or
// more paths split as shell words, with quotes and backslash-escaped spaces,
// separated by spaces or line breaks. It returns the paths in order and true
// when text is such a drop. Each word must name an existing image file, or
// start like a path (/, ~/, ./, ../, or file://) and name an existing file
// or a missing image file. Any other text, such as a sentence that names a
// path, returns false. Text that as a whole names one
// existing file, even with unescaped spaces, is a drop of that file.
func (resolver PathResolver) PastedFilePaths(text string) ([]PastedPath, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, false
	}
	// An open quote or a trailing backslash is typed text, not a terminal drop.
	words, isComplete := splitShellWords(trimmed)
	if !isComplete || len(words) == 0 {
		return nil, false
	}
	if !strings.ContainsAny(trimmed, "\n\r") {
		if pasted, isFile := resolver.pastedWholeFile(trimmed); isFile {
			return []PastedPath{pasted}, true
		}
	}
	pastedPaths := make([]PastedPath, 0, len(words))
	for _, word := range words {
		pasted, isPath := resolver.pastedWordPath(word)
		if !isPath {
			return nil, false
		}
		pastedPaths = append(pastedPaths, pasted)
	}
	return pastedPaths, true
}

// pastedWholeFile reads all of text as the path of one existing file.
func (resolver PathResolver) pastedWholeFile(text string) (PastedPath, bool) {
	value, isLocal := localPathOfPastedValue(lexTypedPath(text).value)
	if !isLocal || value == "" || (!isPathShaped(value) && !hasImageExtension(value)) {
		return PastedPath{}, false
	}
	pasted, isResolved := resolver.classifyPastedPath(text, value)
	if !isResolved || pasted.Kind == PastedPathMissing {
		return PastedPath{}, false
	}
	return pasted, true
}

// pastedWordPath reads one shell word as a path. A word is a path when it
// starts like one or names an existing image file.
func (resolver PathResolver) pastedWordPath(word shellWord) (PastedPath, bool) {
	value, isLocal := localPathOfPastedValue(word.value)
	if !isLocal || value == "" {
		return PastedPath{}, false
	}
	pasted, isResolved := resolver.classifyPastedPath(word.text, value)
	if !isResolved {
		return PastedPath{}, false
	}
	switch {
	case pasted.Kind == PastedPathImage:
		return pasted, true
	case pasted.Kind == PastedPathMissing:
		// Prose such as "/api/v1" names no file; a missing image was meant as one.
		return pasted, isPathShaped(word.value) && hasImageExtension(value)
	default:
		return pasted, isPathShaped(word.value)
	}
}

// classifyPastedPath resolves value and checks the file it names. It
// returns false when value cannot be resolved, such as ~ without HOME.
func (resolver PathResolver) classifyPastedPath(text string, value string) (PastedPath, bool) {
	absolute, err := resolver.absolutePath(value)
	if err != nil {
		return PastedPath{}, false
	}
	pasted := PastedPath{Text: text, Path: absolute}
	info, err := os.Stat(absolute)
	switch {
	case errors.Is(err, os.ErrNotExist):
		pasted.Kind = PastedPathMissing
	case err != nil:
		pasted.Kind, pasted.Err = PastedPathUnreadable, err
	case !info.Mode().IsRegular():
		pasted.Kind = PastedPathNotRegularFile
	case !hasImageExtension(absolute):
		pasted.Kind = PastedPathUnsupportedType
	default:
		pasted.Kind = PastedPathImage
		if err := checkReadable(absolute); err != nil {
			pasted.Kind, pasted.Err = PastedPathUnreadable, err
		}
	}
	return pasted, true
}

// checkReadable opens path for reading and closes it again.
func checkReadable(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}

// localPathOfPastedValue turns a file:// URL into its path. It returns
// false for a URL of another host.
func localPathOfPastedValue(value string) (string, bool) {
	if !strings.HasPrefix(value, "file://") {
		return value, true
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Host != "" && parsed.Host != "localhost") {
		return "", false
	}
	return parsed.Path, true
}

// isPathShaped reports a value written as a path: absolute, from the home
// directory, relative to the current or parent directory, or a file URL.
func isPathShaped(value string) bool {
	for _, prefix := range []string{"/", "~/", "./", "../", "file://"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// HasImageExtension reports a path that ends in .png, .jpg, .jpeg, .gif, or
// .webp, in any letter case.
func HasImageExtension(path string) bool {
	return hasImageExtension(path)
}

func hasImageExtension(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// QuoteTypedPath writes path in single quotes, so ResolveTypedPath reads it
// back unchanged whatever characters it holds.
func QuoteTypedPath(path string) string {
	return typedPathToken{openingQuote: '\''}.encode(path, false)
}

// shellWord is one word of pasted text.
type shellWord struct {
	// text is the word as written, with its quotes and escapes.
	text string
	// value is the word with its quotes and escapes removed.
	value string
}

// splitShellWords splits text at unquoted, unescaped white space, including
// line breaks. It returns false when a quote is left open or text ends in a
// lone backslash.
func splitShellWords(text string) ([]shellWord, bool) {
	splitter := shellWordSplitter{}
	for _, character := range text {
		splitter.read(character)
	}
	splitter.endWord()
	return splitter.words, splitter.quote == 0 && !splitter.isEscaped
}

// shellWordSplitter is the state of splitShellWords.
type shellWordSplitter struct {
	words     []shellWord
	raw       strings.Builder
	value     strings.Builder
	quote     rune
	isEscaped bool
	isInWord  bool
}

func (splitter *shellWordSplitter) read(character rune) {
	switch {
	case splitter.isEscaped:
		splitter.value.WriteRune(character)
		splitter.isEscaped = false
	case character == '\\' && splitter.quote != '\'':
		splitter.isEscaped = true
	case splitter.quote == 0 && (character == '\'' || character == '"'):
		splitter.quote = character
	case splitter.quote != 0 && character == splitter.quote:
		splitter.quote = 0
	case splitter.quote == 0 && unicode.IsSpace(character):
		splitter.endWord()
		return
	default:
		splitter.value.WriteRune(character)
	}
	splitter.raw.WriteRune(character)
	splitter.isInWord = true
}

func (splitter *shellWordSplitter) endWord() {
	if splitter.isInWord {
		splitter.words = append(splitter.words, shellWord{text: splitter.raw.String(), value: splitter.value.String()})
	}
	splitter.raw.Reset()
	splitter.value.Reset()
	splitter.isInWord = false
}
