// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/streamstore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	streamTestFlowType = "stream-test-flow"
	streamTestRedisURL = "redis://127.0.0.1:6379/15"
)

func TestStreamStoreGlobalFIFOResumeAndIdempotency(t *testing.T) {
	store, redisClient := newStreamTestStore(t)
	streamName := "global-fifo-" + newRequestID()
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, streamName) })

	inputs := make([]streamstore.WriteInput, 11)
	for index := range inputs {
		flowID := "flow-a"
		if index%2 == 1 {
			flowID = "flow-b"
		}
		inputs[index] = streamInput(flowID, streamName, index, "value-0000")
	}
	charge := estimatedCharge(inputs[0], 1)
	leaseKey := streamTestBaseKey(streamName) + ":trim-lease"
	require.NoError(t, redisClient.Set(context.Background(), leaseKey, "held", 10*time.Second).Err())
	for index := 0; index < 10; index++ {
		inputs[index].StreamCapacityBytes = charge * 10
		require.NoError(t, store.Write(context.Background(), inputs[index]))
	}
	inputs[10].StreamCapacityBytes = charge * 10
	require.ErrorIs(t, store.Write(context.Background(), inputs[10]), streamstore.ErrCapacityExceeded)
	require.Equal(t, int64(10), streamLength(t, redisClient, streamName))
	require.NoError(t, redisClient.Del(context.Background(), leaseKey).Err())
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) == 8
	}, 3*time.Second, 10*time.Millisecond)

	flowAMessages := readAvailableMessages(t, store, "flow-a", streamName)
	flowBMessages := readAvailableMessages(t, store, "flow-b", streamName)
	requireStreamAccountingConsistent(t, redisClient, streamName)
	require.Equal(t, []string{"public-02", "public-04", "public-06", "public-08"}, messageKeys(flowAMessages))
	require.Equal(t, []string{"public-03", "public-05", "public-07", "public-09"}, messageKeys(flowBMessages))
	require.WithinDuration(t, time.Now(), flowAMessages[0].CreatedTime, 5*time.Second)

	firstToken, err := streamstore.EncodeResumeToken(
		streamTestFlowType,
		"flow-a",
		streamName,
		flowAMessages[0].MessageID,
	)
	require.NoError(t, err)
	nextMessage := readOneMessage(t, store, "flow-a", streamName, firstToken)
	require.Equal(t, "public-04", nextMessage.IdempotencyKey)

	duplicate := inputs[8]
	duplicate.Value = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "different"}}
	require.NoError(t, store.Write(context.Background(), duplicate))
	require.Equal(t, int64(8), streamLength(t, redisClient, streamName))
	beforeLastToken, err := streamstore.EncodeResumeToken(
		streamTestFlowType,
		"flow-a",
		streamName,
		flowAMessages[2].MessageID,
	)
	require.NoError(t, err)
	retainedOriginal := readOneMessage(t, store, "flow-a", streamName, beforeLastToken)
	require.Equal(t, "value-0000", retainedOriginal.Value.GetStringValue())

	trimTrigger := inputs[10]
	trimTrigger.StreamCapacityBytes = charge * 3
	require.ErrorIs(t, store.Write(context.Background(), trimTrigger), streamstore.ErrCapacityExceeded)
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) <= 2
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, store.Write(context.Background(), trimTrigger))
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) <= 2
	}, 3*time.Second, 10*time.Millisecond)
	requireStreamAccountingConsistent(t, redisClient, streamName)
	oldTokenMessage := readOneMessage(t, store, "flow-a", streamName, firstToken)
	require.Equal(t, "public-10", oldTokenMessage.IdempotencyKey)

	wrongScopeToken, err := streamstore.EncodeResumeToken(
		streamTestFlowType,
		"flow-b",
		streamName,
		flowBMessages[0].MessageID,
	)
	require.NoError(t, err)
	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()
	_, err = store.Read(readCtx, streamTestFlowType, "flow-a", streamName, wrongScopeToken)
	require.ErrorIs(t, err, streamstore.ErrInvalidResumeToken)
}

