// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/superdurable/dex/cli/internal/agentsterminal/connectorlist"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionlisting"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionpicker"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// maxNoticeCodePoints is the contract's longest shown notice text.
const maxNoticeCodePoints = 500

// listModel is the part of the sessions picker and the connectors list the
// full-screen host drives.
type listModel interface {
	Init() tea.Cmd
	Update(message tea.Msg) tea.Cmd
	View() string
}

// runSessionsMode shows the session picker and sends the picked session
// as the result.
func runSessionsMode(
	ctx context.Context,
	channel *launcherchannel.LauncherChannel,
	launch launchcontract.LaunchDocument,
	connection dexConnection,
	signals *terminalSignals,
	stdin *os.File,
	stdout *os.File,
) error {
	modeContext, cancelMode := context.WithCancel(ctx)
	defer cancelMode()
	theme := newLaunchTheme(stdout)
	commandName := launch.Command.Name
	picker := sessionpicker.NewSessionPicker(modeContext, sessionpicker.SessionPickerConfig{
		Sessions:       sessionlisting.NewSessionLister(connection.client, launch.Session.FlowType),
		Theme:          theme,
		Heading:        sessionsHeading(launch, theme),
		NewSessionHint: "run " + commandName + " to start a new one",
		EmptyHint:      "No sessions yet. Run " + commandName + " to start one.",
		CloseKeyName:   "exit",
	})
	host, err := runFullScreenList(modeContext, channel, launch, signals, stdin, stdout, picker)
	if err != nil {
		return err
	}
	if host.chosenSessionID == "" {
		return nil
	}
	if sendErr := channel.SendResult(host.chosenSessionID); sendErr != nil {
		debugLog("send result", sendErr)
		return &ExitError{Code: exitCodeFailure}
	}
	return nil
}

// runConnectorsMode shows the full-screen connectors list.
func runConnectorsMode(
	ctx context.Context,
	channel *launcherchannel.LauncherChannel,
	launch launchcontract.LaunchDocument,
	signals *terminalSignals,
	stdin *os.File,
	stdout *os.File,
) error {
	modeContext, cancelMode := context.WithCancel(ctx)
	defer cancelMode()
	catalog, err := connectorlist.DecodeConnectorCatalog(launch.Connectors)
	if err != nil {
		// The launch checks validated connectors, so this never happens.
		return fmt.Errorf("agents-tui: decode the launch connectors: %w", err)
	}
	list := connectorlist.NewConnectorList(modeContext, connectorlist.ConnectorListConfig{
		Catalog:  catalog,
		Requests: channel,
		WebURL:   launch.WebURL(),
		Theme:    newLaunchTheme(stdout),
	})
	_, err = runFullScreenList(modeContext, channel, launch, signals, stdin, stdout, list)
	return err
}

// runFullScreenList runs list on the alt screen until the user leaves it,
// a signal arrives, or the channel ends.
func runFullScreenList(
	ctx context.Context,
	channel *launcherchannel.LauncherChannel,
	launch launchcontract.LaunchDocument,
	signals *terminalSignals,
	stdin *os.File,
	stdout *os.File,
	list listModel,
) (*fullScreenListHost, error) {
	listContext, cancelList := context.WithCancel(ctx)
	defer cancelList()
	channel.Start(launch.Methods)
	environment := os.Environ()
	profile := terminaltheme.TerminalColorProfile(colorprofile.Detect(stdout, environment), os.LookupEnv)
	host := newFullScreenListHost(list, newLaunchTheme(stdout))
	for _, notice := range launch.Notices {
		host.showNotice(notice.Level, notice.Text)
	}
	output := newFirstFrameWriter(stdout, func() {
		if sendErr := channel.SendReady(); sendErr != nil {
			debugLog("send ready", sendErr)
		}
	})
	frames := newReadyReporter(host, output, noRefresh{})
	program := tea.NewProgram(
		frames,
		tea.WithContext(listContext),
		tea.WithInput(stdin),
		tea.WithOutput(output),
		tea.WithEnvironment(environment),
		tea.WithColorProfile(profile),
		tea.WithoutSignalHandler(),
	)
	signals.Attach(program)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		relayChannelEvents(listContext, channel, program)
	}()
	_, runErr := program.Run()
	cancelList()
	<-relayDone
	if code := frames.signalExitCode(); code != 0 {
		return host, &ExitError{Code: code}
	}
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return host, fmt.Errorf("agents-tui: run the terminal program: %w", runErr)
	}
	if host.isChannelEnded {
		return host, &ExitError{Code: exitCodeFailure}
	}
	return host, nil
}

