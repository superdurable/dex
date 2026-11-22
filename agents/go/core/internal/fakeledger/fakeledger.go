// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package fakeledger is a test Connector, fake-ledger, whose provider keeps its writes in files.
//
// It mirrors the Python core's tests/fake_ledger, so a Go test drives the same
// scenarios: switches in an account ID or a text pick a provider behavior,
// such as a lost response, a refusal, or a gate a test kills the Worker at.
// The files live in a directory, so they survive a Worker that a test kills.
package fakeledger

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/singledispatch"
	"github.com/superdurable/dex/sdk-go/dex"
)

// ConnectorID is the test Connector's ID.
const ConnectorID = "fake-ledger"

// Connection is the logical connection every test tool runs with.
var Connection = sdkgo.ConnectionRef{Provider: ConnectorID, Name: "test-ledger"}

// ExecuteMethodTimeout is every operation's Execute timeout.
const ExecuteMethodTimeout = 30 * time.Second

const gatePollInterval = 20 * time.Millisecond

// GetBalanceInput names one account. Account IDs pick a behavior: "missing" selects notFound,
// "invalid" invalidResponse, "retry-once" retries the first attempt, and "retry-always" every one.
type GetBalanceInput struct {
	AccountID string `json:"accountId"`
	Padding   int    `json:"padding,omitempty"`
}

// Balance is one account's balance.
type Balance struct {
	AccountID string `json:"accountId"`
	Amount    int    `json:"amount"`
	Filler    string `json:"filler,omitempty"`
}

// CreatePayoutInput is one payout, written once per idempotency key. Memo "reject" refuses it.
type CreatePayoutInput struct {
	AccountID string `json:"accountId"`
	Amount    int    `json:"amount"`
	Memo      string `json:"memo,omitempty"`
}

// Payout is a payout the provider made.
type Payout struct {
	PayoutID string `json:"payoutId"`
	Amount   int    `json:"amount"`
}

// TextInput is a note or a message: its words switch provider behaviors.
type TextInput struct {
	AccountID string `json:"accountId"`
	Text      string `json:"text"`
}

// Stored is a note or message the provider stored.
type Stored struct {
	ID string `json:"id,omitempty"`
}

// Provider is the fake provider: append-only files of writes and sends in a directory.
type Provider struct {
	dir   string
	mutex sync.Mutex
}

// NewProvider keeps the provider's files in dir, which must exist.
func NewProvider(dir string) *Provider {
	return &Provider{dir: dir}
}

type entry struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
	ID        string `json:"id,omitempty"`
	IsHidden  bool   `json:"hidden,omitempty"`
}

// Writes returns every write of an operation that took effect, oldest first.
func (provider *Provider) Writes(operation string) []string {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	ids := []string{}
	for _, written := range provider.readEntries("writes.jsonl") {
		if written.Operation == operation {
			ids = append(ids, written.ID)
		}
	}
	return ids
}

// Sends returns how many requests of an operation reached the provider, refused ones included.
func (provider *Provider) Sends(operation string) int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	count := 0
	for _, sent := range provider.readEntries("sends.jsonl") {
		if sent.Operation == operation {
			count++
		}
	}
	return count
}

// Calls returns how many times an operation's Invoke ran.
func (provider *Provider) Calls(operation string) int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	count := 0
	for _, called := range provider.readEntries("calls.jsonl") {
		if called.Operation == operation {
			count++
		}
	}
	return count
}

func (provider *Provider) recordCall(operation string, key string) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.appendEntry("calls.jsonl", entry{Operation: operation, Key: key})
}

func (provider *Provider) accept(operation string, key string, shouldDeduplicate bool, isHidden bool) string {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.appendEntry("sends.jsonl", entry{Operation: operation, Key: key})
	writes := provider.readEntries("writes.jsonl")
	if shouldDeduplicate {
		for _, written := range writes {
			if written.Operation == operation && written.Key == key {
				return written.ID
			}
		}
	}
	id := fmt.Sprintf("%s-%d", operation, len(writes)+1)
	provider.appendEntry("writes.jsonl", entry{Operation: operation, Key: key, ID: id, IsHidden: isHidden})
	return id
}

func (provider *Provider) recordRefused(operation string, key string) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.appendEntry("sends.jsonl", entry{Operation: operation, Key: key})
}

// find returns the visible write of a key, as a provider's read-back would.
func (provider *Provider) find(operation string, key string) (string, bool) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	for _, written := range provider.readEntries("writes.jsonl") {
		if written.Operation == operation && written.Key == key && !written.IsHidden {
			return written.ID, true
		}
	}
	return "", false
}