func TestStreamStoreFlowTypesIsolateStreamScopes(t *testing.T) {
	store, redisClient := newStreamTestStore(t)
	streamName := "flow-type-scope-" + newRequestID()
	firstFlowType := "first-flow-type"
	secondFlowType := "second-flow-type"
	t.Cleanup(func() { deleteStreamTestKeysForFlowType(t, redisClient, firstFlowType, streamName) })
	t.Cleanup(func() { deleteStreamTestKeysForFlowType(t, redisClient, secondFlowType, streamName) })

	baseInput := streamInput("shared-flow", streamName, 0, "payload-0000")
	charge := estimatedCharge(baseInput, 1)
	for _, flowType := range []string{firstFlowType, secondFlowType} {
		leaseKey := streamTestBaseKeyForFlowType(flowType, streamName) + ":trim-lease"
		require.NoError(t, redisClient.Set(context.Background(), leaseKey, "held", 10*time.Second).Err())
	}
	for index := 0; index < 2; index++ {
		firstInput := streamInput("shared-flow", streamName, index, "payload-0000")
		firstInput.FlowType = firstFlowType
		firstInput.StreamCapacityBytes = charge * 2
		require.NoError(t, store.Write(context.Background(), firstInput))

		secondInput := streamInput("shared-flow", streamName, index, "payload-0000")
		secondInput.FlowType = secondFlowType
		secondInput.StreamCapacityBytes = charge * 3
		require.NoError(t, store.Write(context.Background(), secondInput))
	}
	firstThird := streamInput("shared-flow", streamName, 2, "payload-0000")
	firstThird.FlowType = firstFlowType
	firstThird.StreamCapacityBytes = charge * 2
	require.ErrorIs(t, store.Write(context.Background(), firstThird), streamstore.ErrCapacityExceeded)
	secondThird := streamInput("shared-flow", streamName, 2, "payload-0000")
	secondThird.FlowType = secondFlowType
	secondThird.StreamCapacityBytes = charge * 3
	require.NoError(t, store.Write(context.Background(), secondThird))

	require.Equal(t, int64(2), streamLengthForFlowType(t, redisClient, firstFlowType, streamName))
	require.Equal(t, int64(3), streamLengthForFlowType(t, redisClient, secondFlowType, streamName))
	require.NotEqual(
		t,
		streamTestBaseKeyForFlowType(firstFlowType, streamName),
		streamTestBaseKeyForFlowType(secondFlowType, streamName),
	)
	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	firstMessage, err := store.Read(readCtx, firstFlowType, "shared-flow", streamName, "")
	cancelRead()
	require.NoError(t, err)
	firstToken, err := streamstore.EncodeResumeToken(
		firstFlowType,
		"shared-flow",
		streamName,
		firstMessage.MessageID,
	)
	require.NoError(t, err)
	wrongTypeCtx, cancelWrongTypeRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelWrongTypeRead()
	_, err = store.Read(wrongTypeCtx, secondFlowType, "shared-flow", streamName, firstToken)
	require.ErrorIs(t, err, streamstore.ErrInvalidResumeToken)
}

func TestStreamStoreTrimWatermarks(t *testing.T) {
	store, redisClient := newStreamTestStore(t)
	streamName := "trim-watermarks-" + newRequestID()
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, streamName) })

	baseInput := streamInput("flow-a", streamName, 0, "payload-0000")
	charge := estimatedCharge(baseInput, 1)
	capacity := charge * 10
	for index := 0; index < 8; index++ {
		input := streamInput("flow-a", streamName, index, "payload-0000")
		input.StreamCapacityBytes = capacity
		require.NoError(t, store.Write(context.Background(), input))
	}
	require.Never(t, func() bool {
		return streamLength(t, redisClient, streamName) != 8
	}, 200*time.Millisecond, 10*time.Millisecond)

	trigger := streamInput("flow-a", streamName, 8, "payload-0000")
	trigger.StreamCapacityBytes = capacity
	require.NoError(t, store.Write(context.Background(), trigger))
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) == 8
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{
		"public-01",
		"public-02",
		"public-03",
		"public-04",
		"public-05",
		"public-06",
		"public-07",
		"public-08",
	}, messageKeys(readAvailableMessages(t, store, "flow-a", streamName)))
	requireStreamAccountingConsistent(t, redisClient, streamName)
}

