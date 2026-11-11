// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"fmt"
	"strings"
)

// Join synchronizes parallel branches of one Flow by counting the results that the branches report.
//
// Use a Join when one Step starts several parallel branches and a later Step must wait for all of
// them, the first of them, or n of them. Fork starts the branches and the join Step in one decision.
// It gives every branch and the join Step the same JoinRound. Each branch reports exactly one
// result with EndBranch or ReportBranchResult. The join Step waits with ForAllBranches,
// ForFirstBranch, or ForBranches, and reads the results with BranchResults. The application keeps
// no branch counter and no round number: Fork counts the branches and creates the round key.
//
// A Join is a ChannelMap of branch results with the same name. Each round uses its own instance,
// keyed by the round key, so results of a later round never satisfy an earlier round's join.
// Register the Join in PersistenceSchema.Channels like a ChannelMap. Its name must differ from every
// other Channel and ChannelMap in the Flow. Each stored result is the application's T, so Dex Web,
// dexcli, and ChannelMap reads show the results as ordinary ChannelMap messages.
//
// When the join waits for fewer results than branches, the remaining results of that round stay
// pending. A Flow that forks the same Join in a loop resets each round before it starts the next:
// the next split Step waits with ForRemainingBranches on the previous round, and its Execute reads
// those late results with RemainingBranchResults. The next round therefore cannot start before
// the previous round has received every result. A canceling join's round is never reset; see the
// rules below.
//
// Rules and traps:
//   - Each branch reports exactly one result per round. A branch that reports twice is counted
//     twice. A branch that never reports, for example because an Execute failure policy moved it to
//     a recovery Step that does not report, makes ForAllBranches wait forever. Report a failure
//     result from the recovery Step, or combine the join Condition with a Timer in AnyOf.
//   - A branch passes its JoinRound only to EndBranch or ReportBranchResult. A branch never waits on
//     a Condition of its own round. Only the join movement built by Fork waits on the round; two Steps
//     that wait on the same round split its results between them.
//   - A multi-Step branch passes the JoinRound to its next Step and reports from its last Step.
//   - One Step invocation forks one Join at most once. Use a second Join definition, or
//     ForkWithRoundKey with a distinct key, for a second independent join in the same invocation.
//   - RPCs have no Step execution ID, so an RPC uses ForkWithRoundKey instead of Fork.
//   - The receivedCount of ForRemainingBranches must equal the number of results the join consumed:
//     n for ForBranches(round, n), 1 for ForFirstBranch, and the branch count for ForAllBranches.
//   - A canceling join adds CancelSiblingSteps for the branch Step types to its decision. Fork puts
//     the branches and the join Step in one decision, so they are siblings, and only this round's
//     branches are canceled. Later Steps of a multi-Step branch are not siblings; use CancelSteps,
//     which is correct only while one round is active. Steps started by an RPC are siblings of
//     every Step that any invocation of the same RPC started, so a round started by an RPC also
//     uses CancelSteps.
//   - A canceled branch never reports, and no other code can safely report for it. Dex decides at
//     commit time whether a cancellation takes effect, so the code that cancels a branch cannot
//     know whether the branch already reported. If it did, the cancellation does nothing, and an
//     extra report counts that branch twice, so ForAllBranches can be satisfied while another
//     branch has not reported. To keep ForAllBranches from waiting forever for a canceled branch,
//     cancel the join Step together with its branches, or wait on AnyOf with the join Condition and
//     a Timer and handle the timeout in Execute.
//   - Never reset a canceling join's round with ForRemainingBranches: it waits for results that
//     canceled branches never report. The next round forks a new round without the reset wait, as
//     the first round does. The canceled round's late results stay pending.
//   - When a join Step is canceled after its wait consumed results but before its Execute commits,
//     what happens to those results depends on the server version. A server that returns consumed
//     messages on cancellation puts them back at the front of the round's instance, so a new join
//     Step for the same round can consume them. An older server drops them, and that round can no
//     longer be joined. See https://docs.superdurable.io/reference/server/changelog.
//   - Results reported after a canceling join, and results of the last round when nothing resets
//     it, stay pending in the round's instance until the Flow closes. They do not block
//     GracefulComplete.
//
// Example (two rounds that each accept two of three quotes, as a complete package):
//
//	package quotes
//
//	import "github.com/superdurable/dex/sdk-go/dex"
//
//	// QuoteReceived collects one quote from each supplier branch.
//	var QuoteReceived = dex.DefineJoin[string]("quoteReceived")
//
//	// SupplierReleased tells a supplier branch that its quote is ready.
//	var SupplierReleased = dex.DefineChannelMap[dex.None]("supplierReleased")
//
//	type QuoteRequest struct {
//		RequiredQuotes int            `json:"requiredQuotes"`
//		RoundNumber    int            `json:"roundNumber"`
//		RoundCount     int            `json:"roundCount"`
//		PreviousRound  *dex.JoinRound `json:"previousRound,omitempty"`
//	}
//
//	type QuoteJoinInput struct {
//		Request QuoteRequest  `json:"request"`
//		Round   dex.JoinRound `json:"round"`
//	}
//
//	type SupplierQuoteInput struct {
//		Supplier string        `json:"supplier"`
//		Round    dex.JoinRound `json:"round"`
//	}
//
//	type QuoteFlow struct{ dex.FlowDefaults }
//
//	func (QuoteFlow) GetSteps() []dex.StepDef {
//		return []dex.StepDef{
//			dex.DefineStartStep(RequestQuotes{}),
//			dex.DefineStep(QuoteFromSupplier{}),
//			dex.DefineStep(AcceptQuotes{}),
//		}
//	}
//
//	func (QuoteFlow) GetPersistenceSchema() dex.PersistenceSchema {
//		return dex.PersistenceSchema{Channels: []dex.ChannelDef{QuoteReceived, SupplierReleased}}
//	}
//
//	// RequestQuotes is the split Step. Before round two it resets round one.
//	type RequestQuotes struct{ dex.StepDefaults }
//
//	func (RequestQuotes) WaitFor(_ dex.Context, request QuoteRequest) (*dex.Wait, error) {
//		if request.PreviousRound == nil {
//			return dex.SkipWaitImmediately(), nil
//		}
//		return dex.Until(QuoteReceived.ForRemainingBranches(*request.PreviousRound, request.RequiredQuotes)), nil
//	}
//
//	func (RequestQuotes) Execute(ctx dex.Context, request QuoteRequest) (*dex.StepDecision, error) {
//		if request.PreviousRound != nil {
//			ignoredQuotes, err := QuoteReceived.RemainingBranchResults(ctx, *request.PreviousRound)
//			if err != nil {
//				return nil, err
//			}
//			if err := ctx.RecordEvent("ignoredQuotes", ignoredQuotes); err != nil {
//				return nil, err
//			}
//		}
//		branchBuilders := make([]func(dex.JoinRound) dex.StepMovement, 0, 3)
//		for _, supplier := range []string{"A", "B", "C"} {
//			branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
//				return dex.MovementOf(QuoteFromSupplier{}, SupplierQuoteInput{Supplier: supplier, Round: round})
//			})
//		}
//		movements, err := QuoteReceived.Fork(ctx, branchBuilders, func(round dex.JoinRound) dex.StepMovement {
//			return dex.MovementOf(AcceptQuotes{}, QuoteJoinInput{Request: request, Round: round})
//		})
//		if err != nil {
//			return nil, err
//		}
//		return dex.GoToMany(movements...), nil
//	}
//
//	// QuoteFromSupplier is one branch. It reports its result and ends.
//	type QuoteFromSupplier struct{ dex.StepDefaults }
//
//	func (QuoteFromSupplier) WaitFor(_ dex.Context, input SupplierQuoteInput) (*dex.Wait, error) {
//		return dex.Until(SupplierReleased.ForOne(input.Supplier)), nil
//	}
//
//	func (QuoteFromSupplier) Execute(ctx dex.Context, input SupplierQuoteInput) (*dex.StepDecision, error) {
//		return QuoteReceived.EndBranch(ctx, input.Round, input.Supplier)
//	}
//
//	// AcceptQuotes is the join Step. It starts the next round with the same JoinRound.
//	type AcceptQuotes struct{ dex.StepDefaults }
//
//	func (AcceptQuotes) WaitFor(_ dex.Context, input QuoteJoinInput) (*dex.Wait, error) {
//		return dex.Until(QuoteReceived.ForBranches(input.Round, input.Request.RequiredQuotes)), nil
//	}
//
//	func (AcceptQuotes) Execute(ctx dex.Context, input QuoteJoinInput) (*dex.StepDecision, error) {
//		acceptedQuotes, err := QuoteReceived.BranchResults(ctx, input.Round)
//		if err != nil {
//			return nil, err
//		}
//		if err := ctx.RecordEvent("acceptedQuotes", acceptedQuotes); err != nil {
//			return nil, err
//		}
//		if input.Request.RoundNumber < input.Request.RoundCount {
//			nextRequest := input.Request
//			nextRequest.RoundNumber++
//			nextRequest.PreviousRound = &input.Round
//			return dex.GoTo(RequestQuotes{}, nextRequest), nil
//		}
//		// ForceComplete abandons the last round's branch whose quote is not needed.
//		return dex.ForceComplete(acceptedQuotes), nil
//	}
//
// Since 1.44.0.
type Join[T any] struct {
	channelMap ChannelMap[T]
}

