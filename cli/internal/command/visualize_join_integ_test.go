// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/flowviz"
)

func TestVisualizeGoJoinKeepsForkPublishAndWaitEdges(t *testing.T) {
	repositoryRoot := visualizerRepositoryRoot(t)
	directory := t.TempDir()
	goModule := "module flowvizjoin\n\ngo 1.24.0\n\nrequire github.com/superdurable/dex/sdk-go v0.0.0\n\nreplace github.com/superdurable/dex/sdk-go => " + filepath.Join(repositoryRoot, "sdk-go") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go.mod"), []byte(goModule), 0o644))
	sourcePath := filepath.Join(directory, "join.go")
	require.NoError(t, os.WriteFile(sourcePath, []byte(goJoinFlow), 0o644))

	graph, err := flowviz.Analyze(context.Background(), sourcePath, flowviz.AnalyzeOptions{})
	require.NoError(t, err)
	require.True(t, graph.Valid, graph.Diagnostics)

	joinResourceID := ""
	for _, node := range graph.Nodes {
		if node.Kind == "channel" && node.Name == "quoteReceived" {
			joinResourceID = node.ID
			require.NotNil(t, node.Resource)
			require.True(t, node.Resource.Map)
			require.Equal(t, "string", node.Resource.ValueType)
		}
	}
	require.NotEmpty(t, joinResourceID)

	for _, branch := range []string{"QuoteFromSupplierA", "QuoteFromSupplierB", "ReviewQuote"} {
		require.True(t, hasEdge(graph.Edges, "resource_publish", "step:"+branch, joinResourceID), branch)
	}
	require.True(t, hasEdge(graph.Edges, "resource_publish", "rpc:ReportCanceledQuote", joinResourceID))
	require.True(t, hasEdge(graph.Edges, "resource_read", joinResourceID, "step:AcceptQuotes"))
	require.True(t, hasEdge(graph.Edges, "resource_read", joinResourceID, "step:RequestQuotes"))

	labels := strings.Join(waitConditionLabels(nodesOfKind(graph.Nodes, "wait")), "\n")
	for _, fragment := range []string{
		"quoteReceived[input.Round].for input.RequiredQuotes branches",
		"quoteReceived[*request.PreviousRound].for remaining branches after request.RequiredQuotes",
		"quoteReceived[input.Round].for all branches",
		"quoteReceived[input.Round].for first branch",
	} {
		require.Contains(t, labels, fragment)
	}
	require.NotEmpty(t, edgesOfKind(graph.Edges, "wait_condition"))
	for _, edge := range edgesOfKind(graph.Edges, "wait_condition") {
		require.Equal(t, joinResourceID, edge.From)
	}

	splitTransitions := transitionsFromOwner(graph, "step:RequestQuotes")
	require.Equal(t, map[string]string{
		"step:QuoteFromSupplierA": "",
		"step:QuoteFromSupplierB": "",
		"step:AcceptQuotes":       "",
	}, splitTransitions)
	require.Equal(t, map[string]string{
		"step:ReviewQuote":   "×N",
		"step:CollectReview": "",
	}, transitionsFromOwner(graph, "step:RequestReviews"))
	require.Equal(t, map[string]string{
		"step:QuoteFromSupplierA": "",
		"step:QuoteFromSupplierB": "",
		"step:PickFirstQuote":     "",
	}, transitionsFromOwner(graph, "rpc:RequestQuotesNow"))

	for _, branch := range []string{"step:QuoteFromSupplierA", "step:QuoteFromSupplierB", "step:ReviewQuote"} {
		decisions := make([]flowviz.Node, 0)
		for _, node := range nodesOfKind(graph.Nodes, "decision") {
			if node.ParentID == branch {
				decisions = append(decisions, node)
			}
		}
		require.True(t, hasDecisionType(decisions, "deadEnd"), branch)
	}
}

// transitionsFromOwner maps each transition target of an owner's decisions to its multiplicity.
func transitionsFromOwner(graph *flowviz.Graph, ownerID string) map[string]string {
	targets := make(map[string]string)
	for _, node := range nodesOfKind(graph.Nodes, "decision") {
		if node.ParentID != ownerID {
			continue
		}
		for _, edge := range edgesFrom(graph.Edges, node.ID, "transition") {
			targets[edge.To] = edge.Multiplicity
		}
	}
	return targets
}

