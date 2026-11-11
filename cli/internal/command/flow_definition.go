// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	flowDefinitionUsage       = "Usage: dexcli flow-definition <upload|list> [flags]"
	flowDefinitionUploadUsage = "Usage: dexcli flow-definition upload <path>... [--force] [global flags]"
	flowDefinitionListUsage   = "Usage: dexcli flow-definition list [global flags]"
	// maxFlowDefinitionUploadItems matches the server's per-request item limit.
	maxFlowDefinitionUploadItems = 256
	// maxFlowDefinitionUploadBytes keeps each request below the default 32 MiB gRPC message limit.
	maxFlowDefinitionUploadBytes = 16 << 20
)

func (a *App) executeFlowDefinition(ctx context.Context, args []string, options options) error {
	if len(args) == 0 {
		return newUsageError("flow-definition", errors.New("expected subcommand upload or list"))
	}
	switch args[0] {
	case "upload":
		return a.executeFlowDefinitionUpload(ctx, args[1:], options)
	case "list":
		return a.executeFlowDefinitionList(ctx, args[1:], options)
	case "help", "--help", "-h":
		fmt.Fprintln(a.stdout, flowDefinitionUsage)
		fmt.Fprintln(a.stdout)
		fmt.Fprintln(a.stdout, "Subcommands:")
		fmt.Fprintln(a.stdout, "  upload  Store Flow Definition Graph 2.0 JSON files or directories on the server")
		fmt.Fprintln(a.stdout, "  list    List the Flow types that have a stored Flow definition")
		return nil
	default:
		return newUsageError("flow-definition", fmt.Errorf("unknown subcommand %q", args[0]))
	}
}

// executeFlowDefinitionUpload reads files with the Go SDK rules and prints one outcome per Flow type.
func (a *App) executeFlowDefinitionUpload(ctx context.Context, args []string, options options) error {
	flags := a.newAdminFlagSet("dexcli flow-definition upload")
	isForced := flags.Bool("force", false, "replace every different stored definition, even one with a later generatedAt")
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseFlowDefinitionFlags(flags, args, flowDefinitionUploadUsage, options); isHelp || err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return newUsageError("flow-definition upload", errors.New("expected at least one file or directory"))
	}
	skippedFileLogger := slog.New(slog.NewTextHandler(a.stderr, nil))
	documents, err := dex.ReadFlowDefinitionFiles(flags.Args(), skippedFileLogger)
	if err != nil {
		return newUsageError("flow-definition upload", err)
	}
	if len(documents) == 0 {
		return newOperationError("flow-definition upload", errors.New("no .json files found"))
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		outcomes := make([]any, 0, len(documents))
		for _, batch := range splitFlowDefinitionUploads(documents) {
			response, err := client.service.UploadFlowDefinitions(callCtx, &dexpb.UploadFlowDefinitionsRequest{
				Definitions: batch,
				Uploader:    "dexcli/" + a.artifactVersion,
				Force:       *isForced,
			})
			if err != nil {
				if len(outcomes) > 0 {
					if writeErr := writeOutput(a.stdout, options.output, map[string]any{"definitions": outcomes}); writeErr != nil {
						return errors.Join(newOperationError("flow-definition upload", err), writeErr)
					}
				}
				return newOperationError("flow-definition upload", err)
			}
			for _, result := range response.GetResults() {
				outcomes = append(outcomes, flowDefinitionUploadOutcome(result))
			}
		}
		return writeOutput(a.stdout, options.output, map[string]any{"definitions": outcomes})
	})
}

// executeFlowDefinitionList pages through every stored Flow type.
func (a *App) executeFlowDefinitionList(ctx context.Context, args []string, options options) error {
	flags := a.newAdminFlagSet("dexcli flow-definition list")
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseFlowDefinitionFlags(flags, args, flowDefinitionListUsage, options); isHelp || err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return newUsageError("flow-definition list", fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		flowTypes := make([]any, 0)
		nextPageToken := ""
		for {
			response, err := client.service.ListFlowDefinitions(callCtx, &dexpb.ListFlowDefinitionsRequest{
				NextPageToken: nextPageToken,
			})
			if err != nil {
				return newOperationError("flow-definition list", err)
			}
			for _, flowType := range response.GetFlowTypes() {
				flowTypes = append(flowTypes, flowType)
			}
			nextPageToken = response.GetNextPageToken()
			if nextPageToken == "" {
				break
			}
		}
		return writeOutput(a.stdout, options.output, map[string]any{"flowTypes": flowTypes})
	})
}

// parseFlowDefinitionFlags accepts flags before or after positional paths and prints help on stdout.
func (a *App) parseFlowDefinitionFlags(flags *flag.FlagSet, args []string, usage string, options options) (bool, error) {
	operation := strings.TrimPrefix(flags.Name(), "dexcli ")
	if err := flags.Parse(interspersedArgs(flags, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.stdout, usage)
			fmt.Fprintln(a.stdout)
			fmt.Fprintln(a.stdout, "Flags:")
			flags.SetOutput(a.stdout)
			flags.PrintDefaults()
			return true, nil
		}
		return false, newUsageError(operation, err)
	}
	if err := options.validate(); err != nil {
		return false, newUsageError(operation, err)
	}
	return false, nil
}

func splitFlowDefinitionUploads(documents []dex.FlowDefinitionDocument) [][]*dexpb.FlowDefinitionUpload {
	var batches [][]*dexpb.FlowDefinitionUpload
	var batch []*dexpb.FlowDefinitionUpload
	batchBytes := 0
	for _, document := range documents {
		isBatchFull := len(batch) == maxFlowDefinitionUploadItems ||
			(len(batch) > 0 && batchBytes+len(document.Content) > maxFlowDefinitionUploadBytes)
		if isBatchFull {
			batches = append(batches, batch)
			batch = nil
			batchBytes = 0
		}
		batch = append(batch, &dexpb.FlowDefinitionUpload{
			FlowType:    document.FlowType,
			Content:     document.Content,
			GeneratedAt: timestamppb.New(document.GeneratedAt.Truncate(time.Millisecond)),
		})
		batchBytes += len(document.Content)
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

func flowDefinitionUploadOutcome(result *dexpb.FlowDefinitionUploadResult) map[string]any {
	return map[string]any{
		"flowType":          result.GetFlowType(),
		"outcome":           strings.TrimPrefix(result.GetOutcome().String(), "FLOW_DEFINITION_UPLOAD_OUTCOME_"),
		"storedDigest":      result.GetStoredDigest(),
		"storedGeneratedAt": result.GetStoredGeneratedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}
}
