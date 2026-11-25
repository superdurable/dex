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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/superdurable/dex/cli/internal/flowdefinitiondiff"
	"github.com/superdurable/dex/cli/internal/flowviz"
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
	flags.Var(repeatedPathFlag{paths: &oldPaths}, "old", "old FDG JSON file or directory; repeatable. "+
		"Without it, the old definitions come from --server, which compares only the Flow types passed and cannot report a removed Flow type")
	isJSON := flags.Bool("json", false, "write the report as JSON instead of a table; same as --output json")
	isBreakingAllowed := flags.Bool("allow-breaking", false, "exit 0 even when breaking changes are found")
	if !options.isOutputSet {
		options.output = "table"
	}
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseFlowDefinitionFlags(flags, args, flowDefinitionDiffUsage, options); isHelp || err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return newUsageError("flow-definition diff", errors.New("expected at least one new FDG file or directory"))
	}
	outputFormat, err := flowDefinitionDiffOutputFormat(flags, options, *isJSON)
	if err != nil {
		return newUsageError("flow-definition diff", err)
	}
	newGraphs, err := readFlowDefinitionGraphs("new", flags.Args())
	if err != nil {
		return err
	}
	isOldFromServer := len(oldPaths) == 0
	var oldGraphs map[string]*flowviz.Graph
	if isOldFromServer {
		if len(newGraphs) == 0 {
			return newOperationError("flow-definition diff", errors.New("no new Flow definitions found"))
		}
		oldGraphs, err = a.fetchStoredFlowDefinitionGraphs(ctx, options, sortedKeys(newGraphs))
		if err != nil {
			return err
		}
		if len(oldGraphs) == 0 {
			return newOperationError("flow-definition diff", fmt.Errorf(
				"the server has no stored definition for any of the %d Flow types; upload them first or pass --old", len(newGraphs)))
		}
	} else {
		oldGraphs, err = readFlowDefinitionGraphs("old", oldPaths)
		if err != nil {
			return err
		}
		if len(oldGraphs) == 0 {
			return newOperationError("flow-definition diff", errors.New("no old Flow definitions found in the --old paths"))
		}
	}
	report := compareFlowDefinitionGraphs(oldGraphs, newGraphs, isOldFromServer)
	if outputFormat == "json" {
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

// flowDefinitionDiffOutputFormat returns "table" unless --json or an explicit root or command --output asks otherwise.
func flowDefinitionDiffOutputFormat(flags *flag.FlagSet, options options, isJSON bool) (string, error) {
	isOutputSet := options.isOutputSet
	flags.Visit(func(setFlag *flag.Flag) {
		if setFlag.Name == "output" {
			isOutputSet = true
		}
	})
	if isJSON && isOutputSet && options.output != "json" {
		return "", fmt.Errorf("--json conflicts with --output %s", options.output)
	}
	if isJSON {
		return "json", nil
	}
	return options.output, nil
}

// readFlowDefinitionGraphs reads every named file and every .json file under a named directory strictly.
// Unlike upload, it never skips a file: a gate must not pass on input it could not read.
// It returns a usage error for a missing path, and an operation error naming the side and file
// for an unreadable or unusable graph, or for two files with the same Flow type.
func readFlowDefinitionGraphs(side string, paths []string) (map[string]*flowviz.Graph, error) {
	files, err := collectFlowDefinitionGraphFiles(side, paths)
	if err != nil {
		return nil, err
	}
	graphs := make(map[string]*flowviz.Graph, len(files))
	fileByFlowType := make(map[string]string, len(files))
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, newOperationError("flow-definition diff", fmt.Errorf("%s definition file %s: %w", side, path, err))
		}
		graph, err := flowdefinitiondiff.ParseGraph(content)
		if err != nil {
			return nil, newOperationError("flow-definition diff", fmt.Errorf("%s definition file %s%s: %w",
				side, path, describeReadableFlowType(content), err))
		}
		if previous, exists := fileByFlowType[graph.Flow.Name]; exists {
			return nil, newOperationError("flow-definition diff", fmt.Errorf("%s definition files %s and %s both describe Flow type %q",
				side, previous, path, graph.Flow.Name))
		}
		fileByFlowType[graph.Flow.Name] = path
		graphs[graph.Flow.Name] = graph
	}
	return graphs, nil
}

