// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package agentsterminal runs the hidden `dexcli agents-tui` command: the
// chat terminal UI of agents CLI commands, under terminal UI contract 1.
package agentsterminal

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/superdurable/dex/cli/internal/agentsterminal/chatscreen"
	"github.com/superdurable/dex/cli/internal/agentsterminal/demosession"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// ExitError carries a non-zero exit code that needs no error message, such as
// a stopped Worker.
type ExitError struct {
	// Code is the process exit code.
	Code int
}

func (exitError *ExitError) Error() string {
	return fmt.Sprintf("agents terminal exited with code %d", exitError.Code)
}

// Execute runs `dexcli agents-tui` with args. stdin and stdout must be the
// terminal. dexcliVersion is reported by --describe.
func Execute(ctx context.Context, args []string, stdin *os.File, stdout *os.File, stderr io.Writer, dexcliVersion string) error {
	flags := flag.NewFlagSet("dexcli agents-tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	isDemo := flags.Bool("demo", false, "play a scripted demo session")
	isDescribe := flags.Bool("describe", false, "print the supported terminal UI contracts as JSON")
	channelFD := flags.Int("channel-fd", -1, "the launcher channel's socket fd")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &ExitError{Code: exitCodeInvalid}
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("agents-tui: unexpected arguments: %v", flags.Args())
	}
	switch {
	case *isDescribe:
		return printDescribe(stdout, dexcliVersion)
	case *channelFD >= 0:
		return runChannelChat(ctx, *channelFD, stdin, stdout)
	case !*isDemo:
		return errors.New("agents-tui: pass --channel-fd, --describe, or --demo")
	}
	if !term.IsTerminal(stdin.Fd()) || !term.IsTerminal(stdout.Fd()) {
		return errors.New("agents-tui: --demo needs a terminal on stdin and stdout")
	}
	return runDemo(ctx, stdin, stdout)
}

// interruptedExitCode is the shell convention for a SIGINT exit.
const interruptedExitCode = 130

// printDescribe writes the --describe document: one JSON line on stdout.
func printDescribe(stdout io.Writer, dexcliVersion string) error {
	encoded, err := json.Marshal(launchcontract.NewDescribeDocument(dexcliVersion, supportedModes))
	if err != nil {
		return fmt.Errorf("agents-tui: encode --describe: %w", err)
	}
	if _, err := stdout.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("agents-tui: write --describe: %w", err)
	}
	return nil
}

// debugLogVariable turns on the debug log, which holds no secret or text.
const debugLogVariable = "DEX_AGENTS_TUI_DEBUG"

// debugLogPath returns the debug log file, or "" when the log is off.
func debugLogPath() string {
	if value, _ := os.LookupEnv(debugLogVariable); value != "1" {
		return ""
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("dexcli-agents-tui-%d.log", os.Getpid()))
}

var debugMutex sync.Mutex

// debugf appends one line to the debug log. Callers pass only types,
// methods, IDs, and error codes.
func debugf(format string, arguments ...any) {
	path := debugLogPath()
	if path == "" {
		return
	}
	debugMutex.Lock()
	defer debugMutex.Unlock()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	line := time.Now().UTC().Format(time.RFC3339Nano) + " " + fmt.Sprintf(format, arguments...) + "\n"
	if home, isSet := os.LookupEnv("HOME"); isSet && home != "" {
		line = strings.ReplaceAll(line, home, "~")
	}
	// The debug log is best effort; a failed write must not end the chat.
	_, writeErr := file.WriteString(line)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return
	}
}

func runDemo(ctx context.Context, stdin *os.File, stdout *os.File) error {
	programContext, cancelProgram := context.WithCancel(ctx)
	defer cancelProgram()
	environment := os.Environ()
	profile := terminaltheme.TerminalColorProfile(colorprofile.Detect(stdout, environment), os.LookupEnv)
	theme := terminaltheme.NewTheme(profile, terminaltheme.IsUnicodeLocale(os.LookupEnv))
	demo := demosession.NewDemoSession(demosession.DemoSessionConfig{Pace: 1})
	screen := chatscreen.NewChatScreen(programContext, chatscreen.ChatScreenConfig{
		Source:           demo,
		Theme:            theme,
		SessionCommands:  demosession.DemoSlashCommands(),
		ShowsStartupLogo: true,
	})
	program := tea.NewProgram(
		screen,
		tea.WithContext(programContext),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithEnvironment(environment),
		tea.WithColorProfile(profile),
	)
	_, err := program.Run()
	if summary := screen.ExitSummary(); summary != "" {
		if _, writeErr := fmt.Fprintln(stdout, summary); writeErr != nil {
			return fmt.Errorf("agents-tui: print the exit summary: %w", writeErr)
		}
	}
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return &ExitError{Code: interruptedExitCode}
		}
		if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("agents-tui: run the terminal program: %w", err)
	}
	if code := screen.ExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}
