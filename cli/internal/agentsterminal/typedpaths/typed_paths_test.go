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