func TestStreamStoreTrimmedIdentityCanWriteAgain(t *testing.T) {
	store, redisClient := newStreamTestStore(t)
	streamName := "trimmed-idem-" + newRequestID()
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, streamName) })

	original := streamInput("flow-a", streamName, 0, "original-00")
	charge := estimatedCharge(original, 1)
	original.StreamCapacityBytes = charge * 100
	require.NoError(t, store.Write(context.Background(), original))
	for index := 1; index < 80; index++ {
		input := streamInput("flow-a", streamName, index, "filler-0000")
		input.StreamCapacityBytes = charge * 100
		require.NoError(t, store.Write(context.Background(), input))
	}
	trigger := streamInput("flow-a", streamName, 80, "trigger-000")
	trigger.StreamCapacityBytes = charge * 2
	require.ErrorIs(t, store.Write(context.Background(), trigger), streamstore.ErrCapacityExceeded)
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) <= 1
	}, 3*time.Second, 10*time.Millisecond)
	requireStreamAccountingConsistent(t, redisClient, streamName)

	original.Value = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "rewritten-0"}}
	original.StreamCapacityBytes = charge * 2
	require.NoError(t, store.Write(context.Background(), original))
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) <= 1
	}, 3*time.Second, 10*time.Millisecond)
	messages := readAvailableMessages(t, store, "flow-a", streamName)
	foundRewritten := false
	for _, message := range messages {
		if message.IdempotencyKey == original.PublicIdempotencyKey {
			foundRewritten = message.Value.GetStringValue() == "rewritten-0"
		}
	}
	require.True(t, foundRewritten)
}

func TestStreamStoreConcurrentTriggersUseSingletonTrimLease(t *testing.T) {
	firstStore, redisClient := newStreamTestStore(t)
	secondStore, _ := newStreamTestStore(t)
	streamName := "concurrent-trim-" + newRequestID()
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, streamName) })

	baseInput := streamInput("flow-a", streamName, 0, "payload-0000")
	charge := estimatedCharge(baseInput, 1)
	var writers sync.WaitGroup
	writeErrors := make(chan error, 80)
	for index := 0; index < 80; index++ {
		writers.Add(1)
		go func(messageIndex int) {
			defer writers.Done()
			input := streamInput("flow-a", streamName, messageIndex, "payload-0000")
			input.StreamCapacityBytes = charge * 100
			selectedStore := firstStore
			if messageIndex%2 == 1 {
				selectedStore = secondStore
			}
			writeErrors <- selectedStore.Write(context.Background(), input)
		}(index)
	}
	writers.Wait()
	close(writeErrors)
	for writeErr := range writeErrors {
		require.NoError(t, writeErr)
	}
	require.Equal(t, int64(80), streamLength(t, redisClient, streamName))

	leaseKey := streamTestBaseKey(streamName) + ":trim-lease"
	require.NoError(t, redisClient.Set(context.Background(), leaseKey, "failed-owner", 250*time.Millisecond).Err())
	firstTrigger := streamInput("flow-a", streamName, 80, "payload-0000")
	firstTrigger.StreamCapacityBytes = charge * 10
	secondTrigger := streamInput("flow-a", streamName, 81, "payload-0000")
	secondTrigger.StreamCapacityBytes = charge * 10
	triggerStart := make(chan struct{})
	triggerErrors := make(chan error, 2)
	go func() {
		<-triggerStart
		triggerErrors <- firstStore.Write(context.Background(), firstTrigger)
	}()
	go func() {
		<-triggerStart
		triggerErrors <- secondStore.Write(context.Background(), secondTrigger)
	}()
	close(triggerStart)
	require.ErrorIs(t, <-triggerErrors, streamstore.ErrCapacityExceeded)
	require.ErrorIs(t, <-triggerErrors, streamstore.ErrCapacityExceeded)
	require.Equal(t, int64(80), streamLength(t, redisClient, streamName))
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) == 8
	}, 4*time.Second, 10*time.Millisecond)
	require.NoError(t, firstStore.Write(context.Background(), firstTrigger))
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) == 8
	}, 4*time.Second, 10*time.Millisecond)
	requireStreamAccountingConsistent(t, redisClient, streamName)
}

