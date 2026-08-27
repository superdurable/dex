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

const streamTestRedisURL = "redis://127.0.0.1:6379/15"

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
		inputs[index].MaxEstimatedBytes = charge * 10
		require.NoError(t, store.Write(context.Background(), inputs[index]))
	}
	inputs[10].MaxEstimatedBytes = charge * 10
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

	firstToken, err := streamstore.EncodeResumeToken("flow-a", streamName, flowAMessages[0].RedisID)
	require.NoError(t, err)
	nextMessage := readOneMessage(t, store, "flow-a", streamName, firstToken)
	require.Equal(t, "public-04", nextMessage.IdempotencyKey)

	duplicate := inputs[8]
	duplicate.Value = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "different"}}
	require.NoError(t, store.Write(context.Background(), duplicate))
	require.Equal(t, int64(8), streamLength(t, redisClient, streamName))
	beforeLastToken, err := streamstore.EncodeResumeToken("flow-a", streamName, flowAMessages[2].RedisID)
	require.NoError(t, err)
	retainedOriginal := readOneMessage(t, store, "flow-a", streamName, beforeLastToken)
	require.Equal(t, "value-0000", retainedOriginal.Value.GetStringValue())

	trimTrigger := inputs[10]
	trimTrigger.MaxEstimatedBytes = charge * 3
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

	wrongScopeToken, err := streamstore.EncodeResumeToken("flow-b", streamName, flowBMessages[0].RedisID)
	require.NoError(t, err)
	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()
	_, err = store.Read(readCtx, "flow-a", streamName, wrongScopeToken)
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
		input.MaxEstimatedBytes = capacity
		require.NoError(t, store.Write(context.Background(), input))
	}
	require.Never(t, func() bool {
		return streamLength(t, redisClient, streamName) != 8
	}, 200*time.Millisecond, 10*time.Millisecond)

	trigger := streamInput("flow-a", streamName, 8, "payload-0000")
	trigger.MaxEstimatedBytes = capacity
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
	original.MaxEstimatedBytes = charge * 100
	require.NoError(t, store.Write(context.Background(), original))
	for index := 1; index < 80; index++ {
		input := streamInput("flow-a", streamName, index, "filler-0000")
		input.MaxEstimatedBytes = charge * 100
		require.NoError(t, store.Write(context.Background(), input))
	}
	trigger := streamInput("flow-a", streamName, 80, "trigger-000")
	trigger.MaxEstimatedBytes = charge * 2
	require.ErrorIs(t, store.Write(context.Background(), trigger), streamstore.ErrCapacityExceeded)
	require.Eventually(t, func() bool {
		return streamLength(t, redisClient, streamName) <= 1
	}, 3*time.Second, 10*time.Millisecond)
	requireStreamAccountingConsistent(t, redisClient, streamName)

	original.Value = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "rewritten-0"}}
	original.MaxEstimatedBytes = charge * 2
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
			input.MaxEstimatedBytes = charge * 100
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
	firstTrigger.MaxEstimatedBytes = charge * 10
	secondTrigger := streamInput("flow-a", streamName, 81, "payload-0000")
	secondTrigger.MaxEstimatedBytes = charge * 10
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
	acceptedInput.MaxEstimatedBytes = 1 << 20
	require.NoError(t, store.Write(context.Background(), acceptedInput))

	oversizedInput := streamInput("flow-a", streamName, 1, string(make([]byte, 32)))
	oversizedInput.MaxEstimatedBytes = 1 << 20
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
	defaultInput.MaxEstimatedBytes = 1 << 20
	require.ErrorIs(t, defaultStore.Write(context.Background(), defaultInput), streamstore.ErrMessageTooLarge)
	require.Equal(t, int64(0), streamLength(t, redisClient, defaultStreamName))
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
			RedisURL:        streamTestRedisURL,
			MaxMessageBytes: 64,
		},
	})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	flowID := "nonexistent-" + newRequestID()
	_, err := flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:            flowID,
		StreamName:        streamName,
		MaxEstimatedBytes: 1 << 20,
		Value:             stringValue("missing-key"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:            flowID,
		StreamName:        streamName,
		MaxEstimatedBytes: 1 << 20,
		Value:             stringValue("first"),
		IdempotencyKey:    "client-key",
	})
	require.NoError(t, err)
	response, err := flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
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
		FlowId:            flowID,
		StreamName:        streamName,
		MaxEstimatedBytes: chargedBytes*2 - 1,
		Value:             stringValue("first"),
		IdempotencyKey:    "second-key",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, int64(1), streamLength(t, redisClient, streamName))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:            flowID,
		StreamName:        streamName,
		MaxEstimatedBytes: 1 << 20,
		Value:             stringValue(string(make([]byte, 64))),
		IdempotencyKey:    "message-too-large",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Equal(t, int64(1), streamLength(t, redisClient, streamName))
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:            flowID,
		StreamName:        streamName,
		MaxEstimatedBytes: 1,
		Value:             stringValue("too-large"),
		IdempotencyKey:    "too-large",
	})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	_, err = flowClient.ReadStream(ctx, &dexpb.ReadStreamRequest{
		FlowId:          flowID,
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
		FlowId:            "different-flow",
		StreamName:        streamName,
		MaxEstimatedBytes: 1 << 20,
		Value:             stringValue("other"),
		IdempotencyKey:    "other-key",
	})
	require.NoError(t, err)
	require.Never(t, readFinished.Load, 200*time.Millisecond, 10*time.Millisecond)
	_, err = flowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:            flowID,
		StreamName:        streamName,
		MaxEstimatedBytes: 1 << 20,
		Value:             stringValue("second"),
		IdempotencyKey:    "run-id#step-id",
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
		StreamStore: config.StreamStoreConfig{RedisURL: "redis://127.0.0.1:1/0"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runtime.FlowClient.WriteStream(ctx, &dexpb.WriteStreamRequest{
		FlowId:            "flow",
		StreamName:        "unavailable",
		MaxEstimatedBytes: 1024,
		Value:             stringValue("value"),
		IdempotencyKey:    "key",
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
		FlowId:            "flow",
		StreamName:        "disabled",
		MaxEstimatedBytes: 1024,
		Value:             stringValue("value"),
		IdempotencyKey:    "key",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func BenchmarkStreamStoreWrite(b *testing.B) {
	for _, payloadBytes := range []int{128, 4096, 65536} {
		b.Run(strconv.Itoa(payloadBytes), func(b *testing.B) {
			store, err := streamstore.New(&config.StreamStoreConfig{
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
				input.MaxEstimatedBytes = 1 << 40
				require.NoError(b, store.Write(context.Background(), input))
			}
		})
	}
}

func newStreamTestStore(t testing.TB) (*streamstore.Store, *redis.Client) {
	t.Helper()
	return newStreamTestStoreWithConfig(t, config.StreamStoreConfig{
		RedisURL:                      streamTestRedisURL,
		EstimatedMessageOverheadBytes: 1,
		TrimTriggerPercent:            90,
		TrimTargetPercent:             80,
		TrimWorkers:                   2,
	})
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
	var messages []*streamstore.Message
	resumeToken := ""
	for {
		readCtx, cancelRead := context.WithTimeout(context.Background(), 30*time.Millisecond)
		message, err := store.Read(readCtx, flowID, streamName, resumeToken)
		cancelRead()
		if err != nil {
			require.ErrorIs(t, err, streamstore.ErrWaitTimeout)
			return messages
		}
		messages = append(messages, message)
		resumeToken, err = streamstore.EncodeResumeToken(flowID, streamName, message.RedisID)
		require.NoError(t, err)
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
	message, err := store.Read(readCtx, flowID, streamName, resumeToken)
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
	length, err := client.XLen(context.Background(), streamTestBaseKey(streamName)+":fifo").Result()
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
	keys, err := client.Keys(context.Background(), streamTestBaseKey(streamName)+"*").Result()
	require.NoError(t, err)
	if len(keys) > 0 {
		require.NoError(t, client.Del(context.Background(), keys...).Err())
	}
}

func streamTestBaseKey(streamName string) string {
	return fmt.Sprintf("dex:stream:v1:%x", sha256.Sum256([]byte(streamName)))
}
