// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	searchKeywordKey  = "CustomKeywordField"
	searchTextKey     = "CustomStringField"
	searchBoolKey     = "CustomBoolField"
	searchDatetimeKey = "CustomDatetimeField"
	searchIntKey      = "CustomIntField"
	searchDoubleKey   = "CustomDoubleField"
)

var (
	persistenceData    = dex.DefineAttribute[persistenceModel]("data")
	persistenceText    = dex.DefineAttribute[string]("text")
	persistenceMap     = dex.DefineAttributeMap[int]("items")
	persistenceKeyword = dex.DefineAttribute[string](
		"keyword",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: searchKeywordKey}),
	)
	persistenceSearchText = dex.DefineAttribute[string](
		"search-text",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexText, IndexKey: searchTextKey}),
	)
	persistenceBool = dex.DefineAttribute[bool](
		"bool",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexBool, IndexKey: searchBoolKey}),
	)
	persistenceDatetime = dex.DefineAttribute[time.Time](
		"datetime",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexDatetime, IndexKey: searchDatetimeKey}),
	)
	persistenceInt = dex.DefineAttribute[int64](
		"int",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexInt, IndexKey: searchIntKey}),
	)
	persistenceDouble = dex.DefineAttribute[float64](
		"double",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexDouble, IndexKey: searchDoubleKey}),
	)
)

type persistenceModel struct {
	Number   int64
	Text     string
	Datetime time.Time
}

type persistenceFlow struct{}

func (persistenceFlow) GetFlowType() string {
	return "go-sdk-persistence"
}

func (persistenceFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(persistenceFirstStep{}),
		dex.DefineStep(persistenceSecondStep{}),
	}
}

func (persistenceFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		persistenceData,
		persistenceText,
		persistenceMap,
		persistenceKeyword,
		persistenceSearchText,
		persistenceBool,
		persistenceDatetime,
		persistenceInt,
		persistenceDouble,
	}}
}

type persistenceFirstStep struct {
	dex.DefaultStepOptions
}

func (persistenceFirstStep) GetStepType() string {
	return "first"
}

