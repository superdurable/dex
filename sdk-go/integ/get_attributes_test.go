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
)

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
	}}
}

func TestGetAttributes(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "get-attributes")
	secret := strings.Repeat("hard-delete-value", 64)
	largeValue := strings.Repeat("large-value", 64)
	count, err := dex.InitialAttribute(getAttributesCount, 0)
	require.NoError(t, err)
	mapValue, err := dex.InitialAttributeMapValue(getAttributesQuantities, "line-1", 0)
	require.NoError(t, err)
	secretValue, err := dex.InitialAttribute(getAttributesSecret, secret)
	require.NoError(t, err)
	largeInitialValue, err := dex.InitialAttribute(getAttributesLargeValue, largeValue)
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, getAttributesFlow{}, flowID, nil, dex.StartFlowOptions{
		Attributes: []dex.InitialAttributeDef{count, mapValue, secretValue, largeInitialValue},
	})
	require.NoError(t, err)

	var decodedCount *int
	var decodedMapValue *int
	var decodedSecret *string
	var decodedLargeValue *string
	var unsetCount *int
	err = integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(getAttributesCount, &decodedCount),
		dex.ReadAttributeMapInstance(getAttributesQuantities, "line-1", &decodedMapValue),
		dex.ReadAttribute(getAttributesSecret, &decodedSecret),
		dex.ReadAttribute(getAttributesLargeValue, &decodedLargeValue),
		dex.ReadAttribute(getAttributesUnset, &unsetCount),
	)
	require.NoError(t, err)
	require.Equal(t, 0, *decodedCount)
	require.Equal(t, 0, *decodedMapValue)
	require.Equal(t, secret, *decodedSecret)
	require.Equal(t, largeValue, *decodedLargeValue)
	require.Nil(t, unsetCount)

	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
	waitForFlow(t, flowID, false)
	decodedSecret = nil
	err = integClient.GetAttributes(ctx, flowID, dex.ReadAttribute(getAttributesSecret, &decodedSecret))
	require.NoError(t, err)
	require.Equal(t, secret, *decodedSecret)

	missingFlowID := newFlowID(t, "get-attributes-missing")
	err = integClient.GetAttributes(ctx, missingFlowID, dex.ReadAttribute(getAttributesCount, &decodedCount))
	var missingFlow *dex.FlowNotFoundError
	require.ErrorAs(t, err, &missingFlow)
	require.ErrorContains(t, err, missingFlowID)
	require.ErrorContains(t, err, getAttributesCount.AttributeName())

	unregisteredAttribute := dex.DefineAttribute[int]("get-attributes-unregistered")
	err = integClient.GetAttributes(ctx, flowID, dex.ReadAttribute(unregisteredAttribute, &decodedCount))
	require.ErrorContains(t, err, flowID)
	require.ErrorContains(t, err, unregisteredAttribute.AttributeName())
}
