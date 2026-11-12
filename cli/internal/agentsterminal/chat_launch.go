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
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/grpclog"

	"github.com/superdurable/dex/agents/go/core/remotesession"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/cli/internal/agentsterminal/chatscreen"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/remotesessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionlisting"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/sdk-go/dex"
)

// supportedModes are the modes this dexcli runs.
var supportedModes = launchcontract.SupportedModes{
	1: {launchcontract.ModeChat, launchcontract.ModeSessions, launchcontract.ModeConnectors},
}

// The exit codes of the contract.
const (
	exitCodeFailure    = 1
	exitCodeInvalid    = 2
	exitCodeTerminated = 143
	blobCacheBytes     = 64 << 20
)

// runChannelHandoff runs the launch document's mode for the launcher on
// channelFD.
func runChannelHandoff(ctx context.Context, channelFD int, stdin *os.File, stdout *os.File) error {
	channel, err := openLauncherChannel(channelFD)
	if err != nil {
		return &ExitError{Code: exitCodeInvalid}
	}
	defer func() {
		// The launcher reads EOF; a second close changes nothing.
		if closeErr := channel.Close(); closeErr != nil {
			debugLog("close the channel", closeErr)
		}
	}()
	// One signal receiver covers the whole handoff. A signal before the chat
	// runs stops the wait for the launch line and decides the exit code.
	signals := newTerminalSignals(stdin, func() {
		if closeErr := channel.Close(); closeErr != nil {
			debugLog("close the channel after a signal", closeErr)
		}
	})
	signals.Start()
	defer signals.Stop()
	line, err := channel.ReadLaunchLine()
	if code := signals.EarlyExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	if err != nil {
		return &ExitError{Code: exitCodeInvalid}
	}
	launch, connection, rejection := runLaunchChecks(line, os.LookupEnv,
		func() *launchcontract.LaunchRejection {
			var rejection *launchcontract.LaunchRejection
			signals.HoldTerminalMode(func() { rejection = checkTerminal(realTerminalModeCalls, stdin.Fd(), stdout.Fd()) })
			return rejection
		}, newDexConnection)
	if rejection == nil {
		defer connection.close()
	}
	if code := signals.EarlyExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	if rejection != nil {
		if sendErr := channel.SendLaunchRejected(rejection); sendErr != nil {
			debugLog("send launch_rejected", sendErr)
		}
		return &ExitError{Code: rejection.ExitCode}
	}
	restoreStderr, err := silenceStderr()
	if err != nil {
		return fmt.Errorf("agents-tui: redirect stderr: %w", err)
	}
	defer restoreStderr()
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(discardWriter{}, discardWriter{}, discardWriter{}))
	// The mode's own signal handling decides how SIGINT, SIGTERM, and SIGHUP
	// end it; the caller's signal context would kill it without the exit code.
	modeContext := context.WithoutCancel(ctx)
	switch launch.Mode {
	case launchcontract.ModeSessions:
		return runSessionsMode(modeContext, channel, launch, connection, signals, stdin, stdout)
	case launchcontract.ModeConnectors:
		return runConnectorsMode(modeContext, channel, launch, signals, stdin, stdout)
	}
	return runChat(modeContext, channel, launch, connection, signals, stdin, stdout)
}

// dexConnection is the Dex client and its blob cache. flow is nil in
// connectors mode.
type dexConnection struct {
	client      *dex.Client
	flow        *remotesession.RemoteSessionFlow
	removeCache func()
}

func (connection dexConnection) close() {
	if closeErr := connection.client.Close(); closeErr != nil {
		debugLog("close the Dex client", closeErr)
	}
	connection.removeCache()
}

// newDexConnection builds the client before ready, so a bad credential or
// TLS setting is a launch rejection rather than text on the terminal.
func newDexConnection(launch launchcontract.LaunchDocument) (dexConnection, *launchcontract.LaunchRejection) {
	client, flow, removeCache, failure := newDexClient(launch)
	if failure != nil {
		return dexConnection{}, failure
	}
	return dexConnection{client: client, flow: flow, removeCache: removeCache}, nil
}

