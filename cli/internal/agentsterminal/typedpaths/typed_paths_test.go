// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package typedpaths

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveTypedPath(t *testing.T) {
	resolver := NewPathResolver("/home/user", "/work")
	for text, expected := range map[string]string{
		"diagram.png":         "/work/diagram.png",
		`"my diagram.png"`:    "/work/my diagram.png",
		"'~/pictures/a.png'":  "/home/user/pictures/a.png",
		"~":                   "/home/user",
		"/tmp/../tmp/b.png  ": "/tmp/b.png",
	} {
		path, err := resolver.ResolveTypedPath(text)
		require.NoError(t, err, text)
		require.Equal(t, expected, path, text)
	}
	_, err := resolver.ResolveTypedPath("  ")
	require.ErrorIs(t, err, ErrEmptyPath)
	_, err = NewPathResolver("", "/work").ResolveTypedPath("~/a.png")
	require.ErrorContains(t, err, "~ needs HOME")
}

func TestPastedImagePath(t *testing.T) {
	directory := t.TempDir()
	imagePath := filepath.Join(directory, "my diagram.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png"), 0o600))
	textPath := filepath.Join(directory, "notes.txt")
	require.NoError(t, os.WriteFile(textPath, []byte("text"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(directory, "folder.png"), 0o700))
	resolver := NewPathResolver(directory, directory)
	for _, pasted := range []string{
		imagePath,
		"'" + imagePath + "' ",
		filepath.Join(directory, `my\ diagram.png`),
		"file://" + filepath.ToSlash(filepath.Join(directory, "my%20diagram.png")),
		"~/my diagram.png",
		"my diagram.png",
	} {
		path, isImage := resolver.PastedImagePath(pasted)
		require.True(t, isImage, pasted)
		require.Equal(t, imagePath, path, pasted)
	}
	for _, pasted := range []string{
		textPath,
		filepath.Join(directory, "missing.png"),
		filepath.Join(directory, "folder.png"),
		imagePath + "\n" + imagePath,
		"file://example.com" + imagePath,
		"look at this.png please",
	} {
		_, isImage := resolver.PastedImagePath(pasted)
		require.False(t, isImage, pasted)
	}
}

func TestCompletePath(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"diagram.png", "diagrams", "data.csv", ".hidden.png"} {
		path := filepath.Join(home, name)
		if name == "diagrams" {
			require.NoError(t, os.Mkdir(path, 0o700))
			continue
		}
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}
	resolver := NewPathResolver(home, home)
	require.Equal(t, []string{"diagram.png", "diagrams/"}, resolver.CompletePath("diag").Candidates)
	require.Equal(t, []string{"~/data.csv", "~/diagram.png", "~/diagrams/"}, resolver.CompletePath("~/").Candidates)
	require.Equal(t, []string{"~/data.csv", "~/diagram.png", "~/diagrams/"}, resolver.CompletePath("~").Candidates)
	require.Equal(t, []string{".hidden.png"}, resolver.CompletePath(".h").Candidates)
	require.Equal(t, []string{home + "/diagrams/"}, resolver.CompletePath(home+"/diagrams").Candidates)
	require.Empty(t, resolver.CompletePath("/does/not/exist/x").Candidates)
	require.Equal(t, "diagram", LongestCommonPrefix([]string{"diagram.png", "diagrams/"}))
	require.Equal(t, "é", LongestCommonPrefix([]string{"éa", "éb"}))
	require.Equal(t, "", LongestCommonPrefix([]string{"é", "è"}))
}

// /image arguments, completion, and pasted paths read quotes and escapes
// the same way.
func TestTypedPathsShareOneLexer(t *testing.T) {
	resolver := NewPathResolver("/home/user", "/work")
	for text, expected := range map[string]string{
		`/tmp/my\ diagram.png`:     "/tmp/my diagram.png",
		`"/tmp/my diagram.png"`:    "/tmp/my diagram.png",
		`'/tmp/it'\''s.png'`:       "/tmp/it's.png",
		`"/tmp/say \"hi\".png"`:    `/tmp/say "hi".png`,
		`'/tmp/back\slash.png'`:    `/tmp/back\slash.png`,
		`~/My\ Pictures/a.png`:     "/home/user/My Pictures/a.png",
		`"~/My Pictures/a.png" `:   "/home/user/My Pictures/a.png",
		`/tmp/unclosed "quote.png`: "/tmp/unclosed quote.png",
	} {
		path, err := resolver.ResolveTypedPath(text)
		require.NoError(t, err, text)
		require.Equal(t, expected, path, text)
	}
}

