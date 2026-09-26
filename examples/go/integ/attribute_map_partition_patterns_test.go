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

package integ

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	hashpartitioned "github.com/superdurable/dex/examples/go/patterns/hash-partitioned-attribute-map"
	sequentiallychunked "github.com/superdurable/dex/examples/go/patterns/sequentially-chunked-attribute-map"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestSequentiallyChunkedAttributeMapArchivesAndPagesSubscribers(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "chunked-subscribers")
	startOptions, err := sequentiallychunked.StartOptions()
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, registry.ChunkedSubscriber, flowID, nil, startOptions)
	require.NoError(t, err)
	emptyPage := getSubscriberPage(t, ctx, flowID, sequentiallychunked.CurrentChunkInstance)
	require.Empty(t, emptyPage.Subscribers)
	require.Empty(t, emptyPage.NextPageToken)

	for subscriberNumber := 1; subscriberNumber <= 201; subscriberNumber++ {
		var subscriber sequentiallychunked.Subscriber
		err = integClient.InvokeRPC(
			ctx,
			flowID,
			registry.ChunkedSubscriber.RegisterSubscriber,
			sequentiallychunked.RegisterSubscriberInput{
				SubscriberID:    fmt.Sprintf("subscriber-%03d", subscriberNumber),
				DeliveryAddress: fmt.Sprintf("subscriber-%03d@example.com", subscriberNumber),
			},
			&subscriber,
		)
		require.NoError(t, err)
		require.Equal(t, uint64(subscriberNumber), subscriber.Sequence)
	}

	currentPage := getSubscriberPage(t, ctx, flowID, sequentiallychunked.CurrentChunkInstance)
	require.Len(t, currentPage.Subscribers, 1)
	require.Equal(t, uint64(201), currentPage.Subscribers[0].Sequence)
	require.Equal(t, sequentiallychunked.ArchiveChunkToken(101), currentPage.NextPageToken)

	secondPage := getSubscriberPage(t, ctx, flowID, currentPage.NextPageToken)
	require.Len(t, secondPage.Subscribers, sequentiallychunked.SubscriberChunkSize)
	require.Equal(t, uint64(200), secondPage.Subscribers[0].Sequence)
	require.Equal(t, uint64(101), secondPage.Subscribers[99].Sequence)
	require.Equal(t, sequentiallychunked.ArchiveChunkToken(1), secondPage.NextPageToken)

	oldestPage := getSubscriberPage(t, ctx, flowID, secondPage.NextPageToken)
	require.Len(t, oldestPage.Subscribers, sequentiallychunked.SubscriberChunkSize)
	require.Equal(t, uint64(100), oldestPage.Subscribers[0].Sequence)
	require.Equal(t, uint64(1), oldestPage.Subscribers[99].Sequence)
	require.Empty(t, oldestPage.NextPageToken)

	missingPageToken := sequentiallychunked.ArchiveChunkToken(301)
	var missingPage sequentiallychunked.SubscriberPage
	err = integClient.InvokeRPCWithOptions(
		ctx,
		flowID,
		registry.ChunkedSubscriber.GetSubscriberPage,
		sequentiallychunked.GetSubscriberPageInput{PageToken: missingPageToken},
		&missingPage,
		chunkLoadOptions(missingPageToken),
	)
	require.ErrorContains(t, err, "not found")
}

func TestSequentiallyChunkedAttributeMapRetriesConcurrentBoundaryAppends(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "concurrent-chunked-subscribers")
	startOptions, err := sequentiallychunked.StartOptions()
	require.NoError(t, err)
	_, err = integClient.StartFlow(ctx, registry.ChunkedSubscriber, flowID, nil, startOptions)
	require.NoError(t, err)

	for subscriberNumber := 1; subscriberNumber <= 95; subscriberNumber++ {
		_, err = registerSubscriber(
			ctx,
			flowID,
			subscriberRegistrationInput(subscriberNumber),
		)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	errorsBySubscriber := make(chan error, 20)
	for subscriberNumber := 96; subscriberNumber <= 115; subscriberNumber++ {
		subscriberNumber := subscriberNumber
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, registerErr := registerSubscriberWithLockRetry(
				ctx,
				flowID,
				subscriberRegistrationInput(subscriberNumber),
			)
			errorsBySubscriber <- registerErr
		}()
	}
	wg.Wait()
	close(errorsBySubscriber)
	for registerErr := range errorsBySubscriber {
		require.NoError(t, registerErr)
	}

	currentPage := getSubscriberPage(t, ctx, flowID, sequentiallychunked.CurrentChunkInstance)
	archivedPage := getSubscriberPage(t, ctx, flowID, currentPage.NextPageToken)
	allSubscribers := append(currentPage.Subscribers, archivedPage.Subscribers...)
	require.Len(t, allSubscribers, 115)
	sort.Slice(allSubscribers, func(leftIndex, rightIndex int) bool {
		return allSubscribers[leftIndex].Sequence < allSubscribers[rightIndex].Sequence
	})
	for subscriberIndex, subscriber := range allSubscribers {
		require.Equal(t, uint64(subscriberIndex+1), subscriber.Sequence)
	}
}

