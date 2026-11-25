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
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/superdurable/dex/cli/internal/flowdefinitiondiff"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// breakingChangesErrorKind exits with status 3 so CI can tell breaking changes from a failed command.
const breakingChangesErrorKind = "breaking_changes"

// maxFlowDefinitionGetItems matches the server's per-request Flow type limit for GetFlowDefinitions.
const maxFlowDefinitionGetItems = 256

// repeatedPathFlag collects every value of a flag that may be passed more than once.
type repeatedPathFlag struct {
	paths *[]string
}

func (flag repeatedPathFlag) String() string {
	if flag.paths == nil {
		return ""
	}
	return strings.Join(*flag.paths, ",")
}

func (flag repeatedPathFlag) Set(value string) error {
	*flag.paths = append(*flag.paths, value)
	return nil
}

type flowDefinitionDiffSummary struct {
	Breaking   int `json:"breaking"`
	Risky      int `json:"risky"`
	Compatible int `json:"compatible"`
}

type flowDefinitionDiffReport struct {
	ComparedFlowTypes []string                    `json:"comparedFlowTypes"`
	Summary           flowDefinitionDiffSummary   `json:"summary"`
	Changes           []flowdefinitiondiff.Change `json:"changes"`
}

// executeFlowDefinitionDiff compares new FDG files with old files, or with the definitions stored on the server.
func (a *App) executeFlowDefinitionDiff(ctx context.Context, args []string, options options) error {
	flags := a.newAdminFlagSet("dexcli flow-definition diff")
	var oldPaths []string
	flags.Var(repeatedPathFlag{paths: &oldPaths}, "old", "old FDG JSON file or directory; repeatable; without it, read the old definitions from --server")
	isJSON := flags.Bool("json", false, "write the report as JSON instead of a table")
	isBreakingAllowed := flags.Bool("allow-breaking", false, "exit 0 even when breaking changes are found")
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseFlowDefinitionFlags(flags, args, flowDefinitionDiffUsage, options); isHelp || err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return newUsageError("flow-definition diff", errors.New("expected at least one new FDG file or directory"))
	}
	skippedFileLogger := slog.New(slog.NewTextHandler(a.stderr, nil))
	newDocuments, err := dex.ReadFlowDefinitionFiles(flags.Args(), skippedFileLogger)
	if err != nil {
		return newUsageError("flow-definition diff", err)
	}
	if len(newDocuments) == 0 {
		return newOperationError("flow-definition diff", errors.New("no new .json files found"))
	}
	newContents := make(map[string][]byte, len(newDocuments))
	for _, document := range newDocuments {
		newContents[document.FlowType] = document.Content
	}
	var oldContents map[string][]byte
	isOldFromServer := len(oldPaths) == 0
	if isOldFromServer {
		oldContents, err = a.fetchStoredFlowDefinitions(ctx, options, sortedKeys(newContents))
	} else {
		oldContents, err = readFlowDefinitionContents(oldPaths, skippedFileLogger)
	}
	if err != nil {
		return err
	}
	report, err := compareFlowDefinitionContents(oldContents, newContents, isOldFromServer)
	if err != nil {
		return newOperationError("flow-definition diff", err)
	}
	if *isJSON {
		err = writeOutput(a.stdout, "json", report)
	} else {
		err = writeFlowDefinitionDiffTable(a.stdout, report)
	}
	if err != nil {
		return err
	}
	if report.Summary.Breaking > 0 && !*isBreakingAllowed {
		return &Error{
			operation: "flow-definition diff",
			kind:      breakingChangesErrorKind,
			cause:     fmt.Errorf("found %d breaking changes; pass --allow-breaking to exit 0", report.Summary.Breaking),
		}
	}
	return nil
}

func readFlowDefinitionContents(paths []string, logger *slog.Logger) (map[string][]byte, error) {
	documents, err := dex.ReadFlowDefinitionFiles(paths, logger)
	if err != nil {
		return nil, newUsageError("flow-definition diff", err)
	}
	contents := make(map[string][]byte, len(documents))
	for _, document := range documents {
		contents[document.FlowType] = document.Content
	}
	return contents, nil
}