func (provider *Provider) appendEntry(fileName string, value entry) {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	file, err := os.OpenFile(filepath.Join(provider.dir, fileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		panic(err)
	}
	if err := file.Sync(); err != nil {
		panic(err)
	}
}

func (provider *Provider) readEntries(fileName string) []entry {
	file, err := os.Open(filepath.Join(provider.dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		panic(err)
	}
	defer func() { _ = file.Close() }()
	entries := []entry{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var read entry
		if err := json.Unmarshal(scanner.Bytes(), &read); err == nil {
			entries = append(entries, read)
		}
	}
	return entries
}

// waitAtGate writes "<name>.reached" and waits for "<name>.release" or the end of ctx.
func (provider *Provider) waitAtGate(ctx context.Context, name string) {
	if err := os.WriteFile(filepath.Join(provider.dir, name+".reached"), nil, 0o600); err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(provider.dir, name+".release")); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(gatePollInterval):
		}
	}
}

func branches(happyPath sdkgo.BranchID, optional ...sdkgo.BranchID) []sdkgo.BranchDefinition {
	defined := []sdkgo.BranchDefinition{{ID: happyPath, Description: "The " + string(happyPath) + " branch."}}
	for _, branch := range optional {
		defined = append(defined, sdkgo.BranchDefinition{ID: branch, Description: "The " + string(branch) + " branch.", Optional: true})
	}
	return defined
}

func stepDefaults() sdkgo.StepDefaults {
	return sdkgo.StepDefaults{ExecuteMethodTimeout: ExecuteMethodTimeout, ExecuteDurability: dex.StepDurabilitySync}
}

func failure(operation string, kind sdkgo.FailureKind, message string) sdkgo.Failure {
	return sdkgo.Failure{Kind: kind, Provider: ConnectorID, Operation: operation, Message: message}
}

func hasWord(text string, word string) bool {
	for _, candidate := range strings.Fields(text) {
		if candidate == word {
			return true
		}
	}
	return false
}

// gateName returns the name after "<prefix>:" in a text, or "".
func gateName(text string, prefix string) string {
	for _, word := range strings.Fields(text) {
		if name, isGate := strings.CutPrefix(word, prefix+":"); isGate {
			return name
		}
	}
	return ""
}

// GetBalance is the Query getBalance.
type GetBalance struct {
	Provider *Provider
}

// GetBalanceDefinition is getBalance's definition.
func GetBalanceDefinition() sdkgo.QueryDefinition {
	return sdkgo.QueryDefinition{
		Operation:    sdkgo.OperationRef{ConnectorID: ConnectorID, OperationID: "getBalance"},
		Branches:     branches("found", "notFound", "invalidResponse", sdkgo.DefectBranchID),
		StepDefaults: stepDefaults(),
	}
}

// Definition returns the definition.
func (GetBalance) Definition() sdkgo.QueryDefinition { return GetBalanceDefinition() }

// Invoke reads the balance.
func (operation GetBalance) Invoke(call sdkgo.Call, input GetBalanceInput) sdkgo.QueryAttempt[Balance] {
	operation.Provider.recordCall("getBalance", input.AccountID)
	switch input.AccountID {
	case "missing":
		missing := failure("getBalance", sdkgo.FailureNotFound, "no such account")
		return sdkgo.NewQueryBranch("notFound", Balance{}, &missing, sdkgo.Receipt{})
	case "invalid":
		invalid := failure("getBalance", sdkgo.FailureProtocol, "the ledger answered nonsense")
		return sdkgo.NewQueryBranch("invalidResponse", Balance{}, &invalid, sdkgo.Receipt{})
	case "retry-always":
		return sdkgo.NewQueryRetry[Balance](failure("getBalance", sdkgo.FailureAvailability, "ledger busy"), 0)
	case "retry-once":
		if call.Context.Attempt() == 1 {
			return sdkgo.NewQueryRetry[Balance](failure("getBalance", sdkgo.FailureAvailability, "ledger busy"), 0)
		}
	}
	return sdkgo.NewQueryBranch("found", Balance{AccountID: input.AccountID, Amount: 1250, Filler: strings.Repeat("x", input.Padding)},
		nil, sdkgo.Receipt{})
}