// JoinRound identifies one round of a Join and the number of branches in that round.
//
// Fork and ForkWithRoundKey create a JoinRound. Pass it unchanged in the input of every branch
// Step, the join Step, and the next round's split Step. Treat it as an opaque token: do not build
// or change one by hand. Its JSON form is {"roundKey": string, "branchCount": integer}. Dex stores
// it in Step inputs, so these field names never change.
//
// See the Join example. Since 1.44.0.
type JoinRound struct {
	// RoundKey is the ChannelMap instance key of this round. Fork uses the split Step's execution
	// ID, such as "RequestQuotes-2". It is never empty and never contains "/".
	RoundKey string `json:"roundKey"`
	// BranchCount is the number of branches that Fork started in this round. It may be zero.
	BranchCount int `json:"branchCount"`
}

// DefineJoin creates a Join whose branch results use the ChannelMap named name.
//
// It performs no I/O. Register the returned Join in PersistenceSchema.Channels. The name follows the
// ChannelMap rules: it is not empty, it does not contain "/", and it differs from every other
// Channel and ChannelMap of the Flow. Registration fails with a Flow definition error otherwise.
//
// To migrate a hand-written join over a ChannelMap[T] without breaking running Flows, keep the name
// and T, and fork with ForkWithRoundKey using the old instance keys.
//
// See the Join example. Since 1.44.0.
func DefineJoin[T any](name string) Join[T] {
	return Join[T]{channelMap: ChannelMap[T]{name: name}}
}

