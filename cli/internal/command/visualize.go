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
	"os"
	"path/filepath"
	"strings"

	"github.com/superdurable/dex/cli/internal/flowviz"
)

type visualizeOptions struct {
	language   string
	output     string
	pythonPath string
}

func (a *App) executeVisualize(ctx context.Context, args []string) error {
	flags := newFlagSet("dexcli visualize", a.stderr)
	options := visualizeOptions{}
	flags.StringVar(&options.language, "language", "auto", "auto, go, or python")
	flags.StringVar(&options.output, "out", "", "output path prefix, or - for stdout")
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
	outputPath, err := writeVisualizationOutput(a.stdout, source, options, jsonData)
	if err != nil {
		return newOperationError("visualize", err)
	}
	if options.output != "-" {
		if err := writeOutput(a.stdout, "json", map[string]any{"valid": graph.Valid, "output": outputPath}); err != nil {
			return err
		}
	}
	if !graph.Valid {
		return newOperationError("visualize", fmt.Errorf("source has blocking diagnostics; partial JSON was written"))
	}
	return nil
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
	return nil
}

func writeVisualizationOutput(stdout io.Writer, source string, options visualizeOptions, jsonData []byte) (string, error) {
	if options.output == "-" {
		if _, err := stdout.Write(jsonData); err != nil {
			return "", fmt.Errorf("write stdout: %w", err)
		}
		return "-", nil
	}
	prefix := options.output
	if prefix == "" {
		extension := filepath.Ext(source)
		prefix = strings.TrimSuffix(source, extension) + ".flow"
	}
	path := prefix + ".json"
	if err := writeVisualizationFile(path, jsonData); err != nil {
		return "", err
	}
	return path, nil
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
	fmt.Fprintln(output, "  --out path-prefix|-                 JSON output prefix, or - for stdout")
	fmt.Fprintln(output, "  --python path                       Python 3.11+ interpreter")
}