// GetBalanceMetadata is getBalance's reviewed agent metadata.
func GetBalanceMetadata() sdkgo.AgentToolMetadata {
	definition := GetBalanceDefinition()
	return sdkgo.AgentToolMetadata{
		Operation:      definition.Operation,
		Kind:           sdkgo.AgentOperationKindQuery,
		ToolName:       "fake-ledger_get_balance",
		Description:    "Read the balance of one ledger account.",
		BranchOutcomes: map[sdkgo.BranchID]sdkgo.AgentBranchOutcome{"notFound": sdkgo.AgentBranchOutcomeRejected},
		InputType:      "GetBalanceInput",
		InputSchema: json.RawMessage(`{"type": "object", "description": "GetBalanceInput names one account.", ` +
			`"additionalProperties": false, "properties": {"accountId": {"type": "string", "description": "The account."}, ` +
			`"padding": {"type": "integer", "description": "Bytes of filler to add to the result."}}, "required": ["accountId"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}

// CreatePayout is the retry-safe Mutation createPayout, whose manifest recommends approval.
type CreatePayout struct {
	Provider *Provider
}

// CreatePayoutDefinition is createPayout's definition.
func CreatePayoutDefinition() sdkgo.MutationDefinition {
	return sdkgo.MutationDefinition{
		Operation:       sdkgo.OperationRef{ConnectorID: ConnectorID, OperationID: "createPayout"},
		Branches:        branches("paid", "providerRejected", sdkgo.UncertainBranchID, "invalidResponse", sdkgo.DefectBranchID),
		StepDefaults:    stepDefaults(),
		RepeatOnUnknown: sdkgo.RepeatOnUnknownSafe,
	}
}

// Definition returns the definition.
func (CreatePayout) Definition() sdkgo.MutationDefinition { return CreatePayoutDefinition() }

// IdempotencyKey is the Call ID.
func (CreatePayout) IdempotencyKey(callID sdkgo.CallID, _ CreatePayoutInput) sdkgo.IdempotencyKey {
	return sdkgo.IdempotencyKey(callID)
}

// Invoke makes the payout once per idempotency key.
func (operation CreatePayout) Invoke(call sdkgo.Call, input CreatePayoutInput) sdkgo.MutationAttempt[Payout] {
	key := string(call.IdempotencyKey)
	operation.Provider.recordCall("createPayout", key)
	if input.Memo == "reject" {
		refused := failure("createPayout", sdkgo.FailureProviderRejection, "payouts are paused")
		return sdkgo.NewMutationBranch("providerRejected", Payout{}, &refused, sdkgo.Receipt{})
	}
	id := operation.Provider.accept("createPayout", key, true, false)
	return sdkgo.NewMutationBranch("paid", Payout{PayoutID: id, Amount: input.Amount}, nil, sdkgo.Receipt{ProviderObjectID: id})
}

// CreatePayoutMetadata is createPayout's reviewed agent metadata.
func CreatePayoutMetadata() sdkgo.AgentToolMetadata {
	definition := CreatePayoutDefinition()
	return sdkgo.AgentToolMetadata{
		Operation:             definition.Operation,
		Kind:                  sdkgo.AgentOperationKindMutation,
		ToolName:              "fake-ledger_create_payout",
		Description:           "Pay an amount out of one ledger account.",
		IsApprovalRecommended: true,
		IsRetrySafe:           true,
		RepeatOnUnknown:       sdkgo.RepeatOnUnknownSafe,
		BranchOutcomes:        map[sdkgo.BranchID]sdkgo.AgentBranchOutcome{"providerRejected": sdkgo.AgentBranchOutcomeRejected},
		InputType:             "CreatePayoutInput",
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {` +
			`"accountId": {"type": "string"}, "amount": {"type": "integer", "minimum": 1}, "memo": {"type": "string"}}, ` +
			`"required": ["accountId", "amount"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}

// PostNote is the single-attempt Mutation postNote. Text "reject" refuses it, "retry" asks to
// retry after the provider stored nothing, and "gate-after-send:<name>" waits at a gate after the
// note is stored.
type PostNote struct {
	Provider *Provider
}

// PostNoteDefinition is postNote's definition.
func PostNoteDefinition() sdkgo.MutationDefinition {
	return sdkgo.MutationDefinition{
		Operation:       sdkgo.OperationRef{ConnectorID: ConnectorID, OperationID: "postNote"},
		Branches:        branches("posted", "providerRejected", sdkgo.UncertainBranchID, sdkgo.DefectBranchID),
		StepDefaults:    stepDefaults(),
		RepeatOnUnknown: sdkgo.RepeatOnUnknownUnsafe,
	}
}

// Definition returns the definition.
func (PostNote) Definition() sdkgo.MutationDefinition { return PostNoteDefinition() }

// IdempotencyKey is the Call ID.
func (PostNote) IdempotencyKey(callID sdkgo.CallID, _ TextInput) sdkgo.IdempotencyKey {
	return sdkgo.IdempotencyKey(callID)
}

// Invoke stores the note.
func (operation PostNote) Invoke(call sdkgo.Call, input TextInput) sdkgo.MutationAttempt[Stored] {
	key := string(call.IdempotencyKey)
	operation.Provider.recordCall("postNote", key)
	switch {
	case hasWord(input.Text, "reject"):
		refused := failure("postNote", sdkgo.FailureProviderRejection, "account is closed")
		return sdkgo.NewMutationBranch("providerRejected", Stored{}, &refused, sdkgo.Receipt{})
	case hasWord(input.Text, "retry"):
		return sdkgo.NewMutationRetry[Stored](failure("postNote", sdkgo.FailureRateLimit, "slow down"), 0)
	}
	id := operation.Provider.accept("postNote", key, false, false)
	if gate := gateName(input.Text, "gate-after-send"); gate != "" && call.Context.Attempt() == 1 {
		operation.Provider.waitAtGate(call.Context, gate)
	}
	return sdkgo.NewMutationBranch("posted", Stored{ID: id}, nil, sdkgo.Receipt{ProviderObjectID: id})
}

// PostNoteMetadata is postNote's reviewed agent metadata.
func PostNoteMetadata() sdkgo.AgentToolMetadata {
	definition := PostNoteDefinition()
	return sdkgo.AgentToolMetadata{
		Operation:       definition.Operation,
		Kind:            sdkgo.AgentOperationKindMutation,
		ToolName:        "fake-ledger_post_note",
		Description:     "Post a note on one ledger account.",
		RepeatOnUnknown: sdkgo.RepeatOnUnknownUnsafe,
		BranchOutcomes:  map[sdkgo.BranchID]sdkgo.AgentBranchOutcome{"providerRejected": sdkgo.AgentBranchOutcomeRejected},
		InputType:       "TextInput",
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {` +
			`"accountId": {"type": "string"}, "text": {"type": "string"}}, "required": ["accountId", "text"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}

// SendMessage is the Mutation sendMessage, whose RepeatOnUnknown is limited, behind the
// single-dispatch fence. Text words: "lose-always" loses every response, "lose-first" and
// "lose-visible" the first; "lose-visible" keeps the lost write visible to read-back; "reject"
// refuses before a send, "reject-on-send" after one, "reject-later" on every later attempt; and
// "gate-after-send:<name>" waits at a gate after the first send.
type SendMessage struct {
	Provider *Provider
	// MaxSends is the definition's cap; zero means 3, and -1 a definition without the tier.
	MaxSends int32
}

// SendMessageDefinition is sendMessage's definition with a cap, or without the tier for -1.
func SendMessageDefinition(maxSends int32) sdkgo.MutationDefinition {
	definition := sdkgo.MutationDefinition{
		Operation: sdkgo.OperationRef{ConnectorID: ConnectorID, OperationID: "sendMessage"},
		Branches:  branches("sent", "providerRejected", sdkgo.UncertainBranchID, sdkgo.DefectBranchID),
		StepDefaults: sdkgo.StepDefaults{
			ExecuteMethodTimeout: ExecuteMethodTimeout,
			ExecuteDurability:    dex.StepDurabilitySync,
			// Five attempts reach the send cap of 3 and one read-back after it.
			ExecuteRetry: &dex.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 1, MaximumInterval: time.Second,
				MaximumAttempts: 5, TotalDuration: 10 * time.Minute},
		},
		RepeatOnUnknown: sdkgo.RepeatOnUnknownLimited,
		MaxSends:        3,
	}
	switch {
	case maxSends < 0:
		definition.RepeatOnUnknown, definition.MaxSends = "", 0
	case maxSends > 0:
		definition.MaxSends = maxSends
	}
	return definition
}

// Definition returns the definition.
func (operation SendMessage) Definition() sdkgo.MutationDefinition {
	return SendMessageDefinition(operation.MaxSends)
}

// IdempotencyKey is the Call ID, which read-back looks up.
func (SendMessage) IdempotencyKey(callID sdkgo.CallID, _ TextInput) sdkgo.IdempotencyKey {
	return sdkgo.IdempotencyKey(callID)
}

// Invoke sends the message once per decision of the fence.
func (operation SendMessage) Invoke(call sdkgo.Call, input TextInput) sdkgo.MutationAttempt[Stored] {
	key := string(call.IdempotencyKey)
	attempt := call.Context.Attempt()
	operation.Provider.recordCall("sendMessage", key)
	if hasWord(input.Text, "reject-later") && attempt > 1 {
		refused := failure("sendMessage", sdkgo.FailureProviderRejection, "refused on a later attempt")
		return sdkgo.NewMutationBranch("providerRejected", Stored{}, &refused, sdkgo.Receipt{})
	}
	// The fence decides before credentials or provider lookups, as the contract requires.
	fence, err := singledispatch.NewFence(call, ExecuteMethodTimeout, singledispatch.WithMaxSends(call.MaxSends()))
	if err != nil {
		return sdkgo.NewMutationUncertain(Stored{}, failure("sendMessage", sdkgo.FailureLocalDefect, err.Error()), sdkgo.Receipt{})
	}
	decision := fence.Decide()
	if decision.Kind == singledispatch.FailClosed {
		return sdkgo.NewMutationUncertain(Stored{}, failure("sendMessage", sdkgo.FailureLocalDefect, "fail closed"), sdkgo.Receipt{})
	}
	if hasWord(input.Text, "reject") {
		refused := failure("sendMessage", sdkgo.FailureProviderRejection, "channel archived")
		return sdkgo.NewMutationBranch("providerRejected", Stored{}, &refused, sdkgo.Receipt{})
	}
	if decision.Kind == singledispatch.ReadBackRequired || decision.Kind == singledispatch.ReadBackThenResendAllowed {
		if id, isFound := operation.Provider.find("sendMessage", key); isFound {
			return sdkgo.NewMutationBranch("sent", Stored{ID: id}, nil, sdkgo.Receipt{ProviderObjectID: id})
		}
		if decision.Kind == singledispatch.ReadBackRequired {
			return sdkgo.NewMutationUncertain(Stored{}, failure("sendMessage", sdkgo.FailureTransport, "read-back found no message"), sdkgo.Receipt{})
		}
	}
	isLost := hasWord(input.Text, "lose-always") ||
		((hasWord(input.Text, "lose-first") || hasWord(input.Text, "lose-visible")) && attempt == 1)
	var id string
	err = fence.Dispatch(func(ctx context.Context) error {
		if hasWord(input.Text, "reject-on-send") {
			operation.Provider.recordRefused("sendMessage", key)
			return nil
		}
		id = operation.Provider.accept("sendMessage", key, false, isLost && !hasWord(input.Text, "lose-visible"))
		if gate := gateName(input.Text, "gate-after-send"); gate != "" && attempt == 1 {
			operation.Provider.waitAtGate(ctx, gate)
		}
		return nil
	})
	if err != nil {
		return sdkgo.NewMutationRetry[Stored](failure("sendMessage", sdkgo.FailureTransport, "dispatch refused: "+err.Error()), 0)
	}
	if id == "" {
		if err := fence.RecordCanResend(); err != nil {
			return sdkgo.NewMutationUncertain(Stored{}, failure("sendMessage", sdkgo.FailureLocalDefect, err.Error()), sdkgo.Receipt{})
		}
		refused := failure("sendMessage", sdkgo.FailureProviderRejection, "message refused")
		return sdkgo.NewMutationBranch("providerRejected", Stored{}, &refused, sdkgo.Receipt{})
	}
	if isLost {
		// The response never arrived: the outcome is unknown, and the cap decides a resend.
		return sdkgo.NewMutationRetry[Stored](failure("sendMessage", sdkgo.FailureTransport, "response lost"), 0)
	}
	_ = fence.RecordProviderID(id)
	return sdkgo.NewMutationBranch("sent", Stored{ID: id}, nil, sdkgo.Receipt{ProviderObjectID: id})
}

// SendMessageMetadata is sendMessage's reviewed agent metadata for a definition cap, or without the
// tier for -1.
func SendMessageMetadata(maxSends int32) sdkgo.AgentToolMetadata {
	definition := SendMessageDefinition(maxSends)
	return sdkgo.AgentToolMetadata{
		Operation:       definition.Operation,
		Kind:            sdkgo.AgentOperationKindMutation,
		ToolName:        "fake-ledger_send_message",
		Description:     "Send a message to one ledger account.",
		RepeatOnUnknown: definition.RepeatOnUnknown,
		MaxSends:        definition.MaxSends,
		BranchOutcomes:  map[sdkgo.BranchID]sdkgo.AgentBranchOutcome{"providerRejected": sdkgo.AgentBranchOutcomeRejected},
		InputType:       "TextInput",
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {` +
			`"accountId": {"type": "string"}, "text": {"type": "string"}}, "required": ["accountId", "text"]}`),
		Branches:     definition.Branches,
		StepDefaults: definition.StepDefaults,
	}
}