const goJoinFlow = `package flow

import "github.com/superdurable/dex/sdk-go/dex"

var quoteReceived = dex.DefineJoin[string]("quoteReceived")

type QuoteRequest struct {
	RequiredQuotes int
	RoundNumber    int
	RoundCount     int
	PreviousRound  *dex.JoinRound
	Reviewers      []string
}

type QuoteJoinInput struct {
	RequiredQuotes int
	RoundNumber    int
	RoundCount     int
	Round          dex.JoinRound
}

type ReviewInput struct {
	Reviewer string
	Round    dex.JoinRound
}

type QuoteFlow struct{ dex.FlowDefaults }

func (*QuoteFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(RequestQuotes{}),
		dex.DefineStep(QuoteFromSupplierA{}),
		dex.DefineStep(QuoteFromSupplierB{}),
		dex.DefineStep(AcceptQuotes{}),
		dex.DefineStep(RequestReviews{}),
		dex.DefineStep(ReviewQuote{}),
		dex.DefineStep(CollectReview{}),
		dex.DefineStep(PickFirstQuote{}),
	}
}

func (*QuoteFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{quoteReceived}}
}

func (flow *QuoteFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.RequestQuotesNow, nil),
		dex.DefineRPC(flow.ReportCanceledQuote, nil),
	}
}

func (*QuoteFlow) RequestQuotesNow(_ dex.Context, roundKey string) (*dex.RPCResult[dex.None], error) {
	movements, err := quoteReceived.ForkWithRoundKey(roundKey,
		[]func(dex.JoinRound) dex.StepMovement{
			func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierA{}, round) },
			func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierB{}, round) },
		},
		func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(PickFirstQuote{}, QuoteJoinInput{Round: round})
		},
	)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{NextSteps: movements}, nil
}

func (*QuoteFlow) ReportCanceledQuote(ctx dex.Context, round dex.JoinRound) (*dex.RPCResult[dex.None], error) {
	if err := quoteReceived.ReportBranchResult(ctx, round, "canceled"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type RequestQuotes struct{ dex.StepDefaults }

func (RequestQuotes) WaitFor(_ dex.Context, request QuoteRequest) (*dex.Wait, error) {
	if request.PreviousRound == nil {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(quoteReceived.ForRemainingBranches(*request.PreviousRound, request.RequiredQuotes)), nil
}

func (RequestQuotes) Execute(ctx dex.Context, request QuoteRequest) (*dex.StepDecision, error) {
	if request.PreviousRound != nil {
		if _, err := quoteReceived.RemainingBranchResults(ctx, *request.PreviousRound); err != nil {
			return nil, err
		}
	}
	movements, err := quoteReceived.Fork(ctx,
		[]func(dex.JoinRound) dex.StepMovement{
			func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierA{}, round) },
			func(round dex.JoinRound) dex.StepMovement { return dex.MovementOf(QuoteFromSupplierB{}, round) },
		},
		func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(AcceptQuotes{}, QuoteJoinInput{
				RequiredQuotes: request.RequiredQuotes,
				RoundNumber:    request.RoundNumber,
				RoundCount:     request.RoundCount,
				Round:          round,
			})
		},
	)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type QuoteFromSupplierA struct{ dex.StepDefaultsNoWaitFor[dex.JoinRound] }

func (QuoteFromSupplierA) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return quoteReceived.EndBranch(ctx, round, "A")
}

type QuoteFromSupplierB struct{ dex.StepDefaultsNoWaitFor[dex.JoinRound] }

func (QuoteFromSupplierB) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	return quoteReceived.EndBranch(ctx, round, "B")
}

type AcceptQuotes struct{ dex.StepDefaults }

func (AcceptQuotes) WaitFor(_ dex.Context, input QuoteJoinInput) (*dex.Wait, error) {
	return dex.Until(quoteReceived.ForBranches(input.Round, input.RequiredQuotes)), nil
}

func (AcceptQuotes) Execute(ctx dex.Context, input QuoteJoinInput) (*dex.StepDecision, error) {
	quotes, err := quoteReceived.BranchResults(ctx, input.Round)
	if err != nil {
		return nil, err
	}
	if input.RoundNumber < input.RoundCount {
		return dex.GoTo(RequestQuotes{}, QuoteRequest{
			RequiredQuotes: input.RequiredQuotes,
			RoundNumber:    input.RoundNumber + 1,
			RoundCount:     input.RoundCount,
			PreviousRound:  &input.Round,
		}), nil
	}
	return dex.GoTo(RequestReviews{}, QuoteRequest{Reviewers: quotes}), nil
}

type RequestReviews struct{ dex.StepDefaultsNoWaitFor[QuoteRequest] }

func (RequestReviews) Execute(ctx dex.Context, request QuoteRequest) (*dex.StepDecision, error) {
	builders := make([]func(dex.JoinRound) dex.StepMovement, 0, len(request.Reviewers))
	for _, reviewer := range request.Reviewers {
		builders = append(builders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(ReviewQuote{}, ReviewInput{Reviewer: reviewer, Round: round})
		})
	}
	joinBuilder := func(round dex.JoinRound) dex.StepMovement {
		return dex.MovementOf(CollectReview{}, QuoteJoinInput{Round: round})
	}
	movements, err := quoteReceived.Fork(ctx, builders, joinBuilder)
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

type ReviewQuote struct{ dex.StepDefaultsNoWaitFor[ReviewInput] }

func (ReviewQuote) Execute(ctx dex.Context, input ReviewInput) (*dex.StepDecision, error) {
	return quoteReceived.EndBranch(ctx, input.Round, input.Reviewer)
}

type CollectReview struct{ dex.StepDefaults }

func (CollectReview) WaitFor(_ dex.Context, input QuoteJoinInput) (*dex.Wait, error) {
	return dex.Until(quoteReceived.ForAllBranches(input.Round)), nil
}

func (CollectReview) Execute(ctx dex.Context, input QuoteJoinInput) (*dex.StepDecision, error) {
	reviews, err := quoteReceived.BranchResults(ctx, input.Round)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(reviews), nil
}

type PickFirstQuote struct{ dex.StepDefaults }

func (PickFirstQuote) WaitFor(_ dex.Context, input QuoteJoinInput) (*dex.Wait, error) {
	return dex.Until(quoteReceived.ForFirstBranch(input.Round)), nil
}

func (PickFirstQuote) Execute(ctx dex.Context, input QuoteJoinInput) (*dex.StepDecision, error) {
	quotes, err := quoteReceived.BranchResults(ctx, input.Round)
	if err != nil {
		return nil, err
	}
	return dex.ForceComplete(quotes).CancelSiblingSteps(QuoteFromSupplierA{}, QuoteFromSupplierB{}), nil
}
`
