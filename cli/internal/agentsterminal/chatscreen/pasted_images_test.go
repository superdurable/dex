// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/typedpaths"
)

// paste delivers a bracketed paste and runs the commands it returns.
func (harness *screenHarness) paste(content string) {
	_, command := harness.screen.Update(tea.PasteMsg{Content: content})
	harness.runCommand(command)
}

func imageActions(harness *screenHarness) []sessionfeed.RunSlashCommand {
	var actions []sessionfeed.RunSlashCommand
	for _, action := range harness.source.recordedActions() {
		if command, isCommand := action.(sessionfeed.RunSlashCommand); isCommand && command.Name == imageCommandName {
			actions = append(actions, command)
		}
	}
	return actions
}

func imageCommand(path string) sessionfeed.RunSlashCommand {
	return sessionfeed.RunSlashCommand{Name: imageCommandName, Argument: typedpaths.QuoteTypedPath(path)}
}

func TestPastedImagePathAttachesAtOnce(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.paste("'" + imagePath + "'")
	require.Equal(t, []sessionfeed.RunSlashCommand{imageCommand(imagePath)}, imageActions(harness))
	require.True(t, harness.screen.input.IsEmpty(), "the path does not stay as text")
	require.NotContains(t, harness.plainFrame(), "Attach diagram.png?", "no confirmation is asked")

	harness.feed(sessionfeed.ImageAttached{Attachment: sessionfeed.ImageAttachment{Name: "diagram.png", MimeType: "image/png", SizeBytes: 2048}})
	frame := harness.plainFrame()
	require.Contains(t, frame, "diagram.png · image/png")
	require.Contains(t, frame, "Backspace remove image")
	harness.press(key(tea.KeyBackspace))
	require.Empty(t, harness.screen.attachments)
	require.Equal(t, sessionfeed.DetachImage{}, harness.lastAction())
}

func TestPastedImagePathAttachesBesideTypedText(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.typeText("What is in ")
	harness.paste(imagePath)
	require.Equal(t, []sessionfeed.RunSlashCommand{imageCommand(imagePath)}, imageActions(harness))
	require.Equal(t, "What is in ", harness.screen.input.Value(), "the typed text stays")
}

func TestPastedFileDropAttachesEveryImageInOrder(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	directory := filepath.Dir(imagePath)
	spacedPath := filepath.Join(directory, "Bob's screen shot.jpeg")
	require.NoError(t, os.WriteFile(spacedPath, []byte("jpeg"), 0o600))
	escapedSpaced := filepath.Join(directory, `Bob\'s\ screen\ shot.jpeg`)
	oldPath := filepath.Join(directory, "diagram-old.png")
	harness.paste(escapedSpaced + " " + imagePath + "\n" + oldPath)
	require.Equal(t, []sessionfeed.RunSlashCommand{imageCommand(spacedPath), imageCommand(imagePath), imageCommand(oldPath)}, imageActions(harness))
	require.True(t, harness.screen.input.IsEmpty())
}

func TestPastedPathsThatDoNotAttachStayAsTextWithAReason(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	directory := filepath.Dir(imagePath)
	notesPath := filepath.Join(directory, "notes.txt")
	missingPath := filepath.Join(directory, "missing.png")
	harness.paste(imagePath + " " + notesPath + " " + missingPath)
	require.Equal(t, []sessionfeed.RunSlashCommand{imageCommand(imagePath)}, imageActions(harness))
	require.Equal(t, "~/notes.txt ~/missing.png", harness.screen.pathResolver.ShortenHomePaths(harness.screen.input.Value()))
	scrollback := harness.scrollbackText()
	require.Contains(t, scrollback, "Not attached: notes.txt is not a PNG, JPEG, GIF, or WebP image.")
	require.Contains(t, scrollback, "Not attached: ~/missing.png does not exist.")

	harness.screen.input.Clear()
	actionCount := len(harness.source.recordedActions())
	harness.paste(notesPath)
	require.Len(t, harness.source.recordedActions(), actionCount, "nothing attaches")
	require.Equal(t, notesPath, harness.screen.input.Value(), "the paste stays exactly as pasted")
}