// Fork starts one round of the Join from a split Step's Execute.
//
// Fork uses the current Step execution ID as the round key and the number of branchBuilders as the
// branch count. It creates the JoinRound first, then calls each branch builder in order and finally
// joinBuilder, each with that JoinRound. Pass the returned movements to GoToMany, optionally with
// more movements. It performs no I/O.
//
// branchBuilders may be empty. The round then has zero branches, and ForAllBranches is satisfied
// immediately, so a Multi-Choice that selects no branch needs no special case.
//
// Fork returns an error when ctx is not a Dex Step invocation, when it runs in an RPC (which has no
// Step execution ID; use ForkWithRoundKey), when a builder is nil, and when the same Join was already
// forked in this invocation. The duplicate check is deterministic, so a retry fails the same way.
//
// See the Join example. Since 1.44.0.
//
// The returned slice holds the branch movements in builder order, then the join movement.
func (join Join[T]) Fork(
	ctx Context,
	branchBuilders []func(round JoinRound) StepMovement,
	joinBuilder func(round JoinRound) StepMovement,
) ([]StepMovement, error) {
	invocation, ok := ctx.(joinForkInvocation)
	if !ok {
		return nil, errInvalidInvocationContext
	}
	roundKey := ctx.StepExecutionID()
	if roundKey == "" {
		return nil, fmt.Errorf(
			"dex: Join %q Fork requires a Step execution ID; an RPC uses ForkWithRoundKey",
			join.channelMap.name,
		)
	}
	if err := validateJoinBuilders(join.channelMap.name, branchBuilders, joinBuilder); err != nil {
		return nil, err
	}
	if err := invocation.recordJoinFork(join.channelMap.name); err != nil {
		return nil, err
	}
	return buildJoinMovements(roundKey, branchBuilders, joinBuilder), nil
}