func TestStreamStoreMessageSizeLimit(t *testing.T) {
	store, redisClient := newStreamTestStoreWithConfig(t, config.StreamStoreConfig{
		Backend:                       config.StreamStoreBackendRedis,
		RedisURL:                      streamTestRedisURL,
		MaxMessageBytes:               32,
		EstimatedMessageOverheadBytes: 1,
		TrimTriggerPercent:            90,
		TrimTargetPercent:             80,
		TrimWorkers:                   2,
	})
	streamName := "message-size-" + newRequestID()
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, streamName) })

	acceptedInput := streamInput("flow-a", streamName, 0, "accepted")
	acceptedInput.StreamCapacityBytes = 1 << 20
	require.NoError(t, store.Write(context.Background(), acceptedInput))

	oversizedInput := streamInput("flow-a", streamName, 1, string(make([]byte, 32)))
	oversizedInput.StreamCapacityBytes = 1 << 20
	require.ErrorIs(t, store.Write(context.Background(), oversizedInput), streamstore.ErrMessageTooLarge)
	require.Equal(t, int64(1), streamLength(t, redisClient, streamName))

	defaultStore, _ := newStreamTestStore(t)
	defaultStreamName := "default-message-size-" + newRequestID()
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, defaultStreamName) })
	defaultInput := streamInput(
		"flow-a",
		defaultStreamName,
		0,
		string(make([]byte, config.DefaultStreamMaxMessageBytes)),
	)
	defaultInput.StreamCapacityBytes = 1 << 20
	require.ErrorIs(t, defaultStore.Write(context.Background(), defaultInput), streamstore.ErrMessageTooLarge)
	require.Equal(t, int64(0), streamLength(t, redisClient, defaultStreamName))
}

func TestStreamStoreMemoryResumeIdempotencyAndBlockingRead(t *testing.T) {
	store := newMemoryStreamTestStore(t)
	streamName := "memory-resume-" + newRequestID()
	firstInput := streamInput("flow-a", streamName, 0, "first")
	firstInput.StreamCapacityBytes = 1 << 20
	require.NoError(t, store.Write(context.Background(), firstInput))

	firstMessage := readOneMessage(t, store, "flow-a", streamName, "")
	require.Equal(t, "first", firstMessage.Value.GetStringValue())
	require.Equal(t, firstInput.PublicIdempotencyKey, firstMessage.IdempotencyKey)
	firstToken, err := streamstore.EncodeResumeToken(
		streamTestFlowType,
		"flow-a",
		streamName,
		firstMessage.MessageID,
	)
	require.NoError(t, err)

	duplicate := firstInput
	duplicate.Value = stringValue("duplicate")
	require.NoError(t, store.Write(context.Background(), duplicate))
	require.Len(t, readAvailableMessages(t, store, "flow-a", streamName), 1)

	readResult := make(chan *streamstore.Message, 1)
	readErrors := make(chan error, 1)
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	go func() {
		message, readErr := store.Read(readCtx, streamTestFlowType, "flow-a", streamName, firstToken)
		if readErr != nil {
			readErrors <- readErr
			return
		}
		readResult <- message
	}()
	otherFlow := streamInput("flow-b", streamName, 1, "other-flow")
	otherFlow.StreamCapacityBytes = 1 << 20
	require.NoError(t, store.Write(context.Background(), otherFlow))
	require.Never(t, func() bool {
		return len(readResult) != 0 || len(readErrors) != 0
	}, 200*time.Millisecond, 10*time.Millisecond)
	secondInput := streamInput("flow-a", streamName, 2, "second")
	secondInput.StreamCapacityBytes = 1 << 20
	require.NoError(t, store.Write(context.Background(), secondInput))
	select {
	case readErr := <-readErrors:
		require.NoError(t, readErr)
	case message := <-readResult:
		require.Equal(t, "second", message.Value.GetStringValue())
	case <-readCtx.Done():
		require.FailNow(t, "Memory Stream read did not resume", readCtx.Err())
	}
}

func TestStreamStoreMemoryTrimWatermarks(t *testing.T) {
	store := newMemoryStreamTestStore(t)
	streamName := "memory-watermarks-" + newRequestID()
	baseInput := streamInput("flow-a", streamName, 0, "payload-0000")
	charge := estimatedCharge(baseInput, 1)
	capacity := charge * 10
	for index := 0; index < 8; index++ {
		input := streamInput("flow-a", streamName, index, "payload-0000")
		input.StreamCapacityBytes = capacity
		require.NoError(t, store.Write(context.Background(), input))
	}
	require.Len(t, readAvailableMessages(t, store, "flow-a", streamName), 8)

	trigger := streamInput("flow-a", streamName, 8, "payload-0000")
	trigger.StreamCapacityBytes = capacity
	require.NoError(t, store.Write(context.Background(), trigger))
	require.Eventually(t, func() bool {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 100*time.Millisecond)
		message, readErr := store.Read(readCtx, streamTestFlowType, "flow-a", streamName, "")
		cancelRead()
		return readErr == nil && message.IdempotencyKey == "public-01"
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{
		"public-01",
		"public-02",
		"public-03",
		"public-04",
		"public-05",
		"public-06",
		"public-07",
		"public-08",
	}, messageKeys(readAvailableMessages(t, store, "flow-a", streamName)))
}