// runLaunchChecks runs the contract's launch checks in their order, the
// terminal last, and only then sets up the Dex client, so a setup failure
// never hides a check the contract orders first.
func runLaunchChecks(
	line []byte,
	lookupEnv func(string) (string, bool),
	checkTerminal func() *launchcontract.LaunchRejection,
	setUpDex func(launchcontract.LaunchDocument) (dexConnection, *launchcontract.LaunchRejection),
) (launchcontract.LaunchDocument, dexConnection, *launchcontract.LaunchRejection) {
	launch, rejection := launchcontract.CheckLaunchLine(line, supportedModes, lookupEnv)
	if rejection == nil {
		rejection = checkTerminal()
	}
	if rejection != nil {
		return launchcontract.LaunchDocument{}, dexConnection{}, rejection
	}
	connection, rejection := setUpDex(launch)
	return launch, connection, rejection
}

// openLauncherChannel takes the inherited socket fd. It sets close-on-exec
// first, so no child dexcli starts inherits it.
func openLauncherChannel(channelFD int) (*launcherchannel.LauncherChannel, error) {
	if channelFD < 0 {
		return nil, errors.New("--channel-fd must not be negative")
	}
	syscall.CloseOnExec(channelFD)
	var status unix.Stat_t
	if err := unix.Fstat(channelFD, &status); err != nil || status.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return nil, fmt.Errorf("fd %d is not a socket", channelFD)
	}
	file := os.NewFile(uintptr(channelFD), "agents-tui-channel")
	connection, err := net.FileConn(file)
	if closeErr := file.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("open the channel socket: %w", err)
	}
	return launcherchannel.NewLauncherChannel(connection, launcherchannel.ContractRequestTimeouts()), nil
}

// terminalModeCalls are the terminal calls of the launch check.
type terminalModeCalls struct {
	isTerminal func(fd uintptr) bool
	getSize    func(fd uintptr) (int, int, error)
	makeRaw    func(fd uintptr) (*term.State, error)
	restore    func(fd uintptr, state *term.State) error
}

var realTerminalModeCalls = terminalModeCalls{
	isTerminal: term.IsTerminal, getSize: term.GetSize, makeRaw: term.MakeRaw, restore: term.Restore,
}

// checkTerminal checks raw mode and the window size, and leaves the
// terminal mode as it found it. Run it under HoldTerminalMode.
func checkTerminal(calls terminalModeCalls, stdin uintptr, stdout uintptr) *launchcontract.LaunchRejection {
	if !calls.isTerminal(stdin) || !calls.isTerminal(stdout) {
		return launchcontract.TerminalFailed("stdin and stdout must be a terminal")
	}
	if _, _, err := calls.getSize(stdout); err != nil {
		return launchcontract.TerminalFailed("the window size is not readable")
	}
	state, err := calls.makeRaw(stdin)
	if err != nil {
		return launchcontract.TerminalFailed("raw mode is not available")
	}
	if err := calls.restore(stdin, state); err != nil {
		return launchcontract.TerminalFailed("the terminal mode could not be restored")
	}
	return nil
}

