// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	getAttributesCount      = dex.DefineAttribute[int]("get-attributes-count")
	getAttributesUnset      = dex.DefineAttribute[int]("get-attributes-unset")
	getAttributesSecret     = dex.DefineAttribute[string]("get-attributes-secret", dex.HardDeleteOnNull())
	getAttributesLargeValue = dex.DefineAttribute[string]("get-attributes-large-value")
	getAttributesQuantities = dex.DefineAttributeMap[int]("get-attributes-quantities")
	getAttributesNote       = dex.DefineAttribute[getAttributesNoteValue]("get-attributes-note")
)

type getAttributesNoteValue struct {
	Text     string `json:"text"`
	Priority int    `json:"priority"`
}

type getAttributesFlow struct {
	dex.FlowDefaults
}

func (getAttributesFlow) GetSteps() []dex.StepDef {
	return nil
}

func (getAttributesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		getAttributesCount,
		getAttributesUnset,
		getAttributesSecret,
		getAttributesLargeValue,
		getAttributesQuantities,
		getAttributesNote,
	}}
}

func TestGetAttributes(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "get-attributes")
	secret := strings.Repeat("hard-delete-value", 64)
	largeValue := strings.Repeat("large-value", 64)
	note := getAttributesNoteValue{Text: "ship today", Priority: 2}
	count, err := dex.InitialAttribute(getAttributesCount, 7)
	require.NoError(t, err)
	mapValue, err := dex.InitialAttributeMapValue(getAttributesQuantities, "line-1", 0)
	require.NoError(t, err)
	secretValue, err := dex.InitialAttribute(getAttributesSecret, secret)
	require.NoError(t, err)
	largeInitialValue, err := dex.InitialAttribute(getAttributesLargeValue, largeValue)
	require.NoError(t, err)
	noteValue, err := dex.InitialAttribute(getAttributesNote, note)
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, getAttributesFlow{}, flowID, nil, dex.StartFlowOptions{
		Attributes: []dex.InitialAttributeDef{count, mapValue, secretValue, largeInitialValue, noteValue},
	})
	require.NoError(t, err)

	require.False(t, dex.AttributeReadResult{}.IsSet(getAttributesCount))
	require.False(t, dex.AttributeReadResult{}.IsMapInstanceSet(getAttributesQuantities, "line-1"))

	decodedCount := -1
	decodedMapValue := -1
	var decodedSecret string
	var decodedLargeValue string
	var decodedNote getAttributesNoteValue
	unsetCount := -1
	unsetMapValue := -1
	result, err := integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(getAttributesCount, &decodedCount),
		dex.ReadAttributeMapInstance(getAttributesQuantities, "line-1", &decodedMapValue),
		dex.ReadAttribute(getAttributesSecret, &decodedSecret),
		dex.ReadAttribute(getAttributesLargeValue, &decodedLargeValue),
		dex.ReadAttribute(getAttributesNote, &decodedNote),
		dex.ReadAttribute(getAttributesUnset, &unsetCount),
		dex.ReadAttributeMapInstance(getAttributesQuantities, "line-2", &unsetMapValue),
	)
	require.NoError(t, err)
	require.Equal(t, 7, decodedCount)
	require.Equal(t, 0, decodedMapValue)
	require.Equal(t, secret, decodedSecret)
	require.Equal(t, largeValue, decodedLargeValue)
	require.Equal(t, note, decodedNote)
	require.Equal(t, -1, unsetCount)
	require.Equal(t, -1, unsetMapValue)
	require.True(t, result.IsSet(getAttributesCount))
	require.True(t, result.IsSet(getAttributesNote))
	require.True(t, result.IsMapInstanceSet(getAttributesQuantities, "line-1"))
	require.False(t, result.IsSet(getAttributesUnset))
	require.False(t, result.IsMapInstanceSet(getAttributesQuantities, "line-2"))
	require.False(t, result.IsSet(getAttributesQuantities))
	require.False(t, result.IsMapInstanceSet(getAttributesCount, "line-1"))

	duplicateCount := -1
	_, err = integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(getAttributesCount, &duplicateCount),
		dex.ReadAttribute(getAttributesCount, &duplicateCount),
	)
	require.ErrorContains(t, err, "duplicate Attribute read")
	require.Equal(t, -1, duplicateCount)

	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
	waitForFlow(t, flowID, false)
	decodedSecret = ""
	decodedLargeValue = ""
	result, err = integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(getAttributesSecret, &decodedSecret),
		dex.ReadAttribute(getAttributesLargeValue, &decodedLargeValue),
	)
	require.NoError(t, err)
	require.Equal(t, secret, decodedSecret)
	require.Equal(t, largeValue, decodedLargeValue)
	require.True(t, result.IsSet(getAttributesLargeValue))

	missingFlowID := newFlowID(t, "get-attributes-missing")
	result, err = integClient.GetAttributes(ctx, missingFlowID, dex.ReadAttribute(getAttributesCount, &decodedCount))
	var missingFlow *dex.FlowNotFoundError
	require.ErrorAs(t, err, &missingFlow)
	require.ErrorContains(t, err, missingFlowID)
	require.ErrorContains(t, err, getAttributesCount.AttributeName())
	require.False(t, result.IsSet(getAttributesCount))

	var definitionError *dex.FlowDefinitionError
	unregisteredAttribute := dex.DefineAttribute[int]("get-attributes-unregistered")
	_, err = integClient.GetAttributes(ctx, flowID, dex.ReadAttribute(unregisteredAttribute, &decodedCount))
	require.ErrorAs(t, err, &definitionError)
	require.ErrorContains(t, err, flowID)
	require.ErrorContains(t, err, unregisteredAttribute.AttributeName())

	mismatchedCount := dex.DefineAttribute[string](getAttributesCount.AttributeName())
	var mismatchedValue string
	_, err = integClient.GetAttributes(ctx, flowID, dex.ReadAttribute(mismatchedCount, &mismatchedValue))
	require.ErrorAs(t, err, &definitionError)
	require.ErrorContains(t, err, getAttributesCount.AttributeName())
}