func TestStreamStoreMemoryConcurrentCapacityTriggersDoNotOverTrim(t *testing.T) {
	store := newMemoryStreamTestStore(t)
	streamName := "memory-trim-" + newRequestID()
	baseInput := streamInput("flow-a", streamName, 0, "payload-0000")
	charge := estimatedCharge(baseInput, 1)
	for index := 0; index < 80; index++ {
		input := streamInput("flow-a", streamName, index, "payload-0000")
		input.StreamCapacityBytes = charge * 100
		require.NoError(t, store.Write(context.Background(), input))
	}

	triggerStart := make(chan struct{})
	triggerErrors := make(chan error, 20)
	var writers sync.WaitGroup
	for index := 80; index < 100; index++ {
		writers.Add(1)
		go func(messageIndex int) {
			defer writers.Done()
			<-triggerStart
			input := streamInput("flow-a", streamName, messageIndex, "payload-0000")
			input.StreamCapacityBytes = charge * 10
			triggerErrors <- store.Write(context.Background(), input)
		}(index)
	}
	close(triggerStart)
	writers.Wait()
	close(triggerErrors)
	rejectedWrites := 0
	for writeErr := range triggerErrors {
		if writeErr == nil {
			continue
		}
		require.ErrorIs(t, writeErr, streamstore.ErrCapacityExceeded)
		rejectedWrites++
	}
	require.Positive(t, rejectedWrites)
	retained := waitForStreamMessageLimit(t, store, "flow-a", streamName, 9)
	require.NotEmpty(t, retained)
	require.GreaterOrEqual(t, len(retained), 8)
	require.LessOrEqual(t, len(retained), 9)

	lastToken, err := streamstore.EncodeResumeToken(
		streamTestFlowType,
		"flow-a",
		streamName,
		retained[len(retained)-1].MessageID,
	)
	require.NoError(t, err)
	rewritten := baseInput
	rewritten.Value = stringValue("rewritten")
	rewritten.StreamCapacityBytes = charge * 10
	require.NoError(t, store.Write(context.Background(), rewritten))
	rewrittenMessage := readOneMessage(t, store, "flow-a", streamName, lastToken)
	require.Equal(t, "rewritten", rewrittenMessage.Value.GetStringValue())
}

func TestStreamStoreMemoryResumeTokenSurvivesProcessRestartBestEffort(t *testing.T) {
	streamName := "memory-restart-" + newRequestID()
	firstStore := newMemoryStreamTestStore(t)
	firstInput := streamInput("flow-a", streamName, 0, "before-restart")
	firstInput.StreamCapacityBytes = 1 << 20
	require.NoError(t, firstStore.Write(context.Background(), firstInput))
	firstMessage := readOneMessage(t, firstStore, "flow-a", streamName, "")
	resumeToken, err := streamstore.EncodeResumeToken(
		streamTestFlowType,
		"flow-a",
		streamName,
		firstMessage.MessageID,
	)
	require.NoError(t, err)
	require.NoError(t, firstStore.Close())
	require.Eventually(t, func() bool {
		return time.Now().UnixMilli() > firstMessage.CreatedTime.UnixMilli()
	}, time.Second, time.Millisecond)

	secondStore := newMemoryStreamTestStore(t)
	afterRestart := streamInput("flow-a", streamName, 1, "after-restart")
	afterRestart.StreamCapacityBytes = 1 << 20
	require.NoError(t, secondStore.Write(context.Background(), afterRestart))
	message := readOneMessage(t, secondStore, "flow-a", streamName, resumeToken)
	require.Equal(t, "after-restart", message.Value.GetStringValue())
}

