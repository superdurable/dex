// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package typedpaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// pastedFileFixture is a directory with the files a drop names.
type pastedFileFixture struct {
	directory  string
	spaced     string
	apostrophe string
	other      string
	text       string
	resolver   PathResolver
}

func newPastedFileFixture(t *testing.T) pastedFileFixture {
	directory := t.TempDir()
	fixture := pastedFileFixture{
		directory:  directory,
		spaced:     filepath.Join(directory, "my diagram.png"),
		apostrophe: filepath.Join(directory, "Bob's chart.JPG"),
		other:      filepath.Join(directory, "other.webp"),
		text:       filepath.Join(directory, "notes.txt"),
		resolver:   NewPathResolver(directory, directory),
	}
	for _, path := range []string{fixture.spaced, fixture.apostrophe, fixture.other, fixture.text} {
		require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(directory, "folder.png"), 0o700))
	return fixture
}

func TestPastedFilePathsReadsOneDroppedImage(t *testing.T) {
	fixture := newPastedFileFixture(t)
	for _, pasted := range []string{
		fixture.spaced,
		"'" + fixture.spaced + "' ",
		`"` + fixture.spaced + `"`,
		filepath.Join(fixture.directory, `my\ diagram.png`),
		"file://" + filepath.ToSlash(filepath.Join(fixture.directory, "my%20diagram.png")),
		"~/my diagram.png",
		"my diagram.png",
	} {
		paths, isDrop := fixture.resolver.PastedFilePaths(pasted)
		require.True(t, isDrop, pasted)
		require.Len(t, paths, 1, pasted)
		require.Equal(t, fixture.spaced, paths[0].Path, pasted)
		require.Equal(t, PastedPathImage, paths[0].Kind, pasted)
	}
}

func TestPastedFilePathsSplitsSeveralDroppedFiles(t *testing.T) {
	fixture := newPastedFileFixture(t)
	escapedSpaced := filepath.Join(fixture.directory, `my\ diagram.png`)
	quotedApostrophe := `"` + fixture.apostrophe + `"`
	for _, pasted := range []string{
		escapedSpaced + " " + quotedApostrophe + " " + fixture.other,
		escapedSpaced + "\n" + quotedApostrophe + "\n" + fixture.other + "\n",
	} {
		paths, isDrop := fixture.resolver.PastedFilePaths(pasted)
		require.True(t, isDrop, pasted)
		require.Equal(t, []PastedPath{
			{Text: escapedSpaced, Path: fixture.spaced, Kind: PastedPathImage},
			{Text: quotedApostrophe, Path: fixture.apostrophe, Kind: PastedPathImage},
			{Text: fixture.other, Path: fixture.other, Kind: PastedPathImage},
		}, paths, pasted)
	}
}

func TestPastedFilePathsClassifiesPathsThatDoNotAttach(t *testing.T) {
	fixture := newPastedFileFixture(t)
	missing := filepath.Join(fixture.directory, "missing.png")
	folder := filepath.Join(fixture.directory, "folder.png")
	paths, isDrop := fixture.resolver.PastedFilePaths(fixture.other + " " + fixture.text + " " + missing + " " + folder)
	require.True(t, isDrop)
	kinds := make([]PastedPathKind, 0, len(paths))
	for _, pasted := range paths {
		kinds = append(kinds, pasted.Kind)
	}
	require.Equal(t, []PastedPathKind{PastedPathImage, PastedPathUnsupportedType, PastedPathMissing, PastedPathNotRegularFile}, kinds)
}

func TestPastedFilePathsReportsAnUnreadableImage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files without read permission")
	}
	fixture := newPastedFileFixture(t)
	require.NoError(t, os.Chmod(fixture.other, 0o000))
	paths, isDrop := fixture.resolver.PastedFilePaths(fixture.other)
	require.True(t, isDrop)
	require.Equal(t, PastedPathUnreadable, paths[0].Kind)
	require.ErrorIs(t, paths[0].Err, os.ErrPermission)
}

func TestPastedFilePathsLeavesTextAlone(t *testing.T) {
	fixture := newPastedFileFixture(t)
	for _, pasted := range []string{
		"",
		"   ",
		"look at this.png please",
		"look at " + fixture.other,
		fixture.other + " and more",
		"/api/v1/users",
		"notes.txt",
		"'" + fixture.other + " " + fixture.text,
		"file://example.com" + fixture.other,
	} {
		_, isDrop := fixture.resolver.PastedFilePaths(pasted)
		require.False(t, isDrop, pasted)
	}
}

func TestQuoteTypedPathRoundTrips(t *testing.T) {
	resolver := NewPathResolver("/home/user", "/work")
	for _, path := range []string{"/tmp/Bob's chart.png", `/tmp/a "b" \c.png`, "/tmp/$HOME `x`.png"} {
		resolved, err := resolver.ResolveTypedPath(QuoteTypedPath(path))
		require.NoError(t, err, path)
		require.Equal(t, path, resolved)
	}
}

func TestPastedFilePathsRejectsAnUnclosedQuote(t *testing.T) {
	fixture := newPastedFileFixture(t)
	for _, pasted := range []string{`"` + fixture.other, "'" + fixture.other, fixture.other + `\`} {
		_, isDrop := fixture.resolver.PastedFilePaths(pasted)
		require.False(t, isDrop, pasted)
	}
}