// collectFlowDefinitionGraphFiles returns each distinct file once, sorted; directories contribute .json files in any letter case.
// A named path, or a symbolic link to a file inside a directory, is followed. It returns a usage
// error for a missing named path. It returns an operation error naming the side and path for a
// symbolic link inside a directory that is broken or points to a directory, which is not followed.
func collectFlowDefinitionGraphFiles(side string, paths []string) ([]string, error) {
	seen := make(map[string]bool)
	var files []string
	addFile := func(path string) error {
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if !seen[resolvedPath] {
			seen[resolvedPath] = true
			files = append(files, path)
		}
		return nil
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, newUsageError("flow-definition diff", err)
		}
		if !info.IsDir() {
			if err := addFile(path); err != nil {
				return nil, newUsageError("flow-definition diff", err)
			}
			continue
		}
		root, err := resolveSymbolicLinkRoot(path)
		if err != nil {
			return nil, newUsageError("flow-definition diff", err)
		}
		err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return addSymbolicLinkTarget(side, current, addFile)
			}
			if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(current), ".json") {
				return addFile(current)
			}
			return nil
		})
		if err != nil {
			return nil, newOperationError("flow-definition diff", err)
		}
	}
	sort.Strings(files)
	return files, nil
}

// resolveSymbolicLinkRoot resolves a named directory that is itself a link, because WalkDir does not descend into one.
func resolveSymbolicLinkRoot(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		return path, err
	}
	return filepath.EvalSymlinks(path)
}

// addSymbolicLinkTarget adds a link to a regular .json file and rejects links it cannot follow safely.
func addSymbolicLinkTarget(side string, linkPath string, addFile func(string) error) error {
	target, err := os.Stat(linkPath)
	switch {
	case err != nil:
		return fmt.Errorf("%s definition file %s: broken symbolic link: %w", side, linkPath, err)
	case target.IsDir():
		return fmt.Errorf("%s definition path %s: symbolic link to a directory is not followed; pass the directory itself", side, linkPath)
	case !strings.EqualFold(filepath.Ext(linkPath), ".json"):
		return nil
	case !target.Mode().IsRegular():
		return fmt.Errorf("%s definition file %s: symbolic link to a file that is not a regular file", side, linkPath)
	default:
		return addFile(linkPath)
	}
}

// describeReadableFlowType names the Flow type of a rejected file when its flow.name can still be read.
func describeReadableFlowType(content []byte) string {
	var header struct {
		Flow struct {
			Name string `json:"name"`
		} `json:"flow"`
	}
	if json.Unmarshal(content, &header) != nil || header.Flow.Name == "" {
		return ""
	}
	return fmt.Sprintf(" (Flow type %q)", header.Flow.Name)
}

// fetchStoredFlowDefinitionGraphs returns the stored definitions of flowTypes; a Flow type the server lacks is left out.
func (a *App) fetchStoredFlowDefinitionGraphs(ctx context.Context, options options, flowTypes []string) (map[string]*flowviz.Graph, error) {
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
	if err != nil {
		return nil, err
	}
	graphs := make(map[string]*flowviz.Graph, len(contents))
	for _, flowType := range sortedKeys(contents) {
		graph, err := flowdefinitiondiff.ParseGraph(contents[flowType])
		if err != nil {
			return nil, newOperationError("flow-definition diff", fmt.Errorf("old definition of Flow type %q stored on the server: %w", flowType, err))
		}
		graphs[flowType] = graph
	}
	return graphs, nil
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

// A missing server baseline cannot distinguish a new Flow type from one never uploaded.
func compareFlowDefinitionGraphs(oldGraphs map[string]*flowviz.Graph, newGraphs map[string]*flowviz.Graph, isOldFromServer bool) flowDefinitionDiffReport {
	report := flowDefinitionDiffReport{ComparedFlowTypes: []string{}, Changes: []flowdefinitiondiff.Change{}}
	for _, flowType := range sortedKeys(newGraphs) {
		oldGraph, exists := oldGraphs[flowType]
		switch {
		case exists:
			report.ComparedFlowTypes = append(report.ComparedFlowTypes, flowType)
			report.Changes = append(report.Changes, flowdefinitiondiff.CompareGraphs(flowType, oldGraph, newGraphs[flowType])...)
		case isOldFromServer:
			report.Changes = append(report.Changes, flowdefinitiondiff.BaselineMissing(flowType))
		default:
			report.Changes = append(report.Changes, flowdefinitiondiff.FlowTypeAdded(flowType))
		}
	}
	if !isOldFromServer {
		for _, flowType := range sortedKeys(oldGraphs) {
			if _, exists := newGraphs[flowType]; !exists {
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
	return report
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
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
