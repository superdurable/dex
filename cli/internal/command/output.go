// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package command

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

func writeOutput(output io.Writer, format string, value any) error {
	if format == "table" {
		return writeTable(output, value)
	}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return newOperationError("output", err)
	}
	return nil
}

func writeTable(output io.Writer, value any) error {
	rows, headers := tableRows(value)
	written := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if len(headers) == 0 {
		_, err := fmt.Fprintln(written, tableCell(value))
		return closeWithError(err, written.Flush)
	}
	if _, err := fmt.Fprintln(written, strings.Join(headers, "\t")); err != nil {
		return closeWithError(err, written.Flush)
	}
	for _, row := range rows {
		cells := make([]string, len(headers))
		for index, header := range headers {
			cells[index] = tableCell(row[header])
		}
		if _, err := fmt.Fprintln(written, strings.Join(cells, "\t")); err != nil {
			return closeWithError(err, written.Flush)
		}
	}
	if err := written.Flush(); err != nil {
		return newOperationError("output", err)
	}
	return nil
}

func tableRows(value any) ([]map[string]any, []string) {
	switch current := value.(type) {
	case []any:
		rows := make([]map[string]any, 0, len(current))
		for _, entry := range current {
			if row, ok := entry.(map[string]any); ok {
				rows = append(rows, row)
			} else {
				rows = append(rows, map[string]any{"value": entry})
			}
		}
		return rows, tableHeaders(rows)
	case map[string]any:
		for _, key := range []string{"flows", "events", "methods", "messages"} {
			if entries, ok := current[key].([]any); ok {
				return tableRows(entries)
			}
		}
		rows := make([]map[string]any, 0, len(current))
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, map[string]any{"field": key, "value": current[key]})
		}
		return rows, []string{"field", "value"}
	default:
		return []map[string]any{{"value": value}}, []string{"value"}
	}
}

func tableHeaders(rows []map[string]any) []string {
	set := make(map[string]struct{})
	for _, row := range rows {
		for key := range row {
			set[key] = struct{}{}
		}
	}
	headers := make([]string, 0, len(set))
	for key := range set {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	return headers
}

func tableCell(value any) string {
	switch current := value.(type) {
	case nil:
		return ""
	case string:
		return strings.ReplaceAll(current, "\t", " ")
	case float64, float32, int, int32, int64, uint, uint32, uint64, bool:
		return fmt.Sprint(current)
	default:
		data, err := json.Marshal(current)
		if err != nil {
			return fmt.Sprint(current)
		}
		return string(data)
	}
}