func TestHashPartitionedAttributeMapUpsertsCollidingProfiles(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "customer-directory")
	_, err := integClient.StartFlow(
		ctx,
		registry.CustomerDirectory,
		flowID,
		nil,
		hashpartitioned.StartOptions(),
	)
	require.NoError(t, err)

	collidingProfiles := customerProfilesForOnePartition(t, 12)
	var wg sync.WaitGroup
	errorsByProfile := make(chan error, len(collidingProfiles))
	for _, profile := range collidingProfiles {
		profile := profile
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, invokeErr := upsertCustomerProfileWithLockRetry(ctx, flowID, profile)
			errorsByProfile <- invokeErr
		}()
	}
	wg.Wait()
	close(errorsByProfile)
	for invokeErr := range errorsByProfile {
		require.NoError(t, invokeErr)
	}

	for _, expectedProfile := range collidingProfiles {
		actualProfile := getCustomerProfile(t, ctx, flowID, " \t"+expectedProfile.EmailAddress+"\r\n")
		require.Equal(t, expectedProfile, actualProfile)
	}

	updatedProfile := collidingProfiles[0]
	updatedProfile.EmailAddress = "  " + updatedProfile.EmailAddress + "  "
	updatedProfile.FullName = "Updated Customer"
	savedProfile, err := upsertCustomerProfile(ctx, flowID, updatedProfile)
	require.NoError(t, err)
	require.Equal(t, collidingProfiles[0].EmailAddress, savedProfile.EmailAddress)
	require.Equal(t, "Updated Customer", savedProfile.FullName)

	_, partitionName, _, err := hashpartitioned.EmailPartition("missing@example.com")
	require.NoError(t, err)
	var missingProfile hashpartitioned.CustomerProfile
	err = integClient.InvokeRPCWithOptions(
		ctx,
		flowID,
		registry.CustomerDirectory.GetCustomerProfileByEmail,
		"missing@example.com",
		&missingProfile,
		partitionLoadOptions(partitionName),
	)
	require.ErrorContains(t, err, "not found")
}

func getSubscriberPage(
	t *testing.T,
	ctx context.Context,
	flowID string,
	pageToken string,
) sequentiallychunked.SubscriberPage {
	t.Helper()
	var page sequentiallychunked.SubscriberPage
	require.NoError(t, integClient.InvokeRPCWithOptions(
		ctx,
		flowID,
		registry.ChunkedSubscriber.GetSubscriberPage,
		sequentiallychunked.GetSubscriberPageInput{PageToken: pageToken},
		&page,
		chunkLoadOptions(pageToken),
	))
	return page
}

func chunkLoadOptions(pageToken string) dex.RPCInvokeOptions {
	return dex.RPCInvokeOptions{
		LoadAttributeMapInstances: []dex.AttributeMapLoad{
			sequentiallychunked.SubscriberChunks.Load(pageToken),
		},
	}
}

func subscriberRegistrationInput(
	subscriberNumber int,
) sequentiallychunked.RegisterSubscriberInput {
	return sequentiallychunked.RegisterSubscriberInput{
		SubscriberID:    fmt.Sprintf("subscriber-%03d", subscriberNumber),
		DeliveryAddress: fmt.Sprintf("subscriber-%03d@example.com", subscriberNumber),
	}
}

func registerSubscriber(
	ctx context.Context,
	flowID string,
	input sequentiallychunked.RegisterSubscriberInput,
) (sequentiallychunked.Subscriber, error) {
	var subscriber sequentiallychunked.Subscriber
	err := integClient.InvokeRPC(
		ctx,
		flowID,
		registry.ChunkedSubscriber.RegisterSubscriber,
		input,
		&subscriber,
	)
	return subscriber, err
}

