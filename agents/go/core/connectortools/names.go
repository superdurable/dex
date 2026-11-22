// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectortools

import (
	"strings"

	"github.com/superdurable/dex-connectors-library/sdkgo"
)

// DefaultConnectorToolName returns the default tool name of an operation:
// "<connectorId>_<operation in snake case>", such as stripe_create_refund.
//
// The snake case is the one the Connector library uses for Python Step
// factory names: an acronym run stays one word, so listStoryIDs becomes
// list_story_ids and getHTTPResponse becomes get_http_response. It equals the
// ToolName that sdkgo validates in AgentToolMetadata, and the default name in
// the Python and TypeScript cores. The name carries no connection; give a
// tool of a second connection of the same operation its own name with
// WithToolName.
//
// Since 0.6.16.
func DefaultConnectorToolName(operation sdkgo.OperationRef) string {
	return operation.ConnectorID + "_" + lowerCamelToSnake(operation.OperationID)
}

// lowerCamelToSnake follows connectorctl's lowerCamelToSnake, which names the Python Step factories.
func lowerCamelToSnake(operationID string) string {
	characters := []rune(operationID)
	characterAt := func(index int) rune {
		if index < 0 || index >= len(characters) {
			return 0
		}
		return characters[index]
	}
	var converted strings.Builder
	for index, character := range characters {
		if !isASCIIUpper(character) {
			converted.WriteRune(character)
			continue
		}
		previous, following, afterFollowing := characterAt(index-1), characterAt(index+1), characterAt(index+2)
		isPreviousLowerOrDigit := isASCIILower(previous) || isASCIIDigit(previous)
		// A trailing lowercase s pluralizes the acronym, as in listStoryIDs.
		isAcronymPlural := following == 's' && !isASCIILower(afterFollowing)
		isEndOfAcronym := isASCIIUpper(previous) && isASCIILower(following) && !isAcronymPlural
		if isPreviousLowerOrDigit || isEndOfAcronym {
			converted.WriteByte('_')
		}
		converted.WriteRune(character + ('a' - 'A'))
	}
	return converted.String()
}

func isASCIIUpper(character rune) bool { return character >= 'A' && character <= 'Z' }

func isASCIILower(character rune) bool { return character >= 'a' && character <= 'z' }

func isASCIIDigit(character rune) bool { return character >= '0' && character <= '9' }