func TestStreamStoreBackendConfiguration(t *testing.T) {
	disabledStore, err := streamstore.New(&config.StreamStoreConfig{}, log.NewNoop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, disabledStore.Close()) })
	disabledInput := streamInput("flow-a", "disabled", 0, "value")
	disabledInput.StreamCapacityBytes = 1024
	require.ErrorIs(t, disabledStore.Write(context.Background(), disabledInput), streamstore.ErrDisabled)

	_, err = streamstore.New(&config.StreamStoreConfig{RedisURL: streamTestRedisURL}, log.NewNoop())
	require.ErrorContains(t, err, "redisURL requires redis backend")
	_, err = streamstore.New(&config.StreamStoreConfig{
		Backend:  config.StreamStoreBackendMemory,
		RedisURL: streamTestRedisURL,
	}, log.NewNoop())
	require.ErrorContains(t, err, "memory backend does not use redisURL")
	_, err = streamstore.New(&config.StreamStoreConfig{
		Backend: config.StreamStoreBackendRedis,
	}, log.NewNoop())
	require.ErrorContains(t, err, "redis backend requires redisURL")
	_, err = streamstore.New(&config.StreamStoreConfig{
		Backend: "unsupported",
	}, log.NewNoop())
	require.ErrorContains(t, err, "unsupported stream store backend")
}

func TestStreamAPITemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	streamName := "api-" + newRequestID()
	redisClient := newStreamRedisClient(t)
	t.Cleanup(func() { deleteStreamTestKeys(t, redisClient, streamName) })
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		StreamStore: config.StreamStoreConfig{
			Backend:         config.StreamStoreBackendRedis,
			RedisURL:        streamTestRedisURL,
			MaxMessageBytes: 64,
		},
	})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	flowID := "nonexistent-" + newRequestID()
	_, err := flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("missing-key"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("first"),
		IdempotencyKey:      "client-key",
	})
	require.NoError(t, err)
	response, err := flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
		FlowType:        streamTestFlowType,
		StreamName:      streamName,
		WaitTimeSeconds: 2,
	})
	require.NoError(t, err)
	require.Equal(t, "first", response.GetMessage().GetValue().GetStringValue())
	require.Equal(t, "client-key", response.GetMessage().GetIdempotencyKey())
	require.NotEmpty(t, response.GetMessage().GetResumeToken())
	require.WithinDuration(t, time.Now(), response.GetMessage().GetCreatedTime().AsTime(), 5*time.Second)
	chargedBytes, err := redisClient.Get(context.Background(), streamTestBaseKey(streamName)+":charged").Int64()
	require.NoError(t, err)
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: chargedBytes*2 - 1,
		Value:               stringValue("first"),
		IdempotencyKey:      "second-key",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, int64(1), streamLength(t, redisClient, streamName))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue(string(make([]byte, 64))),
		IdempotencyKey:      "message-too-large",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, int64(1), streamLength(t, redisClient, streamName))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1,
		Value:               stringValue("too-large"),
		IdempotencyKey:      "too-large",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	_, err = flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
		FlowType:        streamTestFlowType,
		StreamName:      streamName,
		ResumeToken:     "malformed",
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	var readFinished atomic.Bool
	readResult := make(chan *dexpb.ReadStreamResponse, 1)
	readErrors := make(chan error, 1)
	go func() {
		blockingResponse, readErr := flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
			FlowId:          flowID,
			FlowType:        streamTestFlowType,
			StreamName:      streamName,
			ResumeToken:     response.GetMessage().GetResumeToken(),
			WaitTimeSeconds: 5,
		})
		if readErr != nil {
			readErrors <- readErr
		} else {
			readResult <- blockingResponse
		}
		readFinished.Store(true)
	}()
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              "different-flow",
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("other"),
		IdempotencyKey:      "other-key",
	})
	require.NoError(t, err)
	require.Never(t, readFinished.Load, 200*time.Millisecond, 10*time.Millisecond)
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              flowID,
		FlowType:            streamTestFlowType,
		StreamName:          streamName,
		StreamCapacityBytes: 1 << 20,
		Value:               stringValue("second"),
		IdempotencyKey:      "run-id#step-id",
	})
	require.NoError(t, err)
	latestToken := ""
	select {
	case readErr := <-readErrors:
		require.NoError(t, readErr)
	case blockingResponse := <-readResult:
		require.Equal(t, "second", blockingResponse.GetMessage().GetValue().GetStringValue())
		require.Equal(t, "run-id#step-id", blockingResponse.GetMessage().GetIdempotencyKey())
		latestToken = blockingResponse.GetMessage().GetResumeToken()
	case <-ctx.Done():
		require.FailNow(t, "blocking ReadStream did not finish", ctx.Err())
	}
	_, err = flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
		FlowType:        streamTestFlowType,
		StreamName:      streamName,
		ResumeToken:     latestToken,
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
}

func TestStreamFailureIsolationTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		StreamStore: config.StreamStoreConfig{
			Backend:  config.StreamStoreBackendRedis,
			RedisURL: "redis://127.0.0.1:1/0",
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runtime.FlowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              "flow",
		FlowType:            streamTestFlowType,
		StreamName:          "unavailable",
		StreamCapacityBytes: 1024,
		Value:               stringValue("value"),
		IdempotencyKey:      "key",
	})
	require.Equal(t, codes.Unavailable, status.Code(err))
	health, err := runtime.FlowClient.HealthCheck(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, "OK", health.GetCondition())
}

func TestStreamDisabledTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runtime.FlowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:              "flow",
		FlowType:            streamTestFlowType,
		StreamName:          "disabled",
		StreamCapacityBytes: 1024,
		Value:               stringValue("value"),
		IdempotencyKey:      "key",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func BenchmarkStreamStoreWrite(b *testing.B) {
	for _, payloadBytes := range []int{128, 4096, 65536} {
		b.Run(strconv.Itoa(payloadBytes), func(b *testing.B) {
			store, err := streamstore.New(&config.StreamStoreConfig{
				Backend:            config.StreamStoreBackendRedis,
				RedisURL:           streamTestRedisURL,
				TrimWorkers:        2,
				TrimTriggerPercent: 90,
				TrimTargetPercent:  80,
			}, log.NewNoop())
			require.NoError(b, err)
			defer func() { require.NoError(b, store.Close()) }()
			streamName := "benchmark-" + newRequestID()
			redisClient := newStreamRedisClient(b)
			defer deleteStreamTestKeys(b, redisClient, streamName)
			payload := string(make([]byte, payloadBytes))
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				input := streamInput("flow-"+strconv.Itoa(index%100), streamName, index, payload)
				input.StreamCapacityBytes = 1 << 40
				require.NoError(b, store.Write(context.Background(), input))
			}
		})
	}
}

func newStreamTestStore(t testing.TB) (*streamstore.Store, *redis.Client) {
	t.Helper()
	return newStreamTestStoreWithConfig(t, config.StreamStoreConfig{
		Backend:                       config.StreamStoreBackendRedis,
		RedisURL:                      streamTestRedisURL,
		EstimatedMessageOverheadBytes: 1,
		TrimTriggerPercent:            90,
		TrimTargetPercent:             80,
		BackgroundTrimBatchSize:       1,
		TrimLeaseTTL:                  2 * time.Second,
		TrimLeaseRetry:                5 * time.Millisecond,
		TrimBatchYieldTime:            100 * time.Microsecond,
		TrimWorkers:                   2,
	})
}

