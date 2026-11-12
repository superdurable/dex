// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package agentsterminal runs the hidden `dexcli agents-tui` command. This
// version only plays a demo session.
package agentsterminal

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/superdurable/dex/cli/internal/agentsterminal/chatscreen"
	"github.com/superdurable/dex/cli/internal/agentsterminal/demosession"
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
// terminal.
func Execute(ctx context.Context, args []string, stdin *os.File, stdout *os.File, stderr io.Writer) error {
	flags := flag.NewFlagSet("dexcli agents-tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	isDemo := flags.Bool("demo", false, "play a scripted demo session")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("agents-tui: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("agents-tui: unexpected arguments: %v", flags.Args())
	}
	if !*isDemo {
		return errors.New("agents-tui: only --demo is available in this version")
	}
	if !term.IsTerminal(stdin.Fd()) || !term.IsTerminal(stdout.Fd()) {
		return errors.New("agents-tui: --demo needs a terminal on stdin and stdout")
	}
	return runDemo(ctx, stdin, stdout)
}

// interruptedExitCode is the shell convention for a SIGINT exit.
const interruptedExitCode = 130

func runDemo(ctx context.Context, stdin *os.File, stdout *os.File) error {
	programContext, cancelProgram := context.WithCancel(ctx)
	defer cancelProgram()
	environment := os.Environ()
	profile := terminaltheme.TerminalColorProfile(colorprofile.Detect(stdout, environment), os.LookupEnv)
	theme := terminaltheme.NewTheme(profile, terminaltheme.IsUnicodeLocale(os.LookupEnv))
	demo := demosession.NewDemoSession(demosession.DemoSessionConfig{Pace: 1})
	screen := chatscreen.NewChatScreen(programContext, chatscreen.ChatScreenConfig{
		Source:              demo,
		Theme:               theme,
		PrintScrollback:     chatscreen.NewScrollbackPrinter(profile),
		SessionCommands:     demosession.DemoSlashCommands(),
		IsTerminalReflowing: isTerminalReflowing(os.LookupEnv),
		ShowsStartupLogo:    true,
	})
	program := tea.NewProgram(
		screen,
		tea.WithContext(programContext),
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
		tea.WithEnvironment(environment),
		tea.WithColorProfile(profile),
		tea.WithFilter(chatscreen.NewResizeFilter(screen, stdout)),
	)
	finalModel, err := program.Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return &ExitError{Code: interruptedExitCode}
		}
		if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("agents-tui: run the terminal program: %w", err)
	}
	finalScreen, isChatScreen := finalModel.(*chatscreen.ChatScreen)
	if !isChatScreen {
		return fmt.Errorf("agents-tui: terminal program returned %T", finalModel)
	}
	if code := finalScreen.ExitCode(); code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// isTerminalReflowing reports whether the terminal rewraps lines when it
// narrows. Real xterm and the Linux console truncate them instead.
func isTerminalReflowing(lookupEnv func(string) (string, bool)) bool {
	if _, isXterm := lookupEnv("XTERM_VERSION"); isXterm {
		return false
	}
	terminalName, _ := lookupEnv("TERM")
	return terminalName != "linux"
}
