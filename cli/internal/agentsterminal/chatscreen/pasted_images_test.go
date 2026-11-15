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
	"strings"
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

func attachImageActions(harness *screenHarness) []sessionfeed.AttachImage {
	var actions []sessionfeed.AttachImage
	for _, action := range harness.source.recordedActions() {
		if attach, isAttach := action.(sessionfeed.AttachImage); isAttach {
			actions = append(actions, attach)
		}
	}
	return actions
}

// imageActions returns the paths of the AttachImage requests, in order.
func imageActions(harness *screenHarness) []string {
	var paths []string
	for _, attach := range attachImageActions(harness) {
		paths = append(paths, attach.Path)
	}
	return paths
}

func imageCommand(path string) string {
	return path
}

func diagramAttached(requestID int64) sessionfeed.ImageAttached {
	return sessionfeed.ImageAttached{Attachment: sessionfeed.ImageAttachment{Name: "diagram.png", MimeType: "image/png", SizeBytes: 2048}, RequestID: requestID}
}

func sentMessages(harness *screenHarness) []sessionfeed.SendUserMessage {
	var messages []sessionfeed.SendUserMessage
	for _, action := range harness.source.recordedActions() {
		if message, isMessage := action.(sessionfeed.SendUserMessage); isMessage {
			messages = append(messages, message)
		}
	}
	return messages
}

func TestPastedImagePathAttachesAtOnce(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.paste("'" + imagePath + "'")
	require.Equal(t, []string{imageCommand(imagePath)}, imageActions(harness))
	require.True(t, harness.screen.input.IsEmpty(), "the path does not stay as text")
	require.NotContains(t, harness.plainFrame(), "Attach diagram.png?", "no confirmation is asked")
	require.Contains(t, harness.plainFrame(), "attaching image…")

	harness.feed(diagramAttached(attachImageActions(harness)[0].RequestID))
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
	require.Equal(t, []string{imageCommand(imagePath)}, imageActions(harness))
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
	require.Equal(t, []string{imageCommand(spacedPath), imageCommand(imagePath), imageCommand(oldPath)}, imageActions(harness))
	require.True(t, harness.screen.input.IsEmpty())
}

func TestPastedPathsThatDoNotAttachStayAsTextWithAReason(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	directory := filepath.Dir(imagePath)
	notesPath := filepath.Join(directory, "notes.txt")
	missingPath := filepath.Join(directory, "missing.png")
	harness.paste(imagePath + " " + notesPath + " " + missingPath)
	require.Equal(t, []string{imageCommand(imagePath)}, imageActions(harness))
	require.Equal(t, "~/notes.txt ~/missing.png", harness.screen.pathResolver.ShortenHomePaths(harness.screen.input.Value()))
	scrollback := harness.scrollbackText()
	require.Contains(t, scrollback, "Not attached: notes.txt is not a PNG, JPEG, GIF, or WebP image.")
	require.Contains(t, scrollback, "Not attached: ~/missing.png does not exist.")

	harness.screen.input.Clear()
	actionCount := len(harness.source.recordedActions())
	harness.paste(notesPath)
	require.Len(t, harness.source.recordedActions(), actionCount, "nothing attaches")
	require.Equal(t, notesPath, harness.screen.input.Value(), "the paste stays exactly as pasted")
	require.Equal(t, 1, strings.Count(harness.scrollbackText(), "notes.txt is not a PNG"), "a pasted source path alone is text, with no notice")
}

// Enter before the pasted image is stored waits, then sends one message
// that carries the image.
func TestEnterRightAfterAPastedImageSendsItWithTheMessage(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.typeText("What is this?")
	harness.paste(imagePath)
	harness.press(key(tea.KeyEnter))
	require.Empty(t, sentMessages(harness), "the message waits for its image")
	require.Equal(t, "What is this?", harness.screen.input.Value())
	require.Contains(t, harness.plainFrame(), "attaching image… sends when ready")

	harness.feed(diagramAttached(attachImageActions(harness)[0].RequestID))
	messages := sentMessages(harness)
	require.Len(t, messages, 1)
	require.Equal(t, "What is this?", messages[0].Text)
	require.Len(t, messages[0].Images, 1)
	require.Equal(t, "diagram.png", messages[0].Images[0].Name)
	require.Empty(t, harness.screen.attachments, "the image never reaches a later message")
	require.True(t, harness.screen.input.IsEmpty())
}

