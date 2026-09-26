// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package sequentiallychunkedattributemap

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	FlowID               = "chunked-subscriber-archive"
	CurrentChunkInstance = "current"
	SubscriberChunkSize  = 100
)

type Subscriber struct {
	Sequence        uint64 `json:"sequence"`
	SubscriberID    string `json:"subscriberId"`
	DeliveryAddress string `json:"deliveryAddress"`
}

type SubscriberChunk struct {
	Subscribers []Subscriber `json:"subscribers"`
}

type SubscriberArchiveState struct {
	NextSequence             uint64 `json:"nextSequence"`
	LatestArchivedChunkToken string `json:"latestArchivedChunkToken,omitempty"`
}

type RegisterSubscriberInput struct {
	SubscriberID    string `json:"subscriberId"`
	DeliveryAddress string `json:"deliveryAddress"`
}

type GetSubscriberPageInput struct {
	PageToken string `json:"pageToken"`
}

type SubscriberPage struct {
	PageToken     string       `json:"pageToken"`
	Subscribers   []Subscriber `json:"subscribers"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

type ChunkedSubscriberFlow struct {
	dex.FlowDefaults
}

var (
	ArchiveState     = dex.DefineAttribute[SubscriberArchiveState]("subscriber_archive_state")
	SubscriberChunks = dex.DefineAttributeMap[SubscriberChunk]("subscriber_chunks")
)

func NewChunkedSubscriberFlow() *ChunkedSubscriberFlow {
	return &ChunkedSubscriberFlow{}
}

func (*ChunkedSubscriberFlow) GetFlowType() string {
	return "ChunkedSubscriberFlow"
}

func (*ChunkedSubscriberFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow *ChunkedSubscriberFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.RegisterSubscriber, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{
				dex.LockAttribute(ArchiveState),
				dex.LockAttributeMap(SubscriberChunks, CurrentChunkInstance),
			},
			LoadAttributeMapInstances: []dex.AttributeMapLoad{
				SubscriberChunks.Load(CurrentChunkInstance),
			},
		}),
		dex.DefineRPC(flow.GetSubscriberPage, nil),
	}
}

func (*ChunkedSubscriberFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{ArchiveState, SubscriberChunks}}
}

func (*ChunkedSubscriberFlow) RegisterSubscriber(
	ctx dex.Context,
	input RegisterSubscriberInput,
) (*dex.RPCResult[Subscriber], error) {
	if input.SubscriberID == "" {
		return nil, fmt.Errorf("subscriberId is required")
	}
	if input.DeliveryAddress == "" {
		return nil, fmt.Errorf("deliveryAddress is required")
	}

	archiveState, err := ArchiveState.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentChunk, err := SubscriberChunks.Get(ctx, CurrentChunkInstance)
	if err != nil {
		return nil, err
	}

	subscriber := Subscriber{
		Sequence:        archiveState.NextSequence,
		SubscriberID:    input.SubscriberID,
		DeliveryAddress: input.DeliveryAddress,
	}
	if len(currentChunk.Subscribers) == SubscriberChunkSize {
		archiveToken := ArchiveChunkToken(currentChunk.Subscribers[0].Sequence)
		if err := SubscriberChunks.Set(ctx, archiveToken, currentChunk); err != nil {
			return nil, err
		}
		archiveState.LatestArchivedChunkToken = archiveToken
		currentChunk = SubscriberChunk{Subscribers: []Subscriber{subscriber}}
	} else {
		currentChunk.Subscribers = append(currentChunk.Subscribers, subscriber)
	}
	archiveState.NextSequence++
	if err := SubscriberChunks.Set(ctx, CurrentChunkInstance, currentChunk); err != nil {
		return nil, err
	}
	if err := ArchiveState.Set(ctx, archiveState); err != nil {
		return nil, err
	}
	return &dex.RPCResult[Subscriber]{Output: subscriber}, nil
}

func (*ChunkedSubscriberFlow) GetSubscriberPage(
	ctx dex.Context,
	input GetSubscriberPageInput,
) (*dex.RPCResult[SubscriberPage], error) {
	pageToken, err := ValidateSubscriberPageToken(input.PageToken)
	if err != nil {
		return nil, err
	}
	chunk, err := SubscriberChunks.Get(ctx, pageToken)
	if err != nil {
		return nil, fmt.Errorf("subscriber page %q not found: %w", pageToken, err)
	}
	page := SubscriberPage{
		PageToken:   pageToken,
		Subscribers: newestFirst(chunk.Subscribers),
	}
	if len(chunk.Subscribers) == 0 {
		return &dex.RPCResult[SubscriberPage]{Output: page}, nil
	}
	if pageToken == CurrentChunkInstance {
		archiveState, stateErr := ArchiveState.Get(ctx)
		if stateErr != nil {
			return nil, stateErr
		}
		page.NextPageToken = archiveState.LatestArchivedChunkToken
	} else {
		firstSequence, parseErr := strconv.ParseUint(pageToken, 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		if firstSequence > 1 {
			page.NextPageToken = ArchiveChunkToken(firstSequence - SubscriberChunkSize)
		}
	}
	return &dex.RPCResult[SubscriberPage]{Output: page}, nil
}

func StartOptions() (dex.StartFlowOptions, error) {
	archiveState, err := dex.InitialAttribute(
		ArchiveState,
		SubscriberArchiveState{NextSequence: 1},
	)
	if err != nil {
		return dex.StartFlowOptions{}, err
	}
	currentChunk, err := dex.InitialAttributeMapValue(
		SubscriberChunks,
		CurrentChunkInstance,
		SubscriberChunk{Subscribers: []Subscriber{}},
	)
	if err != nil {
		return dex.StartFlowOptions{}, err
	}
	return dex.StartFlowOptions{
		Attributes:     []dex.InitialAttributeDef{archiveState, currentChunk},
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
	}, nil
}

func ValidateSubscriberPageToken(pageToken string) (string, error) {
	if pageToken == "" {
		return CurrentChunkInstance, nil
	}
	if pageToken == CurrentChunkInstance {
		return pageToken, nil
	}
	if len(pageToken) != 20 || strings.Trim(pageToken, "0123456789") != "" {
		return "", fmt.Errorf("invalid page token %q", pageToken)
	}
	firstSequence, err := strconv.ParseUint(pageToken, 10, 64)
	if err != nil || firstSequence == 0 || (firstSequence-1)%SubscriberChunkSize != 0 {
		return "", fmt.Errorf("invalid page token %q", pageToken)
	}
	return pageToken, nil
}

func ArchiveChunkToken(firstSequence uint64) string {
	return fmt.Sprintf("%020d", firstSequence)
}

func newestFirst(subscribers []Subscriber) []Subscriber {
	result := make([]Subscriber, len(subscribers))
	for index := range subscribers {
		result[len(subscribers)-1-index] = subscribers[index]
	}
	return result
}

var (
	_ dex.Flow                                        = (*ChunkedSubscriberFlow)(nil)
	_ dex.RPC[RegisterSubscriberInput, Subscriber]    = (*ChunkedSubscriberFlow)(nil).RegisterSubscriber
	_ dex.RPC[GetSubscriberPageInput, SubscriberPage] = (*ChunkedSubscriberFlow)(nil).GetSubscriberPage
)
