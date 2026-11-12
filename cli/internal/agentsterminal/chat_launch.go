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
	"time"

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
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/sdk-go/dex"
)

// supportedModes are the modes this dexcli runs. The sessions and
// connectors pickers come later, so launchers use plain text for them.
var supportedModes = launchcontract.SupportedModes{1: {launchcontract.ModeChat}}

// The exit codes of the contract.
const (
	exitCodeFailure      = 1
	exitCodeInvalid      = 2
	exitCodeTerminated   = 143
	firstFrameFlushDelay = 50 * time.Millisecond
	blobCacheBytes       = 64 << 20
)

// runChannelChat runs the chat for the launcher on channelFD.
func runChannelChat(ctx context.Context, channelFD int, stdin *os.File, stdout *os.File) error {
	channel, err := openLauncherChannel(channelFD)
	if err != nil {
		return &ExitError{Code: exitCodeInvalid}
	}
	defer func() {
		// The launcher reads EOF; a second close changes nothing.
		if closeErr := channel.Close(); closeErr != nil {
			debugf("close the channel: %v", closeErr)
		}
	}()
	line, err := channel.ReadLaunchLine()
	if err != nil {
		return &ExitError{Code: exitCodeInvalid}
	}
	launch, rejection := launchcontract.CheckLaunchLine(line, supportedModes, os.LookupEnv)
	var connection dexConnection
	if rejection == nil {
		connection, rejection = newDexConnection(launch)
	}
	if rejection == nil {
		defer connection.close()
		rejection = checkTerminal(stdin, stdout)
	}
	if rejection != nil {
		if sendErr := channel.SendLaunchRejected(rejection); sendErr != nil {
			debugf("send launch_rejected: %v", sendErr)
		}
		return &ExitError{Code: rejection.ExitCode}
	}
	restoreStderr, err := silenceStderr()
	if err != nil {
		return fmt.Errorf("agents-tui: redirect stderr: %w", err)
	}
	defer restoreStderr()
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(discardWriter{}, discardWriter{}, discardWriter{}))
	return runChat(ctx, channel, launch, connection, stdin, stdout)
}

// dexConnection is the Dex client of the chat and its blob cache.
type dexConnection struct {
	client      *dex.Client
	flow        *remotesession.RemoteSessionFlow
	removeCache func()
}

func (connection dexConnection) close() {
	if closeErr := connection.client.Close(); closeErr != nil {
		debugf("close the Dex client: %v", closeErr)
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

// checkTerminal checks raw mode and the window size, and leaves the
// terminal mode as it found it.
func checkTerminal(stdin *os.File, stdout *os.File) *launchcontract.LaunchRejection {
	if !term.IsTerminal(stdin.Fd()) || !term.IsTerminal(stdout.Fd()) {
		return launchcontract.TerminalFailed("stdin and stdout must be a terminal")
	}
	if _, _, err := term.GetSize(stdout.Fd()); err != nil {
		return launchcontract.TerminalFailed("the window size is not readable")
	}
	state, err := term.MakeRaw(stdin.Fd())
	if err != nil {
		return launchcontract.TerminalFailed("raw mode is not available")
	}
	if err := term.Restore(stdin.Fd(), state); err != nil {
		return launchcontract.TerminalFailed("the terminal mode could not be restored")
	}
	return nil
}

func runChat(
	ctx context.Context,
	channel *launcherchannel.LauncherChannel,
	launch launchcontract.LaunchDocument,
	connection dexConnection,
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
	feed := remotesessionfeed.NewRemoteSessionFeed(remotesessionfeed.RemoteSessionFeedConfig{
		Sessions:         remotesession.NewRemoteSessionClient(connection.client, connection.flow),
		Requests:         channel,
		Launch:           launch,
		HomeDirectory:    home,
		WorkingDirectory: workingDirectory,
		Debugf:           debugf,
	})
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		if runErr := feed.Run(chatContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
			debugf("session feed: %v", runErr)
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
	frames := newReadyReporter(screen, func() {
		if sendErr := channel.SendReady(); sendErr != nil {
			debugf("send ready: %v", sendErr)
		}
	})
	program := tea.NewProgram(
		frames,
		tea.WithContext(chatContext),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithEnvironment(environment),
		tea.WithColorProfile(profile),
		tea.WithoutSignalHandler(),
	)
	signals := newTerminalSignals(program, stdin, frames, feed)
	signals.Start()
	_, runErr := program.Run()
	signals.Stop()
	cancelChat()
	<-feedDone
	if summary := screen.ExitSummary(); summary != "" {
		if _, writeErr := fmt.Fprintln(stdout, summary); writeErr != nil {
			debugf("print the exit summary: %v", writeErr)
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
// The blob cache lives in a private temporary directory removed at exit. A
// credential failure is auth_missing and any other failure invalid_launch.
func newDexClient(launch launchcontract.LaunchDocument) (*dex.Client, *remotesession.RemoteSessionFlow, func(), *launchcontract.LaunchRejection) {
	invalid := func(message string) *launchcontract.LaunchRejection {
		return &launchcontract.LaunchRejection{Code: launchcontract.RejectionInvalidLaunch, Message: message, ExitCode: exitCodeInvalid}
	}
	flow, err := remotesession.NewRemoteSessionFlow(launch.Session.FlowType)
	if err != nil {
		return nil, nil, nil, invalid("The session Flow type is not valid.")
	}
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	if err != nil {
		return nil, nil, nil, invalid("The session Flow type is not valid.")
	}
	credential, err := launchCredential(launch.Dex.Auth)
	if err != nil {
		variable := launchcontract.RequiredAuthVariable(launch.Dex.Auth)
		return nil, nil, nil, &launchcontract.LaunchRejection{
			Code:     launchcontract.RejectionAuthMissing,
			Message:  fmt.Sprintf("The credential that %s names is not usable for dex.auth mode %s.", variable, launch.Dex.Auth.Mode),
			ExitCode: exitCodeInvalid,
		}
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
			debugf("remove the blob cache: %v", removeErr)
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

// silenceStderr points fd 2 at the debug log or /dev/null while the chat owns
// the terminal, so no library writes over the screen.
func silenceStderr() (func(), error) {
	target, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if path := debugLogPath(); path != "" {
		target, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	}
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
			debugf("restore stderr: %v", errors.Join(restoreErr, closeErr))
		}
	}, nil
}

// readyReporter wraps the chat screen. It sends ready once the first frame
// with a window size is drawn, and quits on an external signal.
type readyReporter struct {
	screen   tea.Model
	onReady  func()
	hasSize  bool
	once     sync.Once
	mutex    sync.Mutex
	exitCode int
}

type externalSignalMsg struct {
	exitCode int
}

func newReadyReporter(screen tea.Model, onReady func()) *readyReporter {
	return &readyReporter{screen: screen, onReady: onReady}
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
	}
	screen, command := reporter.screen.Update(message)
	reporter.screen = screen
	return reporter, command
}

// View draws the chat screen and reports ready after the first sized frame.
func (reporter *readyReporter) View() tea.View {
	view := reporter.screen.View()
	if reporter.hasSize {
		reporter.once.Do(func() {
			// The renderer writes this frame right after View returns.
			time.AfterFunc(firstFrameFlushDelay, reporter.onReady)
		})
	}
	return view
}

func (reporter *readyReporter) signalExitCode() int {
	reporter.mutex.Lock()
	defer reporter.mutex.Unlock()
	return reporter.exitCode
}
