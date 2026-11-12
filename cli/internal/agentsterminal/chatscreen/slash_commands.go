// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"sort"
	"strings"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

const (
	helpCommandName    = "help"
	exitCommandName    = "exit"
	approveCommandName = "approve"
	steerCommandName   = "steer"
	dropCommandName    = "drop"
	planCommandName    = "plan"
	runCommandName     = "run"
	copyCommandName    = "copy"
)

// screenCommands are the commands the chat screen runs itself.
var screenCommands = []sessionfeed.SlashCommand{
	{Name: approveCommandName, Description: "open the waiting approvals and questions"},
	{Name: copyCommandName, Description: "copy the last reply to the clipboard"},
	{Name: dropCommandName, Description: "delete the last queued message"},
	{Name: exitCommandName, Description: "exit; the session stays in Dex"},
	{Name: helpCommandName, Description: "list keys and commands"},
	{Name: steerCommandName, Description: "steer the last queued message into the turn"},
}

// planCommands exist only for a Flow built with plan mode.
var planCommands = []sessionfeed.SlashCommand{
	{Name: planCommandName, Description: "ask for a plan with the next message"},
	{Name: runCommandName, Description: "approve and run the draft plan"},
}

// slashCommandRegistry holds the screen commands and the session commands,
// sorted by name.
type slashCommandRegistry struct {
	commands []sessionfeed.SlashCommand
}

func newSlashCommandRegistry(sessionCommands []sessionfeed.SlashCommand, isPlanModeAvailable bool) slashCommandRegistry {
	ownCommands := append([]sessionfeed.SlashCommand(nil), screenCommands...)
	if isPlanModeAvailable {
		ownCommands = append(ownCommands, planCommands...)
	}
	commands := append([]sessionfeed.SlashCommand(nil), ownCommands...)
	for _, command := range sessionCommands {
		for _, ownCommand := range append(screenCommands, planCommands...) {
			if command.Name == ownCommand.Name {
				panic("session commands must not redefine /" + command.Name)
			}
		}
		commands = append(commands, command)
	}
	sort.Slice(commands, func(left, right int) bool { return commands[left].Name < commands[right].Name })
	return slashCommandRegistry{commands: commands}
}

// Lookup returns the command with name.
func (registry slashCommandRegistry) Lookup(name string) (sessionfeed.SlashCommand, bool) {
	for _, command := range registry.commands {
		if command.Name == name {
			return command, true
		}
	}
	return sessionfeed.SlashCommand{}, false
}

// Has reports whether a command with name exists.
func (registry slashCommandRegistry) Has(name string) bool {
	_, exists := registry.Lookup(name)
	return exists
}

// Matching returns the commands whose names start with prefix.
func (registry slashCommandRegistry) Matching(prefix string) []sessionfeed.SlashCommand {
	var matches []sessionfeed.SlashCommand
	for _, command := range registry.commands {
		if strings.HasPrefix(command.Name, prefix) {
			matches = append(matches, command)
		}
	}
	return matches
}

// Commands returns every command.
func (registry slashCommandRegistry) Commands() []sessionfeed.SlashCommand {
	return registry.commands
}

// parsedSlashCommand is one input line that starts with a slash.
type parsedSlashCommand struct {
	name     string
	argument string
}

// parseSlashCommand splits "/name argument" and reports whether text is a
// slash command at all.
func parseSlashCommand(text string) (parsedSlashCommand, bool) {
	if !strings.HasPrefix(text, "/") {
		return parsedSlashCommand{}, false
	}
	body := strings.TrimPrefix(text, "/")
	name, argument, _ := strings.Cut(body, " ")
	return parsedSlashCommand{name: strings.TrimSpace(name), argument: strings.TrimSpace(argument)}, true
}

// completionPrefix returns the command prefix being typed, and whether the
// input is still at the command name: a slash, then no space.
func completionPrefix(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \n") {
		return "", false
	}
	return strings.TrimPrefix(text, "/"), true
}