func runChat(
	ctx context.Context,
	channel *launcherchannel.LauncherChannel,
	launch launchcontract.LaunchDocument,
	connection dexConnection,
	signals *terminalSignals,
	stdin *os.File,
	stdout *os.File,
) error {
	chatContext, cancelChat := context.WithCancel(ctx)
	defer cancelChat()
	workingDirectory, err := os.Getwd()
	if err != nil {
		workingDirectory = "/"
	}
	home, _ := os.LookupEnv("HOME")
	channel.Start(launch.Methods)
	pickerRequests := make(chan struct{}, 1)
	feed := remotesessionfeed.NewRemoteSessionFeed(remotesessionfeed.RemoteSessionFeedConfig{
		Sessions:         remotesession.NewRemoteSessionClient(connection.client, connection.flow),
		Requests:         channel,
		Launch:           launch,
		HomeDirectory:    home,
		WorkingDirectory: workingDirectory,
		Debugf:           debugLog,
		OpenSessionPicker: func() {
			select {
			case pickerRequests <- struct{}{}:
			default:
			}
		},
	})
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		if runErr := feed.Run(chatContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
			debugLog("session feed", runErr)
		}
	}()
	environment := os.Environ()
	profile := terminaltheme.TerminalColorProfile(colorprofile.Detect(stdout, environment), os.LookupEnv)
	theme := terminaltheme.NewTheme(profile, terminaltheme.IsUnicodeLocale(os.LookupEnv))
	screen := chatscreen.NewChatScreen(chatContext, chatscreen.ChatScreenConfig{
		Source:          feed,
		Theme:           theme,
		SessionCommands: feed.SlashCommands(),
	})
	output := newFirstFrameWriter(stdout, func() {
		if sendErr := channel.SendReady(); sendErr != nil {
			debugLog("send ready", sendErr)
		}
	})
	newSessionHint := ""
	if launch.HasMethod(launchcontract.MethodStartSession) {
		newSessionHint = "start a new one with /new"
	}
	withPicker := newChatSessionPicker(chatContext, screen, chatSessionPickerConfig{
		Sessions:         sessionlisting.NewSessionLister(connection.client, launch.Session.FlowType),
		Source:           feed,
		CurrentSessionID: feed.SessionID,
		Theme:            theme,
		Heading:          sessionsHeading(launch, theme),
		NewSessionHint:   newSessionHint,
		PickerRequests:   pickerRequests,
	})
	frames := newReadyReporter(withPicker, output, feed)
	program := tea.NewProgram(
		frames,
		tea.WithContext(chatContext),
		tea.WithInput(stdin),
		tea.WithOutput(output),
		tea.WithEnvironment(environment),
		tea.WithColorProfile(profile),
		tea.WithoutSignalHandler(),
	)
	signals.Attach(program)
	_, runErr := program.Run()
	cancelChat()
	<-feedDone
	if summary := screen.ExitSummary(); summary != "" {
		if _, writeErr := fmt.Fprintln(stdout, summary); writeErr != nil {
			debugLog("print the exit summary", writeErr)
		}
	}
	if code := frames.signalExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return fmt.Errorf("agents-tui: run the terminal program: %w", runErr)
	}
	if code := feed.ExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// newDexClient connects with the launch document's address, TLS, and auth.
// The blob cache lives in a private temporary directory removed at exit.
// Every failure is dex_setup_failed.
func newDexClient(launch launchcontract.LaunchDocument) (*dex.Client, *remotesession.RemoteSessionFlow, func(), *launchcontract.LaunchRejection) {
	invalid := launchcontract.DexSetupFailed
	var flow *remotesession.RemoteSessionFlow
	var flows []dex.Flow
	if launch.Mode != launchcontract.ModeConnectors {
		var err error
		flow, err = remotesession.NewRemoteSessionFlow(launch.Session.FlowType)
		if err != nil {
			return nil, nil, nil, invalid("The session Flow type is not valid.")
		}
		flows = []dex.Flow{flow}
	}
	registry, err := dex.NewRegistry(flows)
	if err != nil {
		return nil, nil, nil, invalid("The session Flow type is not valid.")
	}
	credential, err := launchCredential(launch.Dex.Auth)
	if err != nil {
		variable := launchcontract.RequiredAuthVariable(launch.Dex.Auth)
		return nil, nil, nil, invalid(fmt.Sprintf("The credential that %s names is not usable for dex.auth mode %s.", variable, launch.Dex.Auth.Mode))
	}
	options := dex.ClientOptions{FlowServiceAddress: launch.Dex.ServerAddress, Credential: credential}
	if launch.Dex.TLS.IsEnabled {
		options.FlowServiceTLS = &dex.FlowServiceTLSOptions{}
		if launch.Dex.TLS.CAFile != nil {
			roots, readErr := os.ReadFile(*launch.Dex.TLS.CAFile)
			if readErr != nil {
				return nil, nil, nil, invalid("The TLS CA file of dex.tls cannot be read.")
			}
			options.FlowServiceTLS.AdditionalRootCAsPEM = roots
		}
		if launch.Dex.TLS.ServerName != nil {
			options.FlowServiceTLS.ServerName = *launch.Dex.TLS.ServerName
		}
	}
	cacheDirectory, err := os.MkdirTemp("", "dexcli-agents-tui-")
	if err != nil {
		return nil, nil, nil, invalid("A private cache directory cannot be created.")
	}
	removeCache := func() {
		if removeErr := os.RemoveAll(cacheDirectory); removeErr != nil {
			debugLog("remove the blob cache", removeErr)
		}
	}
	silentLogger := slog.New(slog.DiscardHandler)
	options.Logger = silentLogger
	cache, err := blobcache.New(&blobcache.Config{Dir: cacheDirectory, MaxBytes: blobCacheBytes, Logger: silentLogger})
	if err != nil {
		removeCache()
		return nil, nil, nil, invalid("The private blob cache cannot be opened.")
	}
	client, err := dex.NewClient(registry, cache, options)
	if err != nil {
		removeCache()
		return nil, nil, nil, invalid("The Dex connection settings of dex are not valid.")
	}
	return client, flow, removeCache, nil
}