// ForkWithRoundKey starts one round of the Join with a round key chosen by the caller.
//
// Use it in an RPC, which has no Step execution ID, or when a Step needs its own key, for example to
// keep the instance keys of a hand-written join during a migration. It behaves like Fork otherwise.
// Put the returned movements in RPCResult.NextSteps or in GoToMany. Each round needs a key that no
// other round of this Join in the same Flow run uses; a reused key mixes the results of both rounds.
// ForkWithRoundKey does not record the fork, so it may run several times in one invocation with
// distinct keys.
//
// It returns an error when roundKey is empty or contains "/", or when a builder is nil.
//
// See the Join example. Since 1.44.0.
//
// The returned slice holds the branch movements in builder order, then the join movement.
func (join Join[T]) ForkWithRoundKey(
	roundKey string,
	branchBuilders []func(round JoinRound) StepMovement,
	joinBuilder func(round JoinRound) StepMovement,
) ([]StepMovement, error) {
	if err := validateJoinRoundKey(join.channelMap.name, roundKey); err != nil {
		return nil, err
	}
	if err := validateJoinBuilders(join.channelMap.name, branchBuilders, joinBuilder); err != nil {
		return nil, err
	}
	return buildJoinMovements(roundKey, branchBuilders, joinBuilder), nil
}

// EndBranch reports this branch's result and ends the branch.
//
// Call it from the Execute of a branch's last Step and return its decision. It stages the result
// like ReportBranchResult, and Dex commits the result only when Execute succeeds, so retries never
// report twice. Call it once per branch and round.
//
// It returns an error when ctx is not a Dex invocation, when round is invalid, when the Join is not
// registered in the Flow, or when result cannot be encoded.
//
// See the Join example. Since 1.44.0.
//
// On success it returns a DeadEnd decision.
func (join Join[T]) EndBranch(ctx Context, round JoinRound, result T) (*StepDecision, error) {
	if err := join.ReportBranchResult(ctx, round, result); err != nil {
		return nil, err
	}
	return DeadEnd(), nil
}

// ReportBranchResult reports one branch result without ending the branch.
//
// Use it in a branch's WaitFor or Execute when the branch continues after it reports, for example
// when a multi-Step branch reports from a Step other than its last. An RPC can report a branch's
// result when that result arrives through the RPC and no Step of that branch reports it, for
// example for a handoff branch in a round the RPC forked with ForkWithRoundKey; the RPC input then
// carries the JoinRound. Dex commits the result only when the handler succeeds. Each branch
// reports exactly one result per round. Never report for a canceled branch; see the Join rules.
//
// It returns an error when ctx is not a Dex invocation, when round is invalid, when the Join is not
// registered in the Flow, or when result cannot be encoded.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) ReportBranchResult(ctx Context, round JoinRound, result T) error {
	if err := validateJoinRound(join.channelMap.name, round); err != nil {
		return err
	}
	return join.channelMap.Publish(ctx, round.RoundKey, result)
}

