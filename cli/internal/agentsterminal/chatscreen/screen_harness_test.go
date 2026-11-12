// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// testClockStart is the fixed time of every test frame, so spinner frames
// and elapsed times are stable.
var testClockStart = time.Date(2026, 11, 11, 14, 22, 33, 240_000_000, time.UTC)

// recordingSource is a ChatSessionSource that records actions. Tests feed
// updates straight into the screen, so NextSessionUpdate never returns one.
type recordingSource struct {
	mutex   sync.Mutex
	actions []sessionfeed.UserAction
}

func (source *recordingSource) NextSessionUpdate(ctx context.Context) (sessionfeed.SessionUpdate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (source *recordingSource) SubmitUserAction(_ context.Context, action sessionfeed.UserAction) error {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	source.actions = append(source.actions, action)
	return nil
}

func (source *recordingSource) recordedActions() []sessionfeed.UserAction {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	return append([]sessionfeed.UserAction(nil), source.actions...)
}

// screenHarness drives one ChatScreen without a terminal.
type screenHarness struct {
	t          *testing.T
	screen     *ChatScreen
	source     *recordingSource
	profile    colorprofile.Profile
	now        time.Time
	scrollback []string
}

type harnessOptions struct {
	width     int
	height    int
	profile   colorprofile.Profile
	isASCII   bool
	commands  []sessionfeed.SlashCommand
	skipIntro bool
}

func newScreenHarness(t *testing.T, options harnessOptions) *screenHarness {
	t.Helper()
	if options.width == 0 {
		options.width = 80
	}
	if options.height == 0 {
		options.height = 40
	}
	if options.profile == colorprofile.NoTTY {
		options.profile = colorprofile.ANSI
	}
	harness := &screenHarness{t: t, source: &recordingSource{}, profile: options.profile, now: testClockStart}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	harness.screen = NewChatScreen(ctx, ChatScreenConfig{
		Source: harness.source,
		Theme:  terminaltheme.NewTheme(options.profile, !options.isASCII),
		PrintScrollback: func(text string) tea.Cmd {
			harness.scrollback = append(harness.scrollback, text)
			return nil
		},
		Now:             func() time.Time { return harness.now },
		SessionCommands: options.commands,
	})
	harness.screen.Update(tea.WindowSizeMsg{Width: options.width, Height: options.height})
	if !options.skipIntro {
		harness.feed(
			sessionfeed.SessionOpened{
				Command: "openai-sdk", Model: "gpt-5.2", SessionID: "openai-sdk-20261111-142233-a1b2c3",
				IsNewSession: true, AcceptsTurnControl: true,
			},
			sessionfeed.SessionLoaded{},
		)
	}
	return harness
}

// feed applies session updates. Their commands only wait for the next
// update or tick, so they are not run.
func (harness *screenHarness) feed(updates ...sessionfeed.SessionUpdate) {
	for _, update := range updates {
		harness.screen.Update(sessionUpdateMsg{update: update})
	}
}

// press applies keys and runs the commands they return, which submit
// actions or quit.
func (harness *screenHarness) press(keys ...tea.KeyPressMsg) {
	for _, key := range keys {
		_, command := harness.screen.Update(key)
		harness.runCommand(command)
	}
}

func (harness *screenHarness) typeText(text string) {
	for _, character := range text {
		harness.press(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
}

func (harness *screenHarness) runCommand(command tea.Cmd) {
	if command == nil {
		return
	}
	message := command()
	if batch, isBatch := message.(tea.BatchMsg); isBatch {
		for _, inner := range batch {
			harness.runCommand(inner)
		}
		return
	}
	if failure, isFailure := message.(userActionFailedMsg); isFailure {
		harness.screen.Update(failure)
	}
}

func (harness *screenHarness) advance(duration time.Duration) {
	harness.now = harness.now.Add(duration)
}

// frame returns the scrollback, a marker, and the live area, downsampled to
// the harness profile.
func (harness *screenHarness) frame() string {
	view := harness.screen.View()
	var builder strings.Builder
	for _, block := range harness.scrollback {
		builder.WriteString(block)
		builder.WriteString("\n")
	}
	builder.WriteString("~~~~ live area ~~~~\n")
	builder.WriteString(view.Content)
	builder.WriteString("\n")
	if view.Cursor != nil {
		fmt.Fprintf(&builder, "~~~~ cursor %d,%d ~~~~\n", view.Cursor.X, view.Cursor.Y)
	}
	return terminaltheme.DownsampleStyledText(harness.profile, builder.String())
}

// plainFrame returns frame without escape sequences.
func (harness *screenHarness) plainFrame() string {
	return terminaltheme.StripStyles(harness.frame())
}

func (harness *screenHarness) lastAction() sessionfeed.UserAction {
	actions := harness.source.recordedActions()
	require.NotEmpty(harness.t, actions, "no action was submitted")
	return actions[len(actions)-1]
}

func key(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

func ctrlKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

func textKey(character rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: character, Text: string(character)}
}

// goldenFrameSet collects named frames into one golden file.
type goldenFrameSet struct {
	t        *testing.T
	sections []string
}

func newGoldenFrameSet(t *testing.T) *goldenFrameSet {
	return &goldenFrameSet{t: t}
}

func (set *goldenFrameSet) add(label string, frame string) {
	set.sections = append(set.sections, "==== "+label+" ====\n"+frame)
}

// compare checks testdata/<name>.golden, writing escapes as ␛.
// UPDATE_GOLDEN=1 rewrites it.
func (set *goldenFrameSet) compare(name string) {
	set.t.Helper()
	actual := strings.ReplaceAll(strings.Join(set.sections, "\n"), "\x1b", "␛")
	path := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(set.t, os.MkdirAll("testdata", 0o755))
		require.NoError(set.t, os.WriteFile(path, []byte(actual), 0o644))
		return
	}
	expected, err := os.ReadFile(path)
	require.NoError(set.t, err, "missing golden file; run with UPDATE_GOLDEN=1")
	require.Equal(set.t, string(expected), actual, "frames differ from %s; run with UPDATE_GOLDEN=1 and review the diff", path)
}

// widthMatrix renders build at each width in ANSI, then at 80 columns with
// NO_COLOR.
func widthMatrix(t *testing.T, name string, widths []int, build func(harness *screenHarness)) {
	t.Helper()
	set := newGoldenFrameSet(t)
	for _, width := range widths {
		harness := newScreenHarness(t, harnessOptions{width: width})
		build(harness)
		set.add(fmt.Sprintf("ansi width %d", width), harness.frame())
	}
	noColor := newScreenHarness(t, harnessOptions{width: 80, profile: colorprofile.ASCII})
	build(noColor)
	set.add("no_color width 80", noColor.frame())
	set.compare(name)
}

var allWidths = []int{40, 60, 80, 120}

// scrollbackText returns the printed scrollback without escape sequences.
func (harness *screenHarness) scrollbackText() string {
	return terminaltheme.StripStyles(strings.Join(harness.scrollback, "\n"))
}

func splitLines(text string) []string {
	return strings.Split(text, "\n")
}

func displayWidthForTest(line string) int {
	return terminaltheme.DisplayWidth(line)
}