// launchCredential reads the one auth variable dex.auth names.
func launchCredential(auth launchcontract.DexAuth) (dex.Credential, error) {
	value, _ := os.LookupEnv(launchcontract.RequiredAuthVariable(auth))
	switch {
	case auth.Mode == launchcontract.AuthModeBearer && auth.Source == "file":
		return dex.FileCredential(value)
	case auth.Mode == launchcontract.AuthModeBearer:
		return staticCredential{token: value}, nil
	case auth.Mode == launchcontract.AuthModeAPIKey:
		return dex.APIKeyCredential(auth.TokenURL, auth.ServerID, value)
	}
	return nil, nil
}

// staticCredential sends one bearer token.
type staticCredential struct {
	token string
}

func (credential staticCredential) Token(context.Context) (string, error) {
	return credential.token, nil
}

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

// silenceStderr points fd 2 at /dev/null while the chat owns the terminal, so
// no library writes over the screen. Library output never reaches the debug
// log, because it may hold user text.
func silenceStderr() (func(), error) {
	target, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	saved, err := unix.Dup(2)
	if err != nil {
		return nil, errors.Join(err, target.Close())
	}
	unix.CloseOnExec(saved)
	if err := unix.Dup2(int(target.Fd()), 2); err != nil {
		return nil, errors.Join(err, target.Close(), unix.Close(saved))
	}
	return func() {
		restoreErr := unix.Dup2(saved, 2)
		closeErr := errors.Join(unix.Close(saved), target.Close())
		if restoreErr != nil || closeErr != nil {
			debugLog("restore stderr", errors.Join(restoreErr, closeErr))
		}
	}, nil
}

// sessionRefresher reads the session again after the chat resumes.
type sessionRefresher interface {
	RequestRefresh()
}

// readyReporter wraps the chat screen. It marks the first frame with a
// window size for the output, which sends ready once that frame is written,
// quits on an external signal, and suspends on the event loop for SIGTSTP.
type readyReporter struct {
	screen    tea.Model
	output    *firstFrameWriter
	refresher sessionRefresher
	hasSize   bool
	mutex     sync.Mutex
	exitCode  int
}

type externalSignalMsg struct {
	exitCode int
}

func newReadyReporter(screen tea.Model, output *firstFrameWriter, refresher sessionRefresher) *readyReporter {
	return &readyReporter{screen: screen, output: output, refresher: refresher}
}

// Init starts the chat screen.
func (reporter *readyReporter) Init() tea.Cmd {
	return reporter.screen.Init()
}

// Update forwards every message to the chat screen.
func (reporter *readyReporter) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		reporter.hasSize = true
	case externalSignalMsg:
		reporter.mutex.Lock()
		reporter.exitCode = typed.exitCode
		reporter.mutex.Unlock()
		return reporter, tea.Quit
	case suspendRequestedMsg:
		return reporter, tea.Exec(typed.command, func(error) tea.Msg { return resumedMsg{} })
	case resumedMsg:
		reporter.refresher.RequestRefresh()
		return reporter, nil
	}
	screen, command := reporter.screen.Update(message)
	reporter.screen = screen
	return reporter, command
}

// View draws the chat screen. The renderer writes a sized frame after View
// returns, and that write sends ready.
func (reporter *readyReporter) View() tea.View {
	view := reporter.screen.View()
	if reporter.hasSize {
		reporter.output.markFrameRendered()
	}
	return view
}

func (reporter *readyReporter) signalExitCode() int {
	reporter.mutex.Lock()
	defer reporter.mutex.Unlock()
	return reporter.exitCode
}