// ForAllBranches returns a Condition satisfied when every branch of round has reported a result.
//
// Use it in the join Step's WaitFor. It consumes BranchCount results. A round with zero branches is
// satisfied immediately. Combine it with a Timer in AnyOf to bound the wait. An invalid round fails
// when Dex serializes the returned Wait.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) ForAllBranches(round JoinRound, options ...ConditionOption) Condition {
	return join.newRoundCondition(round, round.BranchCount, nil, options)
}

// ForFirstBranch returns a Condition satisfied when the first branch of round has reported a result.
//
// Use it in the join Step's WaitFor for a discriminator. It consumes exactly the earliest committed
// result, even when several results are already pending; the others stay for ForRemainingBranches.
// Put the winner's identity in T when Execute needs it. An invalid round, or a round with zero
// branches, fails when Dex serializes the returned Wait.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) ForFirstBranch(round JoinRound, options ...ConditionOption) Condition {
	return join.newRoundCondition(round, 1, validateJoinBranchCount(join.channelMap.name, round, 1), options)
}

// ForBranches returns a Condition satisfied when count branches of round have reported a result.
//
// Use it in the join Step's WaitFor for a partial join. It consumes exactly count results, in commit
// order. count must be between 1 and round.BranchCount. An invalid round or count fails when Dex
// serializes the returned Wait.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) ForBranches(round JoinRound, count int, options ...ConditionOption) Condition {
	return join.newRoundCondition(round, count, validateJoinBranchCount(join.channelMap.name, round, count), options)
}

// BranchResults returns the results that the join Step's satisfied Condition consumed for round.
//
// Call it in the join Step's Execute. The results are in commit order. A Condition that did not
// complete, such as the losing side of AnyOf, contributes no results.
//
// It returns an error when ctx is not an Execute invocation, when round is invalid, or when a
// result cannot be decoded.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) BranchResults(ctx Context, round JoinRound) ([]T, error) {
	if err := validateJoinRound(join.channelMap.name, round); err != nil {
		return nil, err
	}
	return join.channelMap.GetConditionResults(ctx, round.RoundKey)
}

// ForRemainingBranches returns a Condition that resets round by consuming its remaining results.
//
// Use it in the next round's split Step WaitFor. It waits for the round.BranchCount - receivedCount
// results that the join did not consume and consumes them. The next round therefore starts only
// after the previous round is complete. receivedCount is the number of results the join consumed:
// n for ForBranches(round, n), 1 for ForFirstBranch. It must be between 0 and round.BranchCount.
// When it equals round.BranchCount, the Condition is satisfied immediately. An invalid round or
// receivedCount fails when Dex serializes the returned Wait.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) ForRemainingBranches(
	round JoinRound,
	receivedCount int,
	options ...ConditionOption,
) Condition {
	var rangeErr error
	if receivedCount < 0 || receivedCount > round.BranchCount {
		rangeErr = fmt.Errorf(
			"dex: Join %q received count %d must be between 0 and the branch count %d",
			join.channelMap.name,
			receivedCount,
			round.BranchCount,
		)
	}
	return join.newRoundCondition(round, round.BranchCount-receivedCount, rangeErr, options)
}

// RemainingBranchResults returns the late results that ForRemainingBranches consumed for round.
//
// Call it in the Execute of the Step that reset round, usually the next round's split Step. The
// results are in commit order.
//
// It returns an error when ctx is not an Execute invocation, when round is invalid, or when a
// result cannot be decoded.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) RemainingBranchResults(ctx Context, round JoinRound) ([]T, error) {
	return join.BranchResults(ctx, round)
}

// JoinName returns the stable name of the Join, which is also the name of its ChannelMap.
//
// See the Join example. Since 1.44.0.
func (join Join[T]) JoinName() string {
	return join.channelMap.name
}