// relayChannelEvents hands notices and the end of the channel to program.
func relayChannelEvents(ctx context.Context, channel *launcherchannel.LauncherChannel, program *tea.Program) {
	notifications := channel.Notifications()
	for {
		select {
		case notification, isOpen := <-notifications:
			if !isOpen {
				program.Send(channelEndedMsg{})
				return
			}
			if notification.Notice != nil {
				program.Send(launcherNoticeMsg{level: notification.Notice.Level, text: notification.Notice.Text})
			}
		case <-ctx.Done():
			return
		}
	}
}

// launcherNoticeMsg is one notice from the launcher.
type launcherNoticeMsg struct {
	level string
	text  string
}

// channelEndedMsg reports that the launcher closed the channel.
type channelEndedMsg struct{}

// fullScreenListHost draws a list on the alt screen, under the latest
// notice.
type fullScreenListHost struct {
	list            listModel
	theme           terminaltheme.Theme
	width           int
	height          int
	noticeLine      string
	chosenSessionID string
	isChannelEnded  bool
	isQuitting      bool
}

func newFullScreenListHost(list listModel, theme terminaltheme.Theme) *fullScreenListHost {
	return &fullScreenListHost{list: list, theme: theme, width: 80, height: 24}
}

// Init starts the list.
func (host *fullScreenListHost) Init() tea.Cmd {
	return host.list.Init()
}

// Update handles the list's outcomes and forwards everything else to it.
func (host *fullScreenListHost) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		host.width, host.height = typed.Width, typed.Height
		return host, host.list.Update(tea.WindowSizeMsg{Width: typed.Width, Height: host.listHeight()})
	case sessionpicker.SessionChosenMsg:
		host.chosenSessionID = typed.SessionID
		host.isQuitting = true
		return host, tea.Quit
	case sessionpicker.PickerClosedMsg, connectorlist.ListClosedMsg:
		host.isQuitting = true
		return host, tea.Quit
	case launcherNoticeMsg:
		host.showNotice(typed.level, typed.text)
		return host, host.list.Update(tea.WindowSizeMsg{Width: host.width, Height: host.listHeight()})
	case channelEndedMsg:
		if host.isQuitting {
			return host, nil
		}
		host.isChannelEnded = true
		return host, tea.Quit
	}
	return host, host.list.Update(message)
}

// View draws the list on the alt screen.
func (host *fullScreenListHost) View() tea.View {
	content := host.list.View()
	if host.noticeLine != "" {
		content = host.noticeLine + "\n" + content
	}
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func (host *fullScreenListHost) listHeight() int {
	if host.noticeLine != "" {
		return max(host.height-1, 1)
	}
	return host.height
}

// showNotice keeps the newest notice: one line without control characters,
// cut at the contract's length.
func (host *fullScreenListHost) showNotice(level string, text string) {
	shown := terminaltheme.TruncateCodePoints(terminaltheme.ReplaceControlCharacters(text), maxNoticeCodePoints, "…")
	symbol := host.theme.Secondary.Render(host.theme.Symbols.Queued)
	switch level {
	case "warning":
		symbol = host.theme.Warning.Render(host.theme.Symbols.Warning)
	case "error":
		symbol = host.theme.Error.Render(host.theme.Symbols.Failed)
	}
	host.noticeLine = terminaltheme.TruncateToWidth(" "+symbol+" "+strings.TrimSpace(shown), max(host.width, 1), host.theme.Symbols.Ellipsis)
}

// noRefresh is the refresher of a mode without a session to read again.
type noRefresh struct{}

// RequestRefresh does nothing.
func (noRefresh) RequestRefresh() {}

// newLaunchTheme builds the theme for stdout and the locale.
func newLaunchTheme(stdout *os.File) terminaltheme.Theme {
	profile := terminaltheme.TerminalColorProfile(colorprofile.Detect(stdout, os.Environ()), os.LookupEnv)
	return terminaltheme.NewTheme(profile, terminaltheme.IsUnicodeLocale(os.LookupEnv))
}

// sessionsHeading names the command and the session Flow type.
func sessionsHeading(launch launchcontract.LaunchDocument, theme terminaltheme.Theme) string {
	parts := make([]string, 0, 2)
	if launch.Command.Harness != "" {
		parts = append(parts, launch.Command.Harness)
	}
	parts = append(parts, launch.Session.FlowType)
	return strings.Join(parts, theme.Symbols.Separator)
}