// A failed attach keeps the waiting message unsent and says so.
func TestFailedPastedImageKeepsTheMessageUnsent(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.typeText("What is this?")
	harness.paste(imagePath)
	harness.press(key(tea.KeyEnter))
	harness.feed(
		sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityError, Text: "Image not attached: diagram.png could not be stored in the media store: disk full"},
		sessionfeed.ImageAttachFailed{RequestID: attachImageActions(harness)[0].RequestID},
	)
	require.Empty(t, sentMessages(harness), "nothing is sent silently")
	require.Equal(t, "What is this?", harness.screen.input.Value())
	scrollback := harness.scrollbackText()
	require.Contains(t, scrollback, "media store")
	require.Contains(t, scrollback, "Message not sent: an image did not attach.")

	harness.press(key(tea.KeyEnter))
	messages := sentMessages(harness)
	require.Len(t, messages, 1, "Enter now sends without the image")
	require.Empty(t, messages[0].Images)
}

// Typing after Enter keeps the draft for the next Enter; a session switch
// drops a pasted image whose answer comes late.
func TestEditedOrSwitchedDraftDoesNotSendOnItsOwn(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.typeText("first")
	harness.paste(imagePath)
	harness.press(key(tea.KeyEnter))
	harness.typeText(" more")
	harness.feed(diagramAttached(attachImageActions(harness)[0].RequestID))
	require.Empty(t, sentMessages(harness), "the edited draft waits for Enter")
	require.Len(t, harness.screen.attachments, 1)

	harness.screen.input.Clear()
	harness.paste(imagePath)
	lateRequestID := attachImageActions(harness)[1].RequestID
	switchSession(harness, "openai-sdk-20261111-150000-ffffff")
	harness.feed(diagramAttached(lateRequestID))
	require.Empty(t, harness.screen.attachments, "the old draft's image never joins the new session")
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
			require.Equal(t, []string{imageCommand(imagePath)}, imageActions(harness))
		})
	}
}

// Any change after Enter cancels the automatic send: a recalled message or a
// new line.
func TestChangedDraftAfterEnterIsNotSentWhenImagesAttach(t *testing.T) {
	changes := map[string]func(harness *screenHarness){
		"history recall": func(harness *screenHarness) {
			harness.press(ctrlKey('p'))
			require.Equal(t, "previous message", harness.screen.input.Value())
		},
		"new line": func(harness *screenHarness) {
			harness.press(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			harness, imagePath := newImageHarness(t)
			harness.screen.inputHistory, harness.screen.historyIndex = []string{"previous message"}, 1
			harness.typeText("current draft")
			harness.paste(imagePath)
			harness.press(key(tea.KeyEnter))
			change(harness)
			harness.feed(diagramAttached(attachImageActions(harness)[0].RequestID))
			require.Empty(t, sentMessages(harness))
			require.Len(t, harness.screen.attachments, 1, "the image stays on the draft")
		})
	}
}

// A change cancels the automatic send for good, even when later edits bring
// back the text Enter saw.
func TestDraftEditedBackToItsTextIsNotSentWhenImagesAttach(t *testing.T) {
	changes := map[string]func(harness *screenHarness){
		"recall away and back": func(harness *screenHarness) {
			harness.press(ctrlKey('p'))
			require.Equal(t, "previous message", harness.screen.input.Value())
			harness.press(ctrlKey('p'))
		},
		"new line then recall": func(harness *screenHarness) {
			harness.press(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, ctrlKey('p'), ctrlKey('p'))
		},
		"shift-enter then delete": func(harness *screenHarness) {
			harness.press(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, key(tea.KeyBackspace))
		},
		"alt-enter then delete": func(harness *screenHarness) {
			harness.press(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}, key(tea.KeyBackspace))
		},
		"ctrl-j then delete": func(harness *screenHarness) {
			harness.press(ctrlKey('j'), key(tea.KeyBackspace))
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			harness, imagePath := newImageHarness(t)
			harness.screen.inputHistory, harness.screen.historyIndex = []string{"current draft", "previous message"}, 2
			harness.typeText("current draft")
			harness.paste(imagePath)
			harness.press(key(tea.KeyEnter))
			change(harness)
			require.Equal(t, "current draft", harness.screen.input.Value())
			require.NotContains(t, harness.plainFrame(), "sends when ready")
			harness.feed(diagramAttached(attachImageActions(harness)[0].RequestID))
			require.Empty(t, sentMessages(harness))
		})
	}
}

// A recalled slash command never runs on its own when the images attach.
func TestRecalledSlashCommandIsNotRunWhenImagesAttach(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.screen.inputHistory, harness.screen.historyIndex = []string{"/help"}, 1
	harness.typeText("current draft")
	harness.paste(imagePath)
	harness.press(key(tea.KeyEnter))
	harness.press(ctrlKey('p'))
	harness.feed(diagramAttached(attachImageActions(harness)[0].RequestID))
	require.False(t, harness.screen.isHelpOpen)
	require.Equal(t, "/help", harness.screen.input.Value())
}