// ChannelMap returns the ChannelMap that stores this Join's branch results.
//
// Use it to inspect a round's pending results with the ChannelMap API. The instance key of a round
// is its JoinRound.RoundKey. For example, an RPC can read how many late results a round left with
// Size, or load the round's instance with LoadMessages in its options and read them with
// PendingMessages. The returned ChannelMap has the Join's name, so every API that accepts the
// registered ChannelMap accepts it, including load options.
//
// Never publish to the returned ChannelMap or delete its messages. Report results only with
// EndBranch or ReportBranchResult. A direct publish or delete changes the count that the join
// Conditions rely on, so a join can trigger early or wait forever.
//
//	func (QuoteFlow) CountLateQuotes(ctx dex.Context, round dex.JoinRound) (*dex.RPCResult[int], error) {
//		return &dex.RPCResult[int]{Output: QuoteReceived.ChannelMap().Size(ctx, round.RoundKey)}, nil
//	}
//
// See the Join example. Since 1.44.0.
func (join Join[T]) ChannelMap() ChannelMap[T] {
	return join.channelMap
}

func (join Join[T]) channelName() string {
	return join.channelMap.name
}

func (Join[T]) channelIsMap() bool {
	return true
}

func (join Join[T]) newRoundCondition(
	round JoinRound,
	count int,
	rangeErr error,
	options []ConditionOption,
) Condition {
	condition := newChannelCondition(join.channelMap.name, round.RoundKey, true, &count, &count, options)
	validationErr := validateJoinRound(join.channelMap.name, round)
	if validationErr == nil {
		validationErr = rangeErr
	}
	if validationErr != nil {
		condition.(*conditionImpl).err = validationErr
	}
	return condition
}

// joinForkInvocation records which Joins one Step invocation already forked.
type joinForkInvocation interface {
	recordJoinFork(joinName string) error
}

func buildJoinMovements(
	roundKey string,
	branchBuilders []func(round JoinRound) StepMovement,
	joinBuilder func(round JoinRound) StepMovement,
) []StepMovement {
	round := JoinRound{RoundKey: roundKey, BranchCount: len(branchBuilders)}
	movements := make([]StepMovement, 0, len(branchBuilders)+1)
	for _, branchBuilder := range branchBuilders {
		movements = append(movements, branchBuilder(round))
	}
	return append(movements, joinBuilder(round))
}

func validateJoinBuilders(
	joinName string,
	branchBuilders []func(round JoinRound) StepMovement,
	joinBuilder func(round JoinRound) StepMovement,
) error {
	for index, branchBuilder := range branchBuilders {
		if branchBuilder == nil {
			return fmt.Errorf("dex: Join %q branch builder %d is nil", joinName, index)
		}
	}
	if joinBuilder == nil {
		return fmt.Errorf("dex: Join %q join builder is nil", joinName)
	}
	return nil
}

func validateJoinRoundKey(joinName string, roundKey string) error {
	if roundKey == "" {
		return fmt.Errorf("dex: Join %q round key must not be empty", joinName)
	}
	if strings.Contains(roundKey, "/") {
		return fmt.Errorf("dex: Join %q round key %q must not contain '/'", joinName, roundKey)
	}
	return nil
}

func validateJoinRound(joinName string, round JoinRound) error {
	if err := validateJoinRoundKey(joinName, round.RoundKey); err != nil {
		return err
	}
	if round.BranchCount < 0 {
		return fmt.Errorf(
			"dex: Join %q branch count %d must not be negative",
			joinName,
			round.BranchCount,
		)
	}
	return nil
}

func validateJoinBranchCount(joinName string, round JoinRound, count int) error {
	if count < 1 || count > round.BranchCount {
		return fmt.Errorf(
			"dex: Join %q count %d must be between 1 and the branch count %d",
			joinName,
			count,
			round.BranchCount,
		)
	}
	return nil
}