func newMemoryStreamTestStore(t testing.TB) *streamstore.Store {
	t.Helper()
	store, err := streamstore.New(&config.StreamStoreConfig{
		Backend:                       config.StreamStoreBackendMemory,
		EstimatedMessageOverheadBytes: 1,
		TrimTriggerPercent:            90,
		TrimTargetPercent:             80,
		BackgroundTrimBatchSize:       256,
		TrimBatchYieldTime:            100 * time.Microsecond,
		TrimWorkers:                   4,
	}, log.NewNoop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func newStreamTestStoreWithConfig(
	t testing.TB,
	streamStoreConfig config.StreamStoreConfig,
) (*streamstore.Store, *redis.Client) {
	t.Helper()
	store, err := streamstore.New(&streamStoreConfig, log.NewNoop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store, newStreamRedisClient(t)
}

func newStreamRedisClient(t testing.TB) *redis.Client {
	t.Helper()
	options, err := redis.ParseURL(streamTestRedisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

func streamInput(flowID string, streamName string, index int, value string) streamstore.WriteInput {
	return streamstore.WriteInput{
		FlowID:               flowID,
		FlowType:             streamTestFlowType,
		StreamName:           streamName,
		Value:                &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}},
		InternalIdentity:     fmt.Sprintf("internal-%02d", index),
		PublicIdempotencyKey: fmt.Sprintf("public-%02d", index),
	}
}

func estimatedCharge(input streamstore.WriteInput, overhead int64) int64 {
	return int64(proto.Size(input.Value)+len(input.FlowID)+len(input.InternalIdentity)+len(input.PublicIdempotencyKey)) + overhead
}

func readAvailableMessages(
	t testing.TB,
	store *streamstore.Store,
	flowID string,
	streamName string,
) []*streamstore.Message {
	t.Helper()
	messages, err := availableStreamMessages(store, flowID, streamName)
	require.NoError(t, err)
	return messages
}

func availableStreamMessages(
	store *streamstore.Store,
	flowID string,
	streamName string,
) ([]*streamstore.Message, error) {
	var messages []*streamstore.Message
	resumeToken := ""
	for {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 30*time.Millisecond)
		message, err := store.Read(readCtx, streamTestFlowType, flowID, streamName, resumeToken)
		cancelRead()
		if err != nil {
			if errors.Is(err, streamstore.ErrWaitTimeout) {
				return messages, nil
			}
			return nil, err
		}
		messages = append(messages, message)
		resumeToken, err = streamstore.EncodeResumeToken(
			streamTestFlowType,
			flowID,
			streamName,
			message.MessageID,
		)
		if err != nil {
			return nil, err
		}
	}
}

func waitForStreamMessageLimit(
	t testing.TB,
	store *streamstore.Store,
	flowID string,
	streamName string,
	maximumCount int,
) []*streamstore.Message {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var messages []*streamstore.Message
	var readErr error
	for {
		messages, readErr = availableStreamMessages(store, flowID, streamName)
		if readErr == nil && len(messages) <= maximumCount {
			return messages
		}
		select {
		case <-deadline.C:
			require.NoError(t, readErr)
			require.LessOrEqual(t, len(messages), maximumCount)
			return nil
		case <-ticker.C:
		}
	}
}

func readOneMessage(
	t testing.TB,
	store *streamstore.Store,
	flowID string,
	streamName string,
	resumeToken string,
) *streamstore.Message {
	t.Helper()
	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()
	message, err := store.Read(readCtx, streamTestFlowType, flowID, streamName, resumeToken)
	require.NoError(t, err)
	return message
}

func messageKeys(messages []*streamstore.Message) []string {
	keys := make([]string, len(messages))
	for index, message := range messages {
		keys[index] = message.IdempotencyKey
	}
	return keys
}

func streamLength(t testing.TB, client *redis.Client, streamName string) int64 {
	t.Helper()
	return streamLengthForFlowType(t, client, streamTestFlowType, streamName)
}

func streamLengthForFlowType(
	t testing.TB,
	client *redis.Client,
	flowType string,
	streamName string,
) int64 {
	t.Helper()
	length, err := client.XLen(
		context.Background(),
		streamTestBaseKeyForFlowType(flowType, streamName)+":fifo",
	).Result()
	require.NoError(t, err)
	return length
}

func requireStreamAccountingConsistent(t testing.TB, client *redis.Client, streamName string) {
	t.Helper()
	baseKey := streamTestBaseKey(streamName)
	entries, err := client.XRange(context.Background(), baseKey+":fifo", "-", "+").Result()
	require.NoError(t, err)
	var expectedBytes int64
	for _, entry := range entries {
		chargedValue, ok := entry.Values["c"]
		require.True(t, ok)
		chargedBytes, parseErr := strconv.ParseInt(fmt.Sprint(chargedValue), 10, 64)
		require.NoError(t, parseErr)
		expectedBytes += chargedBytes
	}
	actualBytes, err := client.Get(context.Background(), baseKey+":charged").Int64()
	require.NoError(t, err)
	require.Equal(t, expectedBytes, actualBytes)
}

func deleteStreamTestKeys(t testing.TB, client *redis.Client, streamName string) {
	t.Helper()
	deleteStreamTestKeysForFlowType(t, client, streamTestFlowType, streamName)
}

func deleteStreamTestKeysForFlowType(
	t testing.TB,
	client *redis.Client,
	flowType string,
	streamName string,
) {
	t.Helper()
	keys, err := client.Keys(
		context.Background(),
		streamTestBaseKeyForFlowType(flowType, streamName)+"*",
	).Result()
	require.NoError(t, err)
	if len(keys) > 0 {
		require.NoError(t, client.Del(context.Background(), keys...).Err())
	}
}

func streamTestBaseKey(streamName string) string {
	return streamTestBaseKeyForFlowType(streamTestFlowType, streamName)
}

func streamTestBaseKeyForFlowType(flowType string, streamName string) string {
	streamScope := fmt.Sprintf("%d:%s%d:%s", len(flowType), flowType, len(streamName), streamName)
	return fmt.Sprintf("dex:stream:v1:%x", sha256.Sum256([]byte(streamScope)))
}
