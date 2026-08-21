// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/superdurable/dex/cli/internal/command"
	"github.com/superdurable/dex/cli/internal/dev"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		command.WriteError(os.Stderr, err)
		os.Exit(command.ExitCode(err))
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "dev":
		return dev.Execute(ctx, args[1:], os.Stdout, os.Stderr, version)
	case "version", "--version", "-v":
		fmt.Fprintf(os.Stdout, "dexcli %s (commit %s, built %s)\n", version, commit, date)
		return nil
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		return command.NewApp(os.Stdin, os.Stdout, os.Stderr).Execute(ctx, args)
	}
}

func printUsage(output *os.File) {
	fmt.Fprintln(output, "Usage: dexcli <command>")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  dev       Start a local Dex development environment")
	fmt.Fprintln(output, "  health    Check Dex FlowService health")
	fmt.Fprintln(output, "  flow      Search, inspect, watch, stop, or reset Flows")
	fmt.Fprintln(output, "  api       List, describe, or call FlowService RPCs")
	fmt.Fprintln(output, "  version   Print version information")
}