func TestPastedUnreadableImageSaysWhy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files without read permission")
	}
	harness, imagePath := newImageHarness(t)
	require.NoError(t, os.Chmod(imagePath, 0o000))
	harness.paste(imagePath)
	require.Empty(t, imageActions(harness))
	require.Contains(t, harness.scrollbackText(), "Not attached: diagram.png cannot be read: permission denied.")
	require.Equal(t, imagePath, harness.screen.input.Value())
}

func TestPastedTextThatIsNotAFileDropStaysText(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	for _, pasted := range []string{"look at " + imagePath, "/api/v1/users"} {
		harness.screen.input.Clear()
		harness.paste(pasted)
		require.Equal(t, pasted, harness.screen.input.Value())
	}
	require.Empty(t, imageActions(harness))
	require.NotContains(t, harness.scrollbackText(), "Not attached")
}

// A path pasted after "/image " is the command's argument, not a new
// attachment.
func TestPastedImagePathAfterImageCommandIsText(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.typeText("/image ")
	harness.paste(imagePath)
	require.Empty(t, imageActions(harness))
	require.Equal(t, "/image "+imagePath, harness.screen.input.Value())
}

func TestPastedImagePathNeedsTheImageCommand(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	directory := t.TempDir()
	imagePath := filepath.Join(directory, "diagram.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png"), 0o600))
	resolver := typedpaths.NewPathResolver(directory, directory)
	harness.screen.pathResolver = &resolver
	harness.paste(imagePath)
	require.Empty(t, imageActions(harness))
	require.Equal(t, imagePath, harness.screen.input.Value())
}

// A paste never vanishes behind a view: the view closes and the input takes
// the paste.
func TestPasteWhileAViewHasFocusGoesToTheInput(t *testing.T) {
	openViews := map[string]func(harness *screenHarness){
		"help": func(harness *screenHarness) {
			harness.press(key(tea.KeyF1))
			require.True(t, harness.screen.isHelpOpen)
		},
		"approval": func(harness *screenHarness) {
			startTurn(harness, "Post the summary.")
			harness.feed(connectorApproval("approval-1"))
			require.True(t, harness.screen.isPopupOpen)
		},
		"tool picker": func(harness *screenHarness) {
			startTurn(harness, "Look around.")
			harness.feed(outputTool("call-1", "turn-1", "read_file", "output"), sessionfeed.TurnEnded{TurnID: "turn-1"})
			harness.typeText("/tools")
			harness.press(key(tea.KeyEnter))
			require.True(t, harness.screen.isToolPickerOpen)
		},
		"setting picker": func(harness *screenHarness) {
			harness.screen.session.Model = "gpt-5.2"
			require.True(t, harness.screen.openSettingPicker(modelCommandName))
		},
		"tool output pager": func(harness *screenHarness) {
			startTurn(harness, "Look around.")
			harness.feed(outputTool("call-1", "turn-1", "read_file", "output"), sessionfeed.TurnEnded{TurnID: "turn-1"})
			pressCtrlO(harness)
			require.NotNil(t, harness.screen.pager)
		},
		"pending bar": func(harness *screenHarness) {
			startTurn(harness, "Post the summary.")
			harness.feed(connectorApproval("approval-1"))
			harness.press(key(tea.KeyEscape), key(tea.KeyTab))
			require.Equal(t, pendingFocusInterrupts, harness.screen.pendingFocus)
		},
	}
	for name, openView := range openViews {
		t.Run(name, func(t *testing.T) {
			harness, imagePath := newImageHarness(t)
			openView(harness)
			harness.paste("hello")
			require.False(t, harness.screen.isHelpOpen)
			require.False(t, harness.screen.isPopupOpen)
			require.False(t, harness.screen.isToolPickerOpen)
			require.Nil(t, harness.screen.settingPicker)
			require.Nil(t, harness.screen.pager)
			require.Equal(t, pendingFocusNone, harness.screen.pendingFocus)
			require.Equal(t, "hello", harness.screen.input.Value())
			require.Empty(t, approvalActions(harness), "a paste never answers an approval")

			harness.press(key(tea.KeyF1))
			harness.paste(imagePath)
			require.Equal(t, []sessionfeed.RunSlashCommand{imageCommand(imagePath)}, imageActions(harness))
		})
	}
}