func TestCompletePathKeepsTheTypedQuoting(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "my diagram.png"), nil, 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(home, "my folder"), 0o700))
	resolver := NewPathResolver(home, home)
	require.Equal(t, []string{`my\ diagram.png`, `my\ folder/`}, resolver.CompletePath("my").Candidates)
	require.Equal(t, []string{`my\ diagram.png`}, resolver.CompletePath(`my\ d`).Candidates)
	require.Equal(t, []string{`"my diagram.png"`, `"my folder/`}, resolver.CompletePath(`"my`).Candidates)
	require.Equal(t, []string{`'~/my diagram.png'`}, resolver.CompletePath(`'~/my d`).Candidates)
	for _, typed := range []string{`my\ diagram.png`, `"my diagram.png"`, `'~/my diagram.png'`} {
		path, err := resolver.ResolveTypedPath(typed)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(home, "my diagram.png"), path, "a completion resolves to the file: %s", typed)
	}
}

// countingDirectory lists totalCount synthetic entries and counts the ones
// read.
type countingDirectory struct {
	totalCount int
	readCount  int
}

func (directory *countingDirectory) ReadDir(count int) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	for len(entries) < count && directory.readCount < directory.totalCount {
		entries = append(entries, syntheticEntry(fmt.Sprintf("entry-%05d.png", directory.readCount)))
		directory.readCount++
	}
	if len(entries) == 0 {
		return nil, io.EOF
	}
	return entries, nil
}

func (directory *countingDirectory) Close() error { return nil }

type syntheticEntry string

func (entry syntheticEntry) Name() string               { return string(entry) }
func (entry syntheticEntry) IsDir() bool                { return false }
func (entry syntheticEntry) Type() os.FileMode          { return 0 }
func (entry syntheticEntry) Info() (os.FileInfo, error) { return nil, os.ErrNotExist }

// The scan reads at most MaximumScannedEntries; a match past them reports a
// partial scan, and the exact name still completes.
func TestCompletePathReadsAtMostTheCap(t *testing.T) {
	realDirectory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(realDirectory, "entry-09000.png"), nil, 0o600))
	listing := &countingDirectory{totalCount: 10_000}
	resolver := NewPathResolver("", realDirectory)
	resolver.openDirectory = func(string) (directoryReader, error) { return listing, nil }
	completions := resolver.CompletePath("entry-09")
	require.Equal(t, MaximumScannedEntries, listing.readCount, "the scan stops at the cap")
	require.Empty(t, completions.Candidates, "entry-09000 lies past the cap")
	require.True(t, completions.IsPartial)
	listing.readCount = 0
	exact := resolver.CompletePath("entry-09000.png")
	require.Equal(t, []string{"entry-09000.png"}, exact.Candidates, "the whole name still completes")
	listing.readCount, listing.totalCount = 0, 30
	whole := resolver.CompletePath("entry-")
	require.False(t, whole.IsPartial)
	require.Len(t, whole.Candidates, 30)
}

// A huge directory costs at most MaximumScannedEntries reads.
func TestCompletePathStopsInAHugeDirectory(t *testing.T) {
	directory := t.TempDir()
	for index := range MaximumScannedEntries + 100 {
		require.NoError(t, os.WriteFile(filepath.Join(directory, fmt.Sprintf("shot-%05d.png", index)), nil, 0o600))
	}
	completions := NewPathResolver("", directory).CompletePath("shot-")
	require.True(t, completions.IsPartial)
	require.Len(t, completions.Candidates, maximumCompletions)
	whole := NewPathResolver("", t.TempDir()).CompletePath("")
	require.False(t, whole.IsPartial, "an empty directory reads to its end")
}