func registerSubscriberWithLockRetry(
	ctx context.Context,
	flowID string,
	input sequentiallychunked.RegisterSubscriberInput,
) (sequentiallychunked.Subscriber, error) {
	for {
		subscriber, err := registerSubscriber(ctx, flowID, input)
		if err == nil {
			return subscriber, nil
		}
		var conflict *dex.RPCLockConflictError
		if !errors.As(err, &conflict) {
			return sequentiallychunked.Subscriber{}, err
		}
		if ctx.Err() != nil {
			return sequentiallychunked.Subscriber{}, ctx.Err()
		}
		runtime.Gosched()
	}
}

func customerProfilesForOnePartition(
	t *testing.T,
	profileCount int,
) []hashpartitioned.CustomerProfile {
	t.Helper()
	profilesByPartition := map[string][]hashpartitioned.CustomerProfile{}
	for candidateNumber := 0; ; candidateNumber++ {
		emailAddress := fmt.Sprintf("collision-%05d@example.com", candidateNumber)
		canonicalEmail, partitionName, _, err := hashpartitioned.EmailPartition(emailAddress)
		require.NoError(t, err)
		profiles := append(profilesByPartition[partitionName], hashpartitioned.CustomerProfile{
			EmailAddress: canonicalEmail,
			FullName:     fmt.Sprintf("Customer %05d", candidateNumber),
			CompanyName:  "Collision Test Company",
			CustomerTier: "gold",
		})
		profilesByPartition[partitionName] = profiles
		if len(profiles) == profileCount {
			return profiles
		}
	}
}

func upsertCustomerProfile(
	ctx context.Context,
	flowID string,
	profile hashpartitioned.CustomerProfile,
) (hashpartitioned.CustomerProfile, error) {
	_, partitionName, _, err := hashpartitioned.EmailPartition(profile.EmailAddress)
	if err != nil {
		return hashpartitioned.CustomerProfile{}, err
	}
	var savedProfile hashpartitioned.CustomerProfile
	err = integClient.InvokeRPCWithOptions(
		ctx,
		flowID,
		registry.CustomerDirectory.UpsertCustomerProfile,
		profile,
		&savedProfile,
		dex.RPCInvokeOptions{
			LockAttributeMapInstances: []dex.AttributeLock{
				dex.LockAttributeMap(hashpartitioned.CustomerProfilesByEmailPartition, partitionName),
			},
			LoadAttributeMapInstances: []dex.AttributeMapLoad{
				hashpartitioned.CustomerProfilesByEmailPartition.Load(partitionName),
			},
		},
	)
	return savedProfile, err
}

func upsertCustomerProfileWithLockRetry(
	ctx context.Context,
	flowID string,
	profile hashpartitioned.CustomerProfile,
) (hashpartitioned.CustomerProfile, error) {
	for {
		savedProfile, err := upsertCustomerProfile(ctx, flowID, profile)
		if err == nil {
			return savedProfile, nil
		}
		var conflict *dex.RPCLockConflictError
		if !errors.As(err, &conflict) {
			return hashpartitioned.CustomerProfile{}, err
		}
		if ctx.Err() != nil {
			return hashpartitioned.CustomerProfile{}, ctx.Err()
		}
		runtime.Gosched()
	}
}

func getCustomerProfile(
	t *testing.T,
	ctx context.Context,
	flowID string,
	emailAddress string,
) hashpartitioned.CustomerProfile {
	t.Helper()
	_, partitionName, _, err := hashpartitioned.EmailPartition(emailAddress)
	require.NoError(t, err)
	var profile hashpartitioned.CustomerProfile
	require.NoError(t, integClient.InvokeRPCWithOptions(
		ctx,
		flowID,
		registry.CustomerDirectory.GetCustomerProfileByEmail,
		emailAddress,
		&profile,
		partitionLoadOptions(partitionName),
	))
	return profile
}

func partitionLoadOptions(partitionName string) dex.RPCInvokeOptions {
	return dex.RPCInvokeOptions{
		LoadAttributeMapInstances: []dex.AttributeMapLoad{
			hashpartitioned.CustomerProfilesByEmailPartition.Load(partitionName),
		},
	}
}