func (persistenceFirstStep) WaitFor(
	ctx dex.Context,
	input persistenceModel,
) (dex.Wait, error) {
	keyword, found, err := persistenceKeyword.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if !found || keyword != "init-keyword" {
		return dex.Wait{}, fmt.Errorf("unexpected initial keyword %q", keyword)
	}
	searchText, found, err := persistenceSearchText.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if !found || searchText != "init-text" {
		return dex.Wait{}, fmt.Errorf("unexpected initial search text %q", searchText)
	}
	_, found, err = persistenceData.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if found {
		return dex.Wait{}, fmt.Errorf("data must not exist before its first write")
	}
	item, found, err := persistenceMap.Get(ctx, "one")
	if err != nil {
		return dex.Wait{}, err
	}
	if !found || item != 10 {
		return dex.Wait{}, fmt.Errorf("unexpected initial map value %d", item)
	}
	if err := persistenceData.Set(ctx, input); err != nil {
		return dex.Wait{}, err
	}
	if err := persistenceText.Set(ctx, "a string"); err != nil {
		return dex.Wait{}, err
	}
	if err := persistenceMap.Set(ctx, "one", 11); err != nil {
		return dex.Wait{}, err
	}
	if err := persistenceInt.Set(ctx, 1); err != nil {
		return dex.Wait{}, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (persistenceFirstStep) Execute(
	ctx dex.Context,
	input persistenceModel,
) (dex.StepDecision, error) {
	integer, found, err := persistenceInt.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found || integer != 1 {
		return dex.StepDecision{}, fmt.Errorf("WaitFor integer write was not visible")
	}
	data, found, err := persistenceData.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found || data.Text != input.Text || data.Number != input.Number {
		return dex.StepDecision{}, fmt.Errorf("WaitFor data write was not visible")
	}
	if err := persistenceDatetime.Set(ctx, data.Datetime); err != nil {
		return dex.StepDecision{}, err
	}
	if err := persistenceBool.Set(ctx, true); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GoTo(persistenceSecondStep{}, struct{}{}), nil
}

type persistenceSecondStep struct {
	dex.DefaultStepOptions
}

func (persistenceSecondStep) GetStepType() string {
	return "second"
}

func (persistenceSecondStep) WaitFor(
	ctx dex.Context,
	_ struct{},
) (dex.Wait, error) {
	data, found, err := persistenceData.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if !found {
		return dex.Wait{}, fmt.Errorf("persisted data is missing")
	}
	dateTime, found, err := persistenceDatetime.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if !found || !dateTime.Equal(data.Datetime) {
		return dex.Wait{}, fmt.Errorf("persisted datetime does not round-trip")
	}
	boolean, found, err := persistenceBool.Get(ctx)
	if err != nil {
		return dex.Wait{}, err
	}
	if !found || !boolean {
		return dex.Wait{}, fmt.Errorf("persisted bool is false")
	}
	if err := persistenceDouble.Set(ctx, 1); err != nil {
		return dex.Wait{}, err
	}
	if err := persistenceSearchText.Set(ctx, "Hail Dex!"); err != nil {
		return dex.Wait{}, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (persistenceSecondStep) Execute(
	ctx dex.Context,
	_ struct{},
) (dex.StepDecision, error) {
	text, found, err := persistenceSearchText.Get(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found || text != "Hail Dex!" {
		return dex.StepDecision{}, fmt.Errorf("unexpected persisted text %q", text)
	}
	if err := persistenceKeyword.Set(ctx, "Dex"); err != nil {
		return dex.StepDecision{}, err
	}
	return dex.GracefulComplete("done"), nil
}

func TestPersistenceFlow(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "persistence")
	now := time.Now().UTC().Round(0)
	input := persistenceModel{Number: now.UnixNano(), Text: flowID, Datetime: now}
	initialKeyword, err := dex.InitialAttribute(persistenceKeyword, "init-keyword")
	require.NoError(t, err)
	initialText, err := dex.InitialAttribute(persistenceSearchText, "init-text")
	require.NoError(t, err)
	initialBool, err := dex.InitialAttribute(persistenceBool, false)
	require.NoError(t, err)
	initialDatetime, err := dex.InitialAttribute(persistenceDatetime, now)
	require.NoError(t, err)
	initialInt, err := dex.InitialAttribute(persistenceInt, int64(0))
	require.NoError(t, err)
	initialDouble, err := dex.InitialAttribute(persistenceDouble, float64(2.1))
	require.NoError(t, err)
	initialMap, err := dex.InitialAttributeMapValue(persistenceMap, "one", 10)
	require.NoError(t, err)
	_, err = integClient.StartFlow(
		ctx,
		persistenceFlow{},
		flowID,
		input,
		dex.StartFlowOptions{Attributes: []dex.InitialAttributeDef{
			initialKeyword,
			initialText,
			initialBool,
			initialDatetime,
			initialInt,
			initialDouble,
			initialMap,
		}},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)

	var data persistenceModel
	found, err := integClient.GetAttribute(ctx, flowID, persistenceData, &data)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, input, data)
	var item int
	found, err = integClient.GetAttributeMap(ctx, flowID, persistenceMap, "one", &item)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 11, item)
	values, err := integClient.GetAttributes(
		ctx,
		flowID,
		persistenceText,
		persistenceKeyword,
		persistenceSearchText,
		persistenceBool,
		persistenceInt,
		persistenceDouble,
	)
	require.NoError(t, err)
	require.Len(t, values, 6)
	var text string
	require.NoError(t, values[persistenceText.AttributeName()].Decode(&text))
	require.Equal(t, "a string", text)

	var searchPage dex.SearchFlowsPage
	require.Eventually(t, func() bool {
		searchPage, err = integClient.SearchFlows(
			ctx,
			searchKeywordKey+" = 'Dex'",
			100,
			"",
		)
		if err != nil {
			return false
		}
		for _, entry := range searchPage.Flows {
			if entry.FlowID == flowID && entry.FlowType == (persistenceFlow{}).GetFlowType() {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond, "SearchFlows failed: %v", err)
}