// fetchStoredFlowDefinitions returns the stored definitions of flowTypes; a Flow type the server lacks is left out.
func (a *App) fetchStoredFlowDefinitions(ctx context.Context, options options, flowTypes []string) (map[string][]byte, error) {
	contents := make(map[string][]byte, len(flowTypes))
	err := withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		for start := 0; start < len(flowTypes); start += maxFlowDefinitionGetItems {
			batch := flowTypes[start:min(start+maxFlowDefinitionGetItems, len(flowTypes))]
			if err := fetchFlowDefinitionBatch(callCtx, client, batch, contents); err != nil {
				return newOperationError("flow-definition diff", err)
			}
		}
		return nil
	})
	return contents, err
}

// fetchFlowDefinitionBatch retries one Flow type per request when the batch response would be too large.
func fetchFlowDefinitionBatch(ctx context.Context, client *flowService, flowTypes []string, contents map[string][]byte) error {
	response, err := client.service.GetFlowDefinitions(ctx, &dexpb.GetFlowDefinitionsRequest{FlowTypes: flowTypes})
	if status.Code(err) == codes.ResourceExhausted && len(flowTypes) > 1 {
		for _, flowType := range flowTypes {
			if err := fetchFlowDefinitionBatch(ctx, client, []string{flowType}, contents); err != nil {
				return err
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	for _, definition := range response.GetDefinitions() {
		contents[definition.GetInfo().GetFlowType()] = definition.GetContent()
	}
	return nil
}

// compareFlowDefinitionContents skips old-only Flow types when the old side came from the server.
func compareFlowDefinitionContents(oldContents map[string][]byte, newContents map[string][]byte, isOldFromServer bool) (flowDefinitionDiffReport, error) {
	report := flowDefinitionDiffReport{ComparedFlowTypes: []string{}, Changes: []flowdefinitiondiff.Change{}}
	for _, flowType := range sortedKeys(newContents) {
		oldContent, exists := oldContents[flowType]
		if !exists {
			report.Changes = append(report.Changes, flowdefinitiondiff.FlowTypeAdded(flowType))
			continue
		}
		oldGraph, err := flowdefinitiondiff.ParseGraph(oldContent)
		if err != nil {
			return report, fmt.Errorf("old definition of Flow type %q: %w", flowType, err)
		}
		newGraph, err := flowdefinitiondiff.ParseGraph(newContents[flowType])
		if err != nil {
			return report, fmt.Errorf("new definition of Flow type %q: %w", flowType, err)
		}
		report.ComparedFlowTypes = append(report.ComparedFlowTypes, flowType)
		report.Changes = append(report.Changes, flowdefinitiondiff.CompareGraphs(flowType, oldGraph, newGraph)...)
	}
	if !isOldFromServer {
		for _, flowType := range sortedKeys(oldContents) {
			if _, exists := newContents[flowType]; !exists {
				report.Changes = append(report.Changes, flowdefinitiondiff.FlowTypeRemoved(flowType))
			}
		}
	}
	flowdefinitiondiff.SortChanges(report.Changes)
	for _, change := range report.Changes {
		switch change.Severity {
		case flowdefinitiondiff.SeverityBreaking:
			report.Summary.Breaking++
		case flowdefinitiondiff.SeverityRisky:
			report.Summary.Risky++
		case flowdefinitiondiff.SeverityCompatible:
			report.Summary.Compatible++
		}
	}
	return report, nil
}

func sortedKeys(contents map[string][]byte) []string {
	keys := make([]string, 0, len(contents))
	for key := range contents {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeFlowDefinitionDiffTable(output io.Writer, report flowDefinitionDiffReport) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if len(report.Changes) > 0 {
		fmt.Fprintln(writer, "SEVERITY\tFLOW TYPE\tELEMENT\tNAME\tCHANGE")
		for _, change := range report.Changes {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", change.Severity, change.FlowType, change.Element, change.Name, change.Message)
		}
	} else {
		fmt.Fprintln(writer, "No changes.")
	}
	fmt.Fprintf(writer, "\n%d breaking, %d risky, %d compatible; compared %d Flow types.\n",
		report.Summary.Breaking, report.Summary.Risky, report.Summary.Compatible, len(report.ComparedFlowTypes))
	if err := writer.Flush(); err != nil {
		return newOperationError("output", err)
	}
	return nil
}
