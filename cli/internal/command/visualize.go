// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/superdurable/dex/cli/internal/flowviz"
	"github.com/superdurable/dex/web"
	"github.com/superdurable/dex/web/assets"
)

type visualizeOptions struct {
	language    string
	json        bool
	openBrowser bool
	output      string
	pythonPath  string
}

func (a *App) executeVisualize(ctx context.Context, args []string) error {
	flags := newFlagSet("dexcli visualize", a.stderr)
	options := visualizeOptions{openBrowser: true}
	flags.StringVar(&options.language, "language", "auto", "auto, go, or python")
	flags.BoolVar(&options.json, "json", false, "write Flow Definition Graph JSON instead of opening Flow Rendering")
	flags.BoolVar(&options.openBrowser, "open", true, "open Flow Rendering in the default browser")
	flags.StringVar(&options.output, "out", "", "JSON output prefix, or - for stdout (requires --json)")
	flags.StringVar(&options.pythonPath, "python", "", "Python 3.11+ interpreter path")
	source, parseErr := parseVisualizeArgs(flags, args)
	if parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			printVisualizeUsage(a.stdout)
			return nil
		}
		return newUsageError("visualize", parseErr)
	}
	if err := validateVisualizeOptions(options); err != nil {
		return newUsageError("visualize", err)
	}
	graph, err := flowviz.Analyze(ctx, source, flowviz.AnalyzeOptions{
		Language:   options.language,
		PythonPath: options.pythonPath,
	})
	if err != nil {
		return newOperationError("visualize", err)
	}
	jsonData, err := flowviz.MarshalJSON(graph)
	if err != nil {
		return newOperationError("visualize", err)
	}
	if options.json {
		outputPath, outputErr := writeVisualizationOutput(a.stdout, options, jsonData)
		if outputErr != nil {
			return newOperationError("visualize", outputErr)
		}
		if outputPath != "-" {
			fmt.Fprintf(a.stdout, "Flow definition JSON: %s\n", outputPath)
		}
		if !graph.Valid {
			return newOperationError("visualize", fmt.Errorf("source has blocking diagnostics; partial JSON was written"))
		}
		return nil
	}
	return a.renderVisualization(ctx, graph.Valid, jsonData, options)
}

func parseVisualizeArgs(flags *flag.FlagSet, args []string) (string, error) {
	source := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		source = args[0]
		parseArgs = args[1:]
	}
	if err := flags.Parse(parseArgs); err != nil {
		return "", err
	}
	remaining := flags.Args()
	if source == "" && len(remaining) > 0 {
		source = remaining[0]
		remaining = remaining[1:]
	}
	if source == "" {
		return "", fmt.Errorf("SOURCE is required")
	}
	if len(remaining) > 0 {
		return "", fmt.Errorf("unexpected arguments: %v", remaining)
	}
	return source, nil
}

func validateVisualizeOptions(options visualizeOptions) error {
	switch options.language {
	case "", "auto", "go", "python":
	default:
		return fmt.Errorf("language must be auto, go, or python")
	}
	if !options.json && options.output != "" {
		return fmt.Errorf("--out requires --json")
	}
	return nil
}

func (a *App) renderVisualization(ctx context.Context, isValid bool, graph []byte, options visualizeOptions) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return newOperationError("visualize", fmt.Errorf("listen for Flow Rendering: %w", err))
	}
	server, err := web.NewFlowRenderingServer(&web.Config{BindAddress: "127.0.0.1"}, graph, assets.Files)
	if err != nil {
		closeErr := listener.Close()
		return newOperationError("visualize", errors.Join(fmt.Errorf("start Flow Rendering: %w", err), closeErr))
	}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()
	url := "http://" + listener.Addr().String() + "/rendering"
	if options.openBrowser {
		if err := a.openBrowser(url); err != nil {
			shutdownErr := shutdownVisualizationServer(server, serverErrors)
			return newOperationError("visualize", errors.Join(fmt.Errorf("open Flow Rendering: %w", err), shutdownErr))
		}
	}
	fmt.Fprintf(a.stdout, "Flow Rendering: %s\n", url)
	fmt.Fprintln(a.stdout, "Press Ctrl+C to stop Flow Rendering.")
	select {
	case serverErr := <-serverErrors:
		if serverErr != nil {
			return newOperationError("visualize", fmt.Errorf("serve Flow Rendering: %w", serverErr))
		}
	case <-ctx.Done():
		if err := shutdownVisualizationServer(server, serverErrors); err != nil {
			return newOperationError("visualize", err)
		}
	}
	if !isValid {
		return newOperationError("visualize", fmt.Errorf("source has blocking diagnostics; Flow Rendering showed the partial graph"))
	}
	return nil
}

func shutdownVisualizationServer(server *web.Server, serverErrors <-chan error) error {
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdownCtx)
	serverErr := <-serverErrors
	return errors.Join(shutdownErr, serverErr)
}

func writeVisualizationOutput(stdout io.Writer, options visualizeOptions, jsonData []byte) (string, error) {
	if options.output == "" || options.output == "-" {
		if _, err := stdout.Write(jsonData); err != nil {
			return "", fmt.Errorf("write stdout: %w", err)
		}
		return "-", nil
	}
	prefix := options.output
	path := prefix + ".json"
	if err := writeVisualizationFile(path, jsonData); err != nil {
		return "", err
	}
	return path, nil
}

func openVisualizationBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "linux":
		command = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("opening a browser is not supported on %s", runtime.GOOS)
	}
	if err := command.Start(); err != nil {
		return err
	}
	if err := command.Process.Release(); err != nil {
		return err
	}
	return nil
}

func writeVisualizationFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", directory, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func printVisualizeUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: dexcli visualize SOURCE [flags]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Flags:")
	fmt.Fprintln(output, "  --language auto|go|python           source language (default auto)")
	fmt.Fprintln(output, "  --json                              write Flow Definition Graph JSON instead of rendering")
	fmt.Fprintln(output, "  --open                              open Flow Rendering in the default browser (default true)")
	fmt.Fprintln(output, "  --out path-prefix|-                 JSON output prefix, or - for stdout (requires --json)")
	fmt.Fprintln(output, "  --python path                       Python 3.11+ interpreter")
}
